package dynamo

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

// BeginQueueRestart closes admission before any old request is archived.
func (s *Store) BeginQueueRestart(ctx context.Context, event *models.Event) error {
	if event == nil || event.ID == "" {
		return fmt.Errorf("begin queue restart: %w", os.ErrInvalid)
	}
	condition := "attribute_exists(id) AND attribute_not_exists(restart_in_progress) AND (attribute_not_exists(deleting) OR deleting = :not_deleting) AND queue_version = :expected"
	if event.QueueVersion == 0 {
		condition = "attribute_exists(id) AND attribute_not_exists(restart_in_progress) AND (attribute_not_exists(deleting) OR deleting = :not_deleting) AND (queue_version = :expected OR attribute_not_exists(queue_version))"
	}
	updateExpr := "SET #status = :ended, restart_in_progress = :true, updated_at = :now, #ttl = :zero ADD queue_version :one"
	names := map[string]string{"#status": "status", "#ttl": "ttl"}
	vals := map[string]types.AttributeValue{
		":ended": avString(string(models.EventStatusEnded)), ":true": avBool(true),
		":now": avString(timestamp(time.Now().UTC())), ":zero": avNumber(0),
		":one": avNumber(1), ":expected": avNumber(event.QueueVersion),
		":not_deleting": avBool(false),
	}
	var err error
	if _, hasFence := store.SongbookHostFenceFromContext(ctx); hasFence {
		txItems := s.appendHostFence(ctx, []types.TransactWriteItem{{
			Update: &types.Update{
				TableName: aws.String(s.tables.events), Key: stringKey("id", event.ID),
				UpdateExpression: aws.String(updateExpr), ConditionExpression: aws.String(condition),
				ExpressionAttributeNames: names, ExpressionAttributeValues: vals,
			},
		}})
		_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: txItems})
		if err != nil && isConditional(err) {
			if fenceErr := s.checkHostFenceError(ctx); fenceErr != nil {
				return fenceErr
			}
		}
	} else {
		_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName: aws.String(s.tables.events), Key: stringKey("id", event.ID),
			UpdateExpression: aws.String(updateExpr), ConditionExpression: aws.String(condition),
			ExpressionAttributeNames: names, ExpressionAttributeValues: vals,
		})
	}
	return s.restartWriteError(ctx, event.ID, event.QueueVersion, err)
}

// ArchiveRestartBatch changes request copies and the public queue version
// in one transaction. The event marker prevents competing queue writers.
func (s *Store) ArchiveRestartBatch(ctx context.Context, eventID string, expectedVersion int64, requests []*models.SongRequest) error {
	if eventID == "" || len(requests) == 0 || len(requests) > 99 {
		return fmt.Errorf("archive restart batch: %w", os.ErrInvalid)
	}
	now := timestamp(time.Now().UTC())
	items := make([]types.TransactWriteItem, 0, len(requests)+1)
	seen := make(map[string]bool, len(requests))
	for _, req := range requests {
		if req == nil || req.ID == "" || req.EventID != eventID || seen[req.ID] {
			return fmt.Errorf("archive restart request: %w", os.ErrInvalid)
		}
		seen[req.ID] = true
		status := req.Status
		updateExpr := "SET #status = :new, updated_at = :now REMOVE #ttl"
		exprVals := map[string]types.AttributeValue{
			":new":      avString(string(status)),
			":old":      avString(string(req.Status)),
			":event_id": avString(eventID),
			":now":      avString(now),
		}
		if status == models.StatusPending || status == models.StatusAccepted || status == models.StatusUpNext {
			status = models.StatusCompleted
			exprVals[":new"] = avString(string(status))
			updateExpr = "SET #status = :new, updated_at = :now, completed_at = if_not_exists(completed_at, :now) REMOVE #ttl"
		} else if status == models.StatusCompleted && (req.CompletedAt == nil || req.CompletedAt.IsZero()) {
			fallback := req.UpdatedAt
			if fallback.IsZero() {
				fallback = req.CreatedAt
			}
			if !fallback.IsZero() {
				exprVals[":completed_at"] = avString(timestamp(fallback))
				updateExpr = "SET #status = :new, updated_at = :now, completed_at = if_not_exists(completed_at, :completed_at) REMOVE #ttl"
			}
		}
		items = append(items, types.TransactWriteItem{Update: &types.Update{
			TableName: aws.String(s.tables.eventRequests), Key: eventRequestKey(eventID, req.ID),
			UpdateExpression:          aws.String(updateExpr),
			ConditionExpression:       aws.String("attribute_exists(id) AND event_id = :event_id AND #status = :old"),
			ExpressionAttributeNames:  map[string]string{"#status": "status", "#ttl": "ttl"},
			ExpressionAttributeValues: exprVals,
		}})
	}
	items = append(items, restartVersionUpdate(s.tables.events, eventID, expectedVersion, now))
	_, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: items})
	return s.restartWriteError(ctx, eventID, expectedVersion, err)
}

// ListRestartSlots queries strongly consistent slot items in the event-requests table.
func (s *Store) ListRestartSlots(ctx context.Context, eventID string) ([]string, error) {
	if eventID == "" {
		return nil, fmt.Errorf("list restart slots: %w", os.ErrInvalid)
	}
	slotsKey := "slots#" + eventID
	paginator := dynamodb.NewQueryPaginator(s.db, &dynamodb.QueryInput{
		TableName:              aws.String(s.tables.eventRequests),
		ConsistentRead:         aws.Bool(true),
		KeyConditionExpression: aws.String("event_id = :event_id"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":event_id": avString(slotsKey),
		},
		ProjectionExpression: aws.String("id"),
	})
	var ids []string
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list restart slots: %w", err)
		}
		for _, raw := range page.Items {
			if id, ok := raw["id"].(*types.AttributeValueMemberS); ok {
				ids = append(ids, id.Value)
			}
		}
	}
	return ids, nil
}

func (s *Store) DeleteRestartSlots(ctx context.Context, eventID string, expectedVersion int64, slotIDs []string) error {
	if eventID == "" || len(slotIDs) == 0 || len(slotIDs) > 99 {
		return fmt.Errorf("delete restart slots: %w", os.ErrInvalid)
	}
	now := timestamp(time.Now().UTC())
	items := make([]types.TransactWriteItem, 0, len(slotIDs)+1)
	slotsKey := "slots#" + eventID
	for _, id := range slotIDs {
		items = append(items, types.TransactWriteItem{Delete: &types.Delete{
			TableName:                 aws.String(s.tables.eventRequests),
			Key:                       eventRequestKey(slotsKey, id),
			ConditionExpression:       aws.String("attribute_exists(id) AND event_id = :event_id"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":event_id": avString(slotsKey)},
		}})
	}
	items = append(items, restartVersionUpdate(s.tables.events, eventID, expectedVersion, now))
	_, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: items})
	return s.restartWriteError(ctx, eventID, expectedVersion, err)
}

// FinishQueueRestart is called only after consistent request and slot reads
// found no remaining work. Its version guard invalidates racing clear workers.
func (s *Store) FinishQueueRestart(ctx context.Context, eventID string, expectedVersion int64) error {
	if eventID == "" {
		return fmt.Errorf("finish queue restart: %w", os.ErrInvalid)
	}
	updateExpr := "SET #status = :active, updated_at = :now, #ttl = :zero REMOVE restart_in_progress, ended_at ADD queue_version :one"
	condExpr := "restart_in_progress = :true AND queue_version = :expected"
	names := map[string]string{"#status": "status", "#ttl": "ttl"}
	vals := map[string]types.AttributeValue{
		":active": avString(string(models.EventStatusActive)), ":now": avString(timestamp(time.Now().UTC())),
		":zero": avNumber(0), ":one": avNumber(1), ":true": avBool(true),
		":expected": avNumber(expectedVersion),
	}
	var err error
	if _, hasFence := store.SongbookHostFenceFromContext(ctx); hasFence {
		txItems := s.appendHostFence(ctx, []types.TransactWriteItem{{
			Update: &types.Update{
				TableName: aws.String(s.tables.events), Key: stringKey("id", eventID),
				UpdateExpression: aws.String(updateExpr), ConditionExpression: aws.String(condExpr),
				ExpressionAttributeNames: names, ExpressionAttributeValues: vals,
			},
		}})
		_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: txItems})
		if err != nil && isConditional(err) {
			if fenceErr := s.checkHostFenceError(ctx); fenceErr != nil {
				return fenceErr
			}
		}
	} else {
		_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName: aws.String(s.tables.events), Key: stringKey("id", eventID),
			UpdateExpression: aws.String(updateExpr), ConditionExpression: aws.String(condExpr),
			ExpressionAttributeNames: names, ExpressionAttributeValues: vals,
		})
	}
	return s.restartWriteError(ctx, eventID, expectedVersion, err)
}

func restartVersionUpdate(table, eventID string, expectedVersion int64, now string) types.TransactWriteItem {
	return types.TransactWriteItem{Update: &types.Update{
		TableName: aws.String(table), Key: stringKey("id", eventID),
		UpdateExpression:    aws.String("ADD queue_version :one SET updated_at = :now"),
		ConditionExpression: aws.String("restart_in_progress = :true AND queue_version = :expected"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":one": avNumber(1), ":now": avString(now), ":true": avBool(true),
			":expected": avNumber(expectedVersion),
		},
	}}
}

func (s *Store) restartWriteError(ctx context.Context, eventID string, expectedVersion int64, err error) error {
	if err == nil {
		return nil
	}
	if isConditional(err) {
		if current, readErr := s.GetEvent(ctx, eventID); readErr == nil {
			if current.Deleting {
				return store.ErrEventDeleting
			}
			if current.QueueVersion != expectedVersion {
				return &store.QueueVersionConflictError{Current: current.QueueVersion}
			}
		}
	}
	return fmt.Errorf("queue restart write: %w", err)
}
