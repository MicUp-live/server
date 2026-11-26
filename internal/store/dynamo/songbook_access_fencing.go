package dynamo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

func (s *Store) appendEditFence(ctx context.Context, songbookID string, items []types.TransactWriteItem) []types.TransactWriteItem {
	if actorID := store.SongbookEditActorFromContext(ctx); actorID != "" && songbookID != "" {
		items = append(items, s.editGrantConditionCheck(songbookID, actorID))
	}
	return items
}

func (s *Store) appendHostFence(ctx context.Context, items []types.TransactWriteItem) []types.TransactWriteItem {
	fence, ok := store.SongbookHostFenceFromContext(ctx)
	if !ok {
		return items
	}
	items = append(items, s.songbookActiveConditionCheck(fence.SongbookID))
	if !fence.IsOwner {
		items = append(items, s.hostGrantConditionCheck(fence.SongbookID, fence.ActorID))
	}
	return items
}

func (s *Store) checkEditGrantError(ctx context.Context, songbookID string) error {
	actorID := store.SongbookEditActorFromContext(ctx)
	if actorID == "" {
		return nil
	}
	grant, err := s.GetSongbookGrant(ctx, songbookID, actorID)
	if errors.Is(err, store.ErrGrantNotFound) || (err == nil && !grant.CanEditCatalog) {
		return store.ErrPermissionDenied
	}
	return nil
}

func (s *Store) checkHostFenceError(ctx context.Context) error {
	fence, ok := store.SongbookHostFenceFromContext(ctx)
	if !ok {
		return nil
	}
	sb, err := s.getSongbookItem(ctx, fence.SongbookID)
	if errors.Is(err, os.ErrNotExist) {
		return os.ErrNotExist
	}
	if err == nil && sb.Deleting {
		return store.ErrSongbookDeleting
	}
	if !fence.IsOwner {
		grant, gErr := s.GetSongbookGrant(ctx, fence.SongbookID, fence.ActorID)
		if errors.Is(gErr, store.ErrGrantNotFound) || (gErr == nil && !grant.CanHostShows) {
			return store.ErrPermissionDenied
		}
	}
	return nil
}

// ConfirmSongbookDeletion marks a deleting songbook as confirmed after post-barrier dependency checks succeed.
func (s *Store) ConfirmSongbookDeletion(ctx context.Context, id string) error {
	now := time.Now().UTC()
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(s.tables.songbooks),
		Key:                 stringKey("id", id),
		UpdateExpression:    aws.String("SET deletion_confirmed = :true, updated_at = :updated"),
		ConditionExpression: aws.String("attribute_exists(id) AND deleting = :true"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":true":    avBool(true),
			":updated": avString(timestamp(now)),
		},
	})
	if isConditional(err) {
		return errNotExist()
	}
	if err != nil {
		return fmt.Errorf("confirm songbook deletion: %w", err)
	}
	return nil
}

// AbortSongbookDeletion clears an unconfirmed deletion barrier if new dependencies raced before confirmation.
func (s *Store) AbortSongbookDeletion(ctx context.Context, id string) error {
	now := time.Now().UTC()
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(s.tables.songbooks),
		Key:                 stringKey("id", id),
		UpdateExpression:    aws.String("SET deleting = :false, updated_at = :updated"),
		ConditionExpression: aws.String("attribute_exists(id) AND (attribute_not_exists(deletion_confirmed) OR deletion_confirmed = :false)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":false":   avBool(false),
			":updated": avString(timestamp(now)),
		},
	})
	if isConditional(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("abort songbook deletion: %w", err)
	}
	return nil
}

// DetachEventFromSongbook ends and detaches a collaborator's event from a songbook being deleted,
// preserving event and queue history under the event host's retention policy.
func (s *Store) DetachEventFromSongbook(ctx context.Context, eventID, songbookID, songbookName string, retention time.Duration) (*models.Event, error) {
	if retention <= 0 {
		retention = 14 * 24 * time.Hour
	}
	const maxAttempts = 5
	for attempt := 0; attempt < maxAttempts; attempt++ {
		ev, err := s.GetEvent(ctx, eventID)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, nil
			}
			return nil, err
		}
		if ev.SongbookID != songbookID {
			return ev, nil
		}
		now := time.Now().UTC()
		endedAt := ev.EndedAt
		if endedAt == nil {
			endedAt = &now
		}
		ttl := ev.PendingDetachTTL
		if ttl == 0 {
			ttl = ev.TTL
		}
		if ttl == 0 {
			ttl = endedAt.Add(retention).Unix()
		}
		trimmedName := strings.TrimSpace(songbookName)
		names := map[string]string{
			"#status":   "status",
			"#sb":       "songbook_id",
			"#detached": "detached_songbook_name",
			"#ended":    "ended_at",
			"#ttl":      "ttl",
			"#updated":  "updated_at",
			"#mv":       "metadata_version",
			"#qv":       "queue_version",
		}
		vals := map[string]types.AttributeValue{
			":ended":       avString(string(models.EventStatusEnded)),
			":sb_name":     avString(trimmedName),
			":ended_at":    avString(timestamp(*endedAt)),
			":true":        avBool(true),
			":pending_ttl": avNumber(ttl),
			":updated":     avString(timestamp(now)),
			":one":         avNumber(1),
			":cur_sb":      avString(songbookID),
		}
		cond := "attribute_exists(id) AND #sb = :cur_sb"
		if ev.QueueVersion == 0 {
			vals[":eqv"] = avNumber(0)
			cond += " AND (#qv = :eqv OR attribute_not_exists(#qv))"
		} else {
			vals[":eqv"] = avNumber(ev.QueueVersion)
			cond += " AND #qv = :eqv"
		}
		if ev.MetadataVersion == 0 {
			vals[":emv"] = avNumber(0)
			cond += " AND (#mv = :emv OR attribute_not_exists(#mv))"
		} else {
			vals[":emv"] = avNumber(ev.MetadataVersion)
			cond += " AND #mv = :emv"
		}
		// Phase 1: end the show, persist the exact retention deadline in pending_detach_ttl,
		// and fence with detach_in_progress while keeping songbook_id attached and removing
		// event ttl so neither reassignment nor DynamoDB expiry can hide unfinished work.
		_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName:                 aws.String(s.tables.events),
			Key:                       stringKey("id", ev.ID),
			UpdateExpression:          aws.String("SET #status = :ended, #detached = :sb_name, #ended = :ended_at, detach_in_progress = :true, pending_detach_ttl = :pending_ttl, #updated = :updated REMOVE restart_in_progress, #ttl ADD #mv :one, #qv :one"),
			ConditionExpression:       aws.String(cond),
			ExpressionAttributeNames:  names,
			ExpressionAttributeValues: vals,
		})
		if err != nil {
			if isConditional(err) {
				continue
			}
			return nil, fmt.Errorf("detach event %s from songbook %s: %w", eventID, songbookID, err)
		}
		ev.Status = models.EventStatusEnded
		ev.DetachedSongbookName = trimmedName
		ev.EndedAt = endedAt
		ev.Detaching = true
		ev.PendingDetachTTL = ttl
		ev.TTL = 0
		ev.QueueVersion++
		ev.MetadataVersion++
		// Phase 2: expire all request history rows before unlinking songbook_id or setting event ttl.
		if s.testDetachRetentionHook != nil {
			if err := s.testDetachRetentionHook(ctx, ev.ID, time.Unix(ttl, 0)); err != nil {
				return nil, fmt.Errorf("set retention on detached event %s: %w", ev.ID, err)
			}
		}
		if err := s.SetRequestsTTLByEvent(ctx, ev.ID, time.Unix(ttl, 0)); err != nil {
			return nil, fmt.Errorf("set retention on detached event %s: %w", ev.ID, err)
		}
		// Phase 3: clear songbook_id, restore exact event ttl, and lift detach_in_progress
		// now that request retention work is durably complete.
		_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName:           aws.String(s.tables.events),
			Key:                 stringKey("id", ev.ID),
			UpdateExpression:    aws.String("SET #sb = :empty, #ttl = :ttl, #updated = :updated REMOVE detach_in_progress, pending_detach_ttl ADD #mv :one, #qv :one"),
			ConditionExpression: aws.String("attribute_exists(id) AND #sb = :cur_sb AND #status = :ended AND detach_in_progress = :true"),
			ExpressionAttributeNames: map[string]string{
				"#sb":      "songbook_id",
				"#status":  "status",
				"#ttl":     "ttl",
				"#updated": "updated_at",
				"#mv":      "metadata_version",
				"#qv":      "queue_version",
			},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":empty":   avString(""),
				":cur_sb":  avString(songbookID),
				":ended":   avString(string(models.EventStatusEnded)),
				":true":    avBool(true),
				":ttl":     avNumber(ttl),
				":updated": avString(timestamp(time.Now().UTC())),
				":one":     avNumber(1),
			},
		})
		if err != nil {
			if isConditional(err) {
				continue
			}
			return nil, fmt.Errorf("finalize detach event %s from songbook %s: %w", eventID, songbookID, err)
		}
		ev.SongbookID = ""
		ev.TTL = ttl
		ev.QueueVersion++
		ev.MetadataVersion++
		return ev, nil
	}
	return nil, fmt.Errorf("detach event %s from songbook %s: exceeded conflict retries", eventID, songbookID)
}

func (s *Store) markImportJobDispatchReadyFenced(ctx context.Context, jobID, actorID string) error {
	existing, err := s.GetImportJob(ctx, jobID)
	if err != nil {
		return os.ErrNotExist
	}
	now := time.Now().UTC()
	txItems := []types.TransactWriteItem{
		{
			Update: &types.Update{
				TableName:                aws.String(s.tables.importJobs),
				Key:                      stringKey("id", jobID),
				UpdateExpression:         aws.String("SET dispatch_ready = :ready, #status = :queued, updated_at = :now"),
				ConditionExpression:      aws.String("attribute_exists(id) AND (#status = :pending OR #status = :queued)"),
				ExpressionAttributeNames: map[string]string{"#status": "status"},
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":ready":   avBool(true),
					":pending": avString(string(models.ImportJobPending)),
					":queued":  avString(string(models.ImportJobQueued)),
					":now":     avString(timestamp(now)),
				},
			},
		},
		s.songbookActiveConditionCheck(existing.SongbookID),
		s.editGrantConditionCheck(existing.SongbookID, actorID),
	}
	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: txItems})
	if isConditional(err) {
		if sb, sbErr := s.getSongbookItem(ctx, existing.SongbookID); sbErr == nil && sb.Deleting {
			return store.ErrSongbookDeleting
		}
		if gErr := s.checkEditGrantError(ctx, existing.SongbookID); gErr != nil {
			return gErr
		}
		reloaded, getErr := s.GetImportJob(ctx, jobID)
		if getErr != nil {
			return os.ErrNotExist
		}
		if reloaded.Status == models.ImportJobCompleted {
			return store.ErrImportJobAlreadyCompleted
		}
		if reloaded.Status == models.ImportJobFailed {
			return store.ErrImportJobTerminal
		}
		if reloaded.Status == models.ImportJobProcessing || reloaded.DispatchedAt != nil {
			return store.ErrImportJobAlreadyDispatched
		}
		return os.ErrNotExist
	}
	return err
}

func (s *Store) nextCatalogGenerationFenced(ctx context.Context, songbookID, actorID string) (int64, error) {
	const maxAttempts = 5
	for attempt := 0; attempt < maxAttempts; attempt++ {
		item, err := s.getSongbookItem(ctx, songbookID)
		if err != nil {
			return 0, err
		}
		if item.Deleting {
			return 0, fmt.Errorf("next catalog generation: %w", store.ErrSongbookDeleting)
		}
		cur := item.LastImportGeneration
		next := cur + 1
		cond := "attribute_exists(id) AND (attribute_not_exists(deleting) OR deleting = :not_deleting)"
		vals := map[string]types.AttributeValue{
			":next":         avNumber(next),
			":not_deleting": avBool(false),
		}
		if cur == 0 {
			cond += " AND (attribute_not_exists(last_import_generation) OR last_import_generation = :cur)"
			vals[":cur"] = avNumber(0)
		} else {
			cond += " AND last_import_generation = :cur"
			vals[":cur"] = avNumber(cur)
		}
		_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
			TransactItems: []types.TransactWriteItem{
				{
					Update: &types.Update{
						TableName:                 aws.String(s.tables.songbooks),
						Key:                       stringKey("id", songbookID),
						UpdateExpression:          aws.String("SET last_import_generation = :next"),
						ConditionExpression:       aws.String(cond),
						ExpressionAttributeValues: vals,
					},
				},
				s.editGrantConditionCheck(songbookID, actorID),
			},
		})
		if err == nil {
			return next, nil
		}
		if isConditional(err) {
			if current, getErr := s.getSongbookItem(ctx, songbookID); getErr == nil && current.Deleting {
				return 0, fmt.Errorf("next catalog generation: %w", store.ErrSongbookDeleting)
			} else if errors.Is(getErr, os.ErrNotExist) {
				return 0, errNotExist()
			}
			if gErr := s.checkEditGrantError(ctx, songbookID); gErr != nil {
				return 0, fmt.Errorf("next catalog generation: %w", gErr)
			}
			continue
		}
		return 0, fmt.Errorf("next catalog generation: %w", err)
	}
	return 0, fmt.Errorf("next catalog generation: exceeded conflict retries")
}
