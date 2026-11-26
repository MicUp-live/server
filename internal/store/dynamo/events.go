package dynamo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

func (s *Store) CreateEvent(ctx context.Context, ev *models.Event) error {
	if ev.ID == "" {
		ev.ID = uuid.NewString()
	}
	created, updated := nowForCreate(ev.CreatedAt)
	ev.CreatedAt = created
	ev.UpdatedAt = updated
	if ev.Status == "" {
		ev.Status = models.EventStatusActive
	}
	if ev.RotationMode == "" {
		ev.RotationMode = models.RotationModeRoundRobin
	}
	if ev.SubmissionDelay < 0 {
		ev.SubmissionDelay = 0
	}
	if ev.CurrentRound <= 0 {
		ev.CurrentRound = 1
	}
	if ev.QueueVersion <= 0 {
		ev.QueueVersion = 1
	}
	maxActive := ev.MaxActivePerSinger
	if maxActive < 0 {
		maxActive = 0
	}

	if ev.OpenKJKey == "" {
		ev.OpenKJKey = models.GenerateOpenKJKey(ev.ID)
	}
	ev.JoinCode = strings.ToUpper(strings.TrimSpace(ev.JoinCode))

	ev.SingerWeights = models.NormalizeSingerWeights(ev.SingerWeights)

	item := eventItem{
		ID:                 ev.ID,
		OwnerID:            ev.OwnerID,
		SongbookID:         ev.SongbookID,
		VenueName:          ev.VenueName,
		Name:               ev.Name,
		JoinCode:           ev.JoinCode,
		Status:             string(ev.Status),
		Recurring:          ev.Recurring,
		TipURL:             ev.TipURL,
		Announcement:       ev.Announcement,
		KioskPIN:           ev.KioskPIN,
		RotationMode:       ev.RotationMode,
		CurrentRound:       ev.CurrentRound,
		AutoAccept:         ev.AutoAccept,
		SubmissionDelay:    ev.SubmissionDelay,
		MaxActivePerSinger: &maxActive,
		AllowKeyChange:     &ev.AllowKeyChange,
		AllowWriteIns:      ev.AllowWriteIns,
		SingerWeights:      ev.SingerWeights,

		QueueVersion:    ev.QueueVersion,
		MetadataVersion: ev.MetadataVersion,
		Restarting:      ev.Restarting,
		OpenKJKey:       ev.OpenKJKey,
		CreatedAt:       timestamp(created),
		CreatedKey:      fmt.Sprintf("%s#%s", timestamp(created), ev.ID),
		UpdatedAt:       timestamp(updated),
		TTL:             ev.TTL,
	}
	if ev.EndedAt != nil {
		item.EndedAt = timestamp(*ev.EndedAt)
	}

	if strings.TrimSpace(ev.OwnerID) == "" {
		return fmt.Errorf("create event: owner is required: %w", os.ErrInvalid)
	}

	payload, err := marshal(item)
	if err != nil {
		return err
	}

	txItems := s.appendHostFence(ctx, []types.TransactWriteItem{
		{
			Put: &types.Put{
				TableName:           aws.String(s.tables.events),
				Item:                payload,
				ConditionExpression: aws.String("attribute_not_exists(id)"),
			},
		},
		{
			ConditionCheck: &types.ConditionCheck{
				TableName:           aws.String(s.tables.users),
				Key:                 stringKey("id", ev.OwnerID),
				ConditionExpression: aws.String("attribute_exists(id) AND (attribute_not_exists(deleting) OR deleting = :not_deleting)"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":not_deleting": avBool(false),
				},
			},
		},
	})
	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: txItems,
	})
	if err != nil {
		if isConditional(err) {
			if _, getErr := s.GetEvent(ctx, ev.ID); getErr == nil {
				return os.ErrExist
			}
			if fenceErr := s.checkHostFenceError(ctx); fenceErr != nil {
				return fenceErr
			}
			u, getErr := s.GetUser(ctx, ev.OwnerID)
			if errors.Is(getErr, os.ErrNotExist) {
				return os.ErrNotExist
			}
			if getErr == nil && u.Deleting {
				return store.ErrAccountDeleting
			}
		}
		return fmt.Errorf("create event: %w", err)
	}
	return nil
}

func (s *Store) GetEvent(ctx context.Context, id string) (*models.Event, error) {
	result, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(s.tables.events),
		Key:            stringKey("id", id),
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("get event %s: %w", id, err)
	}
	if err := missing(result.Item); err != nil {
		return nil, err
	}
	var item eventItem
	if err := unmarshal(result.Item, &item); err != nil {
		return nil, err
	}
	return modelEvent(item)
}

func (s *Store) GetEventByJoinCode(ctx context.Context, joinCode string) (*models.Event, error) {
	code := strings.ToUpper(strings.TrimSpace(joinCode))
	result, err := s.db.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.tables.events),
		IndexName:              aws.String(joinCodeIndex),
		KeyConditionExpression: aws.String("join_code = :join_code"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":join_code": avString(code),
		},
		Limit: aws.Int32(1),
	})
	if err != nil {
		return nil, fmt.Errorf("get event by code %s: %w", code, err)
	}
	if len(result.Items) == 0 {
		return nil, errNotExist()
	}
	var item eventItem
	if err := unmarshal(result.Items[0], &item); err != nil {
		return nil, err
	}
	return modelEvent(item)
}

func (s *Store) ListEventsByOwner(ctx context.Context, ownerID string) ([]*models.Event, error) {
	var events []*models.Event
	var start map[string]types.AttributeValue
	for {
		result, err := s.db.Scan(ctx, &dynamodb.ScanInput{
			TableName:                 aws.String(s.tables.events),
			FilterExpression:          aws.String("owner_id = :owner_id"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":owner_id": avString(ownerID)},
			ExclusiveStartKey:         start,
			ConsistentRead:            boolValue(true),
		})
		if err != nil {
			return nil, fmt.Errorf("list events by owner %s: %w", ownerID, err)
		}
		for _, raw := range result.Items {
			var item eventItem
			if err := unmarshal(raw, &item); err != nil {
				return nil, err
			}
			ev, err := modelEvent(item)
			if err != nil {
				return nil, err
			}
			events = append(events, ev)
		}
		if len(result.LastEvaluatedKey) == 0 {
			break
		}
		start = result.LastEvaluatedKey
	}
	sort.Slice(events, func(i, j int) bool {
		return events[i].CreatedAt.After(events[j].CreatedAt)
	})
	return events, nil
}

func (s *Store) ListEventsBySongbook(ctx context.Context, songbookID string) ([]*models.Event, error) {
	var events []*models.Event
	var start map[string]types.AttributeValue
	for {
		scan, err := s.db.Scan(ctx, &dynamodb.ScanInput{
			TableName:        aws.String(s.tables.events),
			FilterExpression: aws.String("songbook_id = :songbook_id"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":songbook_id": avString(songbookID),
			},
			ExclusiveStartKey: start,
			ConsistentRead:    boolValue(true),
		})
		if err != nil {
			return nil, fmt.Errorf("list events for songbook %s: %w", songbookID, err)
		}

		for _, raw := range scan.Items {
			var item eventItem
			if err := unmarshal(raw, &item); err != nil {
				return nil, err
			}
			ev, err := modelEvent(item)
			if err != nil {
				return nil, err
			}
			events = append(events, ev)
		}
		if len(scan.LastEvaluatedKey) == 0 {
			break
		}
		start = scan.LastEvaluatedKey
	}

	sort.SliceStable(events, func(i, j int) bool {
		return events[i].CreatedAt.After(events[j].CreatedAt)
	})
	return events, nil
}

// UpdateEvent commits a current event snapshot without bumping queue_version.
func (s *Store) UpdateEvent(ctx context.Context, ev *models.Event) error {
	return s.UpdateEventWithVersion(ctx, ev, false)
}

func (s *Store) buildEventUpdateExpr(ev *models.Event, bumpVersion bool) (map[string]string, map[string]types.AttributeValue, string) {
	names := map[string]string{
		"#mv":         "metadata_version",
		"#name":       "name",
		"#venue":      "venue_name",
		"#sb":         "songbook_id",
		"#status":     "status",
		"#recurring":  "recurring",
		"#tip":        "tip_url",
		"#ann":        "announcement",
		"#pin":        "kiosk_pin",
		"#rotation":   "rotation_mode",
		"#cur_round":  "current_round",
		"#auto":       "auto_accept",
		"#delay":      "submission_delay",
		"#max_active": "max_active_per_singer",
		"#allow_key":  "allow_key_change",
		"#write_ins":  "allow_write_ins",
		"#openkj":     "openkj_key",
		"#updated":    "updated_at",
		"#ttl":        "ttl",
	}

	currentRound := ev.CurrentRound
	if currentRound <= 0 {
		currentRound = 1
	}

	values := map[string]types.AttributeValue{
		":one":        avNumber(1),
		":name":       avString(ev.Name),
		":venue":      avString(ev.VenueName),
		":sb":         avString(ev.SongbookID),
		":status":     avString(string(ev.Status)),
		":recurring":  avBool(ev.Recurring),
		":tip":        avString(ev.TipURL),
		":ann":        avString(ev.Announcement),
		":pin":        avString(ev.KioskPIN),
		":rotation":   avString(ev.RotationMode),
		":cur_round":  avNumber(int64(currentRound)),
		":auto":       avBool(ev.AutoAccept),
		":delay":      avNumber(int64(ev.SubmissionDelay)),
		":max_active": avNumber(int64(ev.MaxActivePerSinger)),
		":allow_key":  avBool(ev.AllowKeyChange),
		":write_ins":  avBool(ev.AllowWriteIns),
		":openkj":     avString(ev.OpenKJKey),
		":updated":    avString(timestamp(time.Now().UTC())),
		":ttl":        avNumber(ev.TTL),
	}

	setParts := []string{
		"#name = :name",
		"#venue = :venue",
		"#sb = :sb",
		"#status = :status",
		"#recurring = :recurring",
		"#tip = :tip",
		"#ann = :ann",
		"#pin = :pin",
		"#rotation = :rotation",
		"#cur_round = :cur_round",
		"#auto = :auto",
		"#delay = :delay",
		"#max_active = :max_active",
		"#allow_key = :allow_key",
		"#write_ins = :write_ins",
		"#openkj = :openkj",
		"#updated = :updated",
		"#ttl = :ttl",
	}

	var removeParts []string
	if ev.EndedAt != nil {
		names["#ended"] = "ended_at"
		values[":ended"] = avString(timestamp(*ev.EndedAt))
		setParts = append(setParts, "#ended = :ended")
	} else {
		names["#ended"] = "ended_at"
		removeParts = append(removeParts, "#ended")
	}
	names["#detached"] = "detached_songbook_name"
	if strings.TrimSpace(ev.DetachedSongbookName) != "" {
		values[":detached"] = avString(strings.TrimSpace(ev.DetachedSongbookName))
		setParts = append(setParts, "#detached = :detached")
	} else {
		removeParts = append(removeParts, "#detached")
	}
	names["#singer_weights"] = "singer_weights"
	normalizedWeights := models.NormalizeSingerWeights(ev.SingerWeights)
	if len(normalizedWeights) > 0 {
		weightMap := make(map[string]types.AttributeValue, len(normalizedWeights))
		for k, v := range normalizedWeights {
			weightMap[k] = avNumber(int64(v))
		}
		values[":singer_weights"] = &types.AttributeValueMemberM{Value: weightMap}
		setParts = append(setParts, "#singer_weights = :singer_weights")
	} else {
		removeParts = append(removeParts, "#singer_weights")
	}

	updateExpr := "SET " + strings.Join(setParts, ", ")
	if len(removeParts) > 0 {
		updateExpr += " REMOVE " + strings.Join(removeParts, ", ")
	}

	updateExpr += " ADD #mv :one"
	if bumpVersion {
		names["#qv"] = "queue_version"
		updateExpr += ", #qv :one"
	}

	return names, values, updateExpr
}

// eventSnapshotCondition fences queue changes, private metadata changes, and restart.
// Old records with missing version attributes are equivalent to version zero.
func eventSnapshotCondition(ev *models.Event, names map[string]string, values map[string]types.AttributeValue) string {
	condition := "attribute_exists(id) AND attribute_not_exists(restart_in_progress) AND attribute_not_exists(detach_in_progress) AND (attribute_not_exists(deleting) OR deleting = :not_deleting)"
	values[":not_deleting"] = avBool(false)
	for _, version := range []struct {
		name, field, value string
		expected           int64
	}{
		{"#qv_cond", "queue_version", ":expected_qv", ev.QueueVersion},
		{"#mv_cond", "metadata_version", ":expected_mv", ev.MetadataVersion},
	} {
		names[version.name] = version.field
		values[version.value] = avNumber(version.expected)
		if version.expected == 0 {
			condition += fmt.Sprintf(" AND (%s = %s OR attribute_not_exists(%s))", version.name, version.value, version.name)
		} else {
			condition += fmt.Sprintf(" AND %s = %s", version.name, version.value)
		}
	}
	return condition
}

func (s *Store) eventSnapshotWriteError(ctx context.Context, ev *models.Event, err error) error {
	var conditional *types.ConditionalCheckFailedException
	var canceled *types.TransactionCanceledException
	// Only a failed event condition proves a stale snapshot. Transaction capacity,
	// transport, and request-row failures must retain their storage-error behavior.
	eventConditionFailed := errors.As(err, &conditional)
	if errors.As(err, &canceled) && len(canceled.CancellationReasons) > 0 {
		eventConditionFailed = aws.ToString(canceled.CancellationReasons[0].Code) == "ConditionalCheckFailed"
	}
	if eventConditionFailed {
		current, readErr := s.GetEvent(ctx, ev.ID)
		if readErr != nil {
			return fmt.Errorf("read event after rejected update: %w", readErr)
		}
		if current.Deleting {
			return store.ErrEventDeleting
		}
		if current.QueueVersion != ev.QueueVersion || current.MetadataVersion != ev.MetadataVersion || current.Restarting || current.Detaching {
			return &store.QueueVersionConflictError{Current: current.QueueVersion, Expected: ev.QueueVersion}
		}
	}
	return fmt.Errorf("commit event snapshot %s: %w", ev.ID, err)
}

// UpdateEventWithVersion compares both snapshot versions, advances metadata_version,
// and optionally increments queue_version atomically in the same operation.
func (s *Store) UpdateEventWithVersion(ctx context.Context, ev *models.Event, bumpVersion bool) error {
	if ev == nil || strings.TrimSpace(ev.ID) == "" {
		return fmt.Errorf("update event: invalid event")
	}

	names, values, updateExpr := s.buildEventUpdateExpr(ev, bumpVersion)
	condition := eventSnapshotCondition(ev, names, values)

	if _, hasFence := store.SongbookHostFenceFromContext(ctx); hasFence {
		txItems := s.appendHostFence(ctx, []types.TransactWriteItem{{
			Update: &types.Update{
				TableName:                 aws.String(s.tables.events),
				Key:                       stringKey("id", ev.ID),
				UpdateExpression:          aws.String(updateExpr),
				ConditionExpression:       aws.String(condition),
				ExpressionAttributeNames:  names,
				ExpressionAttributeValues: values,
			},
		}})
		if _, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: txItems}); err != nil {
			if isConditional(err) {
				if fenceErr := s.checkHostFenceError(ctx); fenceErr != nil {
					return fenceErr
				}
			}
			return s.eventSnapshotWriteError(ctx, ev, err)
		}
		ev.MetadataVersion++
		if bumpVersion {
			ev.QueueVersion++
		}
		ev.UpdatedAt = time.Now().UTC()
		return nil
	}

	result, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 aws.String(s.tables.events),
		Key:                       stringKey("id", ev.ID),
		UpdateExpression:          aws.String(updateExpr),
		ConditionExpression:       aws.String(condition),
		ExpressionAttributeNames:  names,
		ExpressionAttributeValues: values,
		ReturnValues:              types.ReturnValueUpdatedNew,
	})
	if err != nil {
		return s.eventSnapshotWriteError(ctx, ev, err)
	}

	var updatedFields struct {
		QueueVersion    int64  `dynamodbav:"queue_version"`
		MetadataVersion int64  `dynamodbav:"metadata_version"`
		UpdatedAt       string `dynamodbav:"updated_at"`
	}
	if err := unmarshal(result.Attributes, &updatedFields); err != nil {
		return fmt.Errorf("decode committed event versions: %w", err)
	}
	updatedAt, err := parseTime(updatedFields.UpdatedAt)
	if err != nil {
		return fmt.Errorf("decode committed event timestamp: %w", err)
	}
	ev.MetadataVersion = updatedFields.MetadataVersion
	// UPDATED_NEW omits queue_version when this write deliberately leaves it
	// unchanged. The checked snapshot already holds the authoritative value.
	if bumpVersion {
		ev.QueueVersion = updatedFields.QueueVersion
	}
	ev.UpdatedAt = updatedAt
	return nil
}

// TransitionQueueRotation atomically applies a queue rotation mode transition,
// persisting the updated requests and event metadata in a single transaction.
func (s *Store) TransitionQueueRotation(ctx context.Context, ev *models.Event, reqs []*models.SongRequest) error {
	if ev == nil || strings.TrimSpace(ev.ID) == "" {
		return fmt.Errorf("transition queue rotation: invalid event")
	}

	const maxReqsPerTx = 99
	if len(reqs) > maxReqsPerTx {
		return fmt.Errorf("queue exceeds maximum atomic transition size of %d requests: %w", maxReqsPerTx, os.ErrInvalid)
	}

	names, values, updateExpr := s.buildEventUpdateExpr(ev, true)
	now := timestamp(time.Now().UTC())

	conditionExpr := eventSnapshotCondition(ev, names, values)

	transactItems := make([]types.TransactWriteItem, 0, len(reqs)+1)
	transactItems = append(transactItems, types.TransactWriteItem{
		Update: &types.Update{
			TableName:                 aws.String(s.tables.events),
			Key:                       stringKey("id", ev.ID),
			UpdateExpression:          aws.String(updateExpr),
			ConditionExpression:       aws.String(conditionExpr),
			ExpressionAttributeNames:  names,
			ExpressionAttributeValues: values,
		},
	})
	for _, req := range reqs {
		transactItems = append(transactItems, types.TransactWriteItem{
			Update: &types.Update{
				TableName:           aws.String(s.tables.eventRequests),
				Key:                 eventRequestKey(ev.ID, req.ID),
				UpdateExpression:    aws.String("SET #r = :rank, #rd = :round, #u = :updated_at"),
				ConditionExpression: aws.String("attribute_exists(id) AND event_id = :event_id"),
				ExpressionAttributeNames: map[string]string{
					"#r":  "rank",
					"#rd": "round",
					"#u":  "updated_at",
				},
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":rank":       avNumber(req.Rank),
					":round":      avNumber(int64(req.Round)),
					":updated_at": avString(now),
					":event_id":   avString(ev.ID),
				},
			},
		})
	}
	transactItems = s.appendHostFence(ctx, transactItems)
	_, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: transactItems,
	})
	if err != nil {
		if isConditional(err) {
			if fenceErr := s.checkHostFenceError(ctx); fenceErr != nil {
				return fenceErr
			}
		}
		return s.eventSnapshotWriteError(ctx, ev, err)
	}
	ev.QueueVersion++
	ev.MetadataVersion++
	if t, parseErr := parseTime(now); parseErr == nil {
		ev.UpdatedAt = t
	}
	return nil
}

// BumpQueueVersion atomically marks an event's public queue snapshot stale.
func (s *Store) BumpQueueVersion(ctx context.Context, eventID string) (int64, error) {
	result, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 aws.String(s.tables.events),
		Key:                       stringKey("id", eventID),
		UpdateExpression:          aws.String("ADD queue_version :one SET updated_at = :updated_at"),
		ConditionExpression:       aws.String("attribute_exists(id) AND attribute_not_exists(restart_in_progress) AND (attribute_not_exists(deleting) OR deleting = :not_deleting)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":one": avNumber(1), ":updated_at": avString(timestamp(time.Now().UTC())), ":not_deleting": avBool(false)},
		ReturnValues:              types.ReturnValueUpdatedNew,
	})
	if isConditional(err) {
		if ev, getErr := s.GetEvent(ctx, eventID); getErr == nil && ev.Deleting {
			return 0, fmt.Errorf("bump queue version for event %s: %w", eventID, store.ErrEventDeleting)
		}
	}
	if err != nil {
		return 0, fmt.Errorf("bump queue version for event %s: %w", eventID, err)
	}

	var fields struct {
		QueueVersion int64 `dynamodbav:"queue_version"`
	}
	if err := unmarshal(result.Attributes, &fields); err != nil {
		return 0, err
	}
	return fields.QueueVersion, nil
}

// BeginEventDeletion marks an event as ended and deleting before child enumeration.
// Once marked, no child requests or metadata updates can commit against it.
func (s *Store) BeginEventDeletion(ctx context.Context, id string) error {
	now := time.Now().UTC()
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(s.tables.events),
		Key:                 stringKey("id", id),
		UpdateExpression:    aws.String("SET #status = :ended, deleting = :true, updated_at = :updated, ended_at = if_not_exists(ended_at, :updated)"),
		ConditionExpression: aws.String("attribute_exists(id)"),
		ExpressionAttributeNames: map[string]string{
			"#status": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":ended":   avString(string(models.EventStatusEnded)),
			":true":    avBool(true),
			":updated": avString(timestamp(now)),
		},
	})
	if isConditional(err) {
		return errNotExist()
	}
	if err != nil {
		return fmt.Errorf("begin event deletion: %w", err)
	}
	return nil
}

func (s *Store) DeleteEvent(ctx context.Context, id string) error {
	itemIDs, err := s.listEventItemIDs(ctx, id)
	if err != nil {
		return fmt.Errorf("list items for event %s: %w", id, err)
	}
	for _, itemID := range itemIDs {
		if err := s.DeleteRequest(ctx, itemID); err != nil {
			return fmt.Errorf("delete item %s for event %s: %w", itemID, id, err)
		}
	}
	_, err = s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(s.tables.events),
		Key:       stringKey("id", id),
	})
	if err != nil {
		return fmt.Errorf("delete event %s: %w", id, err)
	}
	return nil
}
