package dynamo

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

// Idempotency records are deliberately short-lived: they cover offline replay
// after a host reconnects without retaining request bodies indefinitely.
const idempotencyRetention = 24 * time.Hour

type idempotencyItem struct {
	IdempotencyKey string `dynamodbav:"idempotency_key"`
	OwnerID        string `dynamodbav:"owner_id"`
	Route          string `dynamodbav:"route"`
	EntityID       string `dynamodbav:"entity_id"`
	RequestHash    string `dynamodbav:"request_hash"`
	StatusCode     int    `dynamodbav:"status_code"`
	ResponseBody   string `dynamodbav:"response_body"`
	QueueVersion   int64  `dynamodbav:"queue_version"`
	TTL            int64  `dynamodbav:"ttl"`
}

func (s *Store) loadIdempotency(ctx context.Context, key string) (*idempotencyItem, error) {
	result, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(s.tables.idempotency),
		Key:            stringKey("idempotency_key", key),
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("load idempotency record: %w", err)
	}
	if len(result.Item) == 0 {
		return nil, nil
	}
	var item idempotencyItem
	if err := unmarshal(result.Item, &item); err != nil {
		return nil, err
	}
	if item.TTL > 0 && item.TTL <= time.Now().UTC().Unix() {
		// DynamoDB TTL deletion is asynchronous. Remove an expired record before
		// accepting a new mutation, guarded by the observed TTL value.
		_, deleteErr := s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
			TableName:           aws.String(s.tables.idempotency),
			Key:                 stringKey("idempotency_key", key),
			ConditionExpression: aws.String("#ttl = :ttl"),
			ExpressionAttributeNames: map[string]string{
				"#ttl": "ttl",
			},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":ttl": avNumber(item.TTL),
			},
		})
		if deleteErr != nil && !isConditional(deleteErr) {
			return nil, fmt.Errorf("remove expired idempotency record: %w", deleteErr)
		}
		if isConditional(deleteErr) {
			return s.loadIdempotency(ctx, key)
		}
		return nil, nil
	}
	return &item, nil
}

func (s *Store) replayIdempotency(ctx context.Context, key, ownerID, route, entityID, requestHash string) (*store.QueueMutationResult, bool, error) {
	item, err := s.loadIdempotency(ctx, key)
	if err != nil || item == nil {
		return nil, false, err
	}
	if item.OwnerID != ownerID || item.Route != route || item.EntityID != entityID || item.RequestHash != requestHash {
		return nil, true, &store.IdempotencyConflictError{}
	}
	return &store.QueueMutationResult{
		StatusCode:   item.StatusCode,
		Body:         []byte(item.ResponseBody),
		QueueVersion: item.QueueVersion,
		Replay:       true,
	}, true, nil
}

func (s *Store) GetIdempotencyResult(ctx context.Context, key, ownerID, route, entityID, requestHash string) (*store.QueueMutationResult, bool, error) {
	if err := validateIdempotencyArgs(key, ownerID, route, entityID, requestHash); err != nil {
		return nil, false, err
	}
	return s.replayIdempotency(ctx, key, ownerID, route, entityID, requestHash)
}

func marshalIdempotency(item idempotencyItem) (map[string]types.AttributeValue, error) {
	payload, err := marshal(item)
	if err != nil {
		return nil, err
	}
	return payload, nil
}

func idempotencyPut(tableName string, item map[string]types.AttributeValue) types.TransactWriteItem {
	return types.TransactWriteItem{Put: &types.Put{
		TableName:           aws.String(tableName),
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(idempotency_key)"),
	}}
}

func idempotencyRecord(key, ownerID, route, entityID, requestHash string, result *store.QueueMutationResult) (map[string]types.AttributeValue, error) {
	item := idempotencyItem{
		IdempotencyKey: key,
		OwnerID:        ownerID,
		Route:          route,
		EntityID:       entityID,
		RequestHash:    requestHash,
		StatusCode:     result.StatusCode,
		ResponseBody:   string(result.Body),
		QueueVersion:   result.QueueVersion,
		TTL:            time.Now().UTC().Add(idempotencyRetention).Unix(),
	}
	return marshalIdempotency(item)
}

func validateIdempotencyArgs(key, ownerID, route, entityID, requestHash string) error {
	if key == "" || ownerID == "" || route == "" || entityID == "" || requestHash == "" {
		return fmt.Errorf("idempotency arguments are incomplete: %w", os.ErrInvalid)
	}
	return nil
}

func queueVersionAfter(expected *int64) (int64, error) {
	if expected == nil {
		return 0, nil
	}
	if *expected < 0 {
		return 0, fmt.Errorf("expected queue version must not be negative: %w", os.ErrInvalid)
	}
	return *expected, nil
}

func (s *Store) eventVersion(ctx context.Context, eventID string, expected *int64) (int64, error) {
	if expected != nil {
		return *expected, nil
	}
	event, err := s.GetEvent(ctx, eventID)
	if err != nil {
		return 0, err
	}
	return event.QueueVersion, nil
}

func idempotentEventVersionUpdate(tableName, eventID, now string, version int64) types.TransactWriteItem {
	return types.TransactWriteItem{Update: &types.Update{
		TableName:           aws.String(tableName),
		Key:                 stringKey("id", eventID),
		UpdateExpression:    aws.String("ADD queue_version :one SET updated_at = :updated_at"),
		ConditionExpression: aws.String("attribute_exists(id) AND attribute_not_exists(restart_in_progress) AND (attribute_not_exists(deleting) OR deleting = :not_deleting) AND queue_version = :expected"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":one":          avNumber(1),
			":updated_at":   avString(now),
			":not_deleting": avBool(false),
			":expected":     avNumber(version),
		},
	}}
}

func (s *Store) classifyIdempotentEventError(ctx context.Context, eventID string, version int64, err error) error {
	if isConditional(err) {
		if current, getErr := s.GetEvent(ctx, eventID); getErr == nil {
			if current.Deleting {
				return store.ErrEventDeleting
			}
			if current.QueueVersion != version {
				return &store.QueueVersionConflictError{Current: current.QueueVersion}
			}
		}
	}
	return nil
}

func (s *Store) UpdateRequestWithIdempotency(ctx context.Context, req *models.SongRequest, expected *int64, key, ownerID, route, requestHash string) (*store.QueueMutationResult, error) {
	return s.UpdateRequestAndReorderWithIdempotency(ctx, req, nil, expected, key, ownerID, route, requestHash)
}

func (s *Store) UpdateRequestAndReorderWithIdempotency(ctx context.Context, req *models.SongRequest, reorderIDs []string, expected *int64, key, ownerID, route, requestHash string) (*store.QueueMutationResult, error) {
	if req == nil || req.EventID == "" {
		return nil, fmt.Errorf("event ID is required for idempotent request update: %w", os.ErrInvalid)
	}
	if err := validateIdempotencyArgs(key, ownerID, route, req.ID, requestHash); err != nil {
		return nil, err
	}
	if _, err := queueVersionAfter(expected); err != nil {
		return nil, err
	}
	if replay, found, err := s.replayIdempotency(ctx, key, ownerID, route, req.ID, requestHash); found || err != nil {
		return replay, err
	}
	version, err := s.eventVersion(ctx, req.EventID, expected)
	if err != nil {
		return nil, err
	}

	req.UpdatedAt = time.Now().UTC()
	now := timestamp(req.UpdatedAt)

	if len(reorderIDs) == 0 {
		payload, err := marshal(buildRequestItem(req, req.EventID, now))
		if err != nil {
			return nil, err
		}
		body, err := json.Marshal(req)
		if err != nil {
			return nil, err
		}
		result := &store.QueueMutationResult{StatusCode: 200, Body: body, QueueVersion: version + 1}
		record, err := idempotencyRecord(key, ownerID, route, req.ID, requestHash, result)
		if err != nil {
			return nil, err
		}
		_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
			{Put: &types.Put{
				TableName:           aws.String(s.tables.eventRequests),
				Item:                payload,
				ConditionExpression: aws.String("attribute_exists(id) AND event_id = :event_id"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":event_id": avString(req.EventID),
				},
			}},
			idempotentEventVersionUpdate(s.tables.events, req.EventID, now, version),
			idempotencyPut(s.tables.idempotency, record),
		}})
		if err == nil {
			return result, nil
		}
		if replay, found, replayErr := s.replayIdempotency(ctx, key, ownerID, route, req.ID, requestHash); found || replayErr != nil {
			return replay, replayErr
		}
		if classified := s.classifyIdempotentEventError(ctx, req.EventID, version, err); classified != nil {
			return nil, classified
		}
		return nil, fmt.Errorf("idempotent request update: %w", err)
	}

	seen := make(map[string]struct{}, len(reorderIDs))
	reqInReorder := false
	for _, id := range reorderIDs {
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("duplicate request %s in reorder: %w", id, os.ErrInvalid)
		}
		seen[id] = struct{}{}
		if id == req.ID {
			reqInReorder = true
		}
	}
	if !reqInReorder {
		return nil, fmt.Errorf("request %s missing from reorder list: %w", req.ID, os.ErrInvalid)
	}
	if len(reorderIDs) > 98 {
		return nil, fmt.Errorf("too many requests for atomic idempotent update and reorder: %w", os.ErrInvalid)
	}

	transactItems := make([]types.TransactWriteItem, 0, len(reorderIDs)+2)
	for i, id := range reorderIDs {
		rank := int64(i + 1)
		if id == req.ID {
			if req.Round > 0 {
				rank = (int64(req.Round) * models.RoundRankMultiplier) + int64(i+1)
			}
			req.Rank = rank
			payload, err := marshal(buildRequestItem(req, req.EventID, now))
			if err != nil {
				return nil, err
			}
			transactItems = append(transactItems, types.TransactWriteItem{
				Put: &types.Put{
					TableName:           aws.String(s.tables.eventRequests),
					Item:                payload,
					ConditionExpression: aws.String("attribute_exists(id) AND event_id = :event_id"),
					ExpressionAttributeValues: map[string]types.AttributeValue{
						":event_id": avString(req.EventID),
					},
				},
			})
		} else {
			otherReq, err := s.GetRequest(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("load request %s for conditional reorder: %w", id, err)
			}
			if otherReq.EventID != req.EventID {
				return nil, fmt.Errorf("request %s does not belong to event %s: %w", id, req.EventID, os.ErrPermission)
			}
			if otherReq.Round > 0 {
				rank = (int64(otherReq.Round) * models.RoundRankMultiplier) + int64(i+1)
			}
			transactItems = append(transactItems, types.TransactWriteItem{
				Update: &types.Update{
					TableName:           aws.String(s.tables.eventRequests),
					Key:                 eventRequestKey(req.EventID, id),
					UpdateExpression:    aws.String("SET #r = :rank, #u = :updated_at"),
					ConditionExpression: aws.String("event_id = :event_id"),
					ExpressionAttributeNames: map[string]string{
						"#r": "rank", "#u": "updated_at",
					},
					ExpressionAttributeValues: map[string]types.AttributeValue{
						":rank":       avNumber(rank),
						":updated_at": avString(now),
						":event_id":   avString(req.EventID),
					},
				},
			})
		}
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	result := &store.QueueMutationResult{StatusCode: 200, Body: body, QueueVersion: version + 1}
	record, err := idempotencyRecord(key, ownerID, route, req.ID, requestHash, result)
	if err != nil {
		return nil, err
	}

	transactItems = append(transactItems,
		idempotentEventVersionUpdate(s.tables.events, req.EventID, now, version),
		idempotencyPut(s.tables.idempotency, record),
	)

	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: transactItems})
	if err == nil {
		return result, nil
	}
	if replay, found, replayErr := s.replayIdempotency(ctx, key, ownerID, route, req.ID, requestHash); found || replayErr != nil {
		return replay, replayErr
	}
	if classified := s.classifyIdempotentEventError(ctx, req.EventID, version, err); classified != nil {
		return nil, classified
	}
	return nil, fmt.Errorf("idempotent request update and reorder: %w", err)
}

func (s *Store) ReorderRequestsWithIdempotency(ctx context.Context, eventID string, orderedIDs []string, expected *int64, key, ownerID, route, requestHash string) (*store.QueueMutationResult, error) {
	if err := validateIdempotencyArgs(key, ownerID, route, eventID, requestHash); err != nil {
		return nil, err
	}
	if _, err := queueVersionAfter(expected); err != nil {
		return nil, err
	}
	if replay, found, err := s.replayIdempotency(ctx, key, ownerID, route, eventID, requestHash); found || err != nil {
		return replay, err
	}
	if len(orderedIDs) > 98 {
		return nil, fmt.Errorf("idempotent reorder supports at most 98 requests: %w", os.ErrInvalid)
	}
	seen := make(map[string]struct{}, len(orderedIDs))
	for _, id := range orderedIDs {
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("duplicate request %s in idempotent reorder: %w", id, os.ErrInvalid)
		}
		seen[id] = struct{}{}
	}
	version, err := s.eventVersion(ctx, eventID, expected)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]string{"status": "ok"})
	if err != nil {
		return nil, err
	}
	result := &store.QueueMutationResult{StatusCode: 200, Body: body, QueueVersion: version}
	if len(orderedIDs) > 0 {
		result.QueueVersion++
	}
	record, err := idempotencyRecord(key, ownerID, route, eventID, requestHash, result)
	if err != nil {
		return nil, err
	}
	if len(orderedIDs) == 0 {
		_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
			{ConditionCheck: &types.ConditionCheck{
				TableName:           aws.String(s.tables.events),
				Key:                 stringKey("id", eventID),
				ConditionExpression: aws.String("attribute_exists(id) AND attribute_not_exists(restart_in_progress) AND (attribute_not_exists(deleting) OR deleting = :not_deleting) AND queue_version = :expected"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":not_deleting": avBool(false),
					":expected":     avNumber(version),
				},
			}},
			idempotencyPut(s.tables.idempotency, record),
		}})
		if err == nil {
			return result, nil
		}
		if replay, found, replayErr := s.replayIdempotency(ctx, key, ownerID, route, eventID, requestHash); found || replayErr != nil {
			return replay, replayErr
		}
		if classified := s.classifyIdempotentEventError(ctx, eventID, version, err); classified != nil {
			return nil, classified
		}
		return nil, fmt.Errorf("idempotent empty reorder: %w", err)
	}

	now := timestamp(time.Now().UTC())
	transactItems := make([]types.TransactWriteItem, 0, len(orderedIDs)+2)
	for i, id := range orderedIDs {
		req, getErr := s.GetRequest(ctx, id)
		if getErr != nil {
			return nil, fmt.Errorf("load request %s for idempotent reorder: %w", id, getErr)
		}
		if req.EventID != eventID {
			return nil, fmt.Errorf("request %s does not belong to event %s: %w", id, eventID, os.ErrPermission)
		}
		rank := int64(i + 1)
		if req.Round > 0 {
			rank = (int64(req.Round) * models.RoundRankMultiplier) + int64(i+1)
		}
		transactItems = append(transactItems, types.TransactWriteItem{Update: &types.Update{
			TableName:           aws.String(s.tables.eventRequests),
			Key:                 eventRequestKey(eventID, id),
			UpdateExpression:    aws.String("SET #r = :rank, #u = :updated_at"),
			ConditionExpression: aws.String("event_id = :event_id"),
			ExpressionAttributeNames: map[string]string{
				"#r": "rank", "#u": "updated_at",
			},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":rank": avNumber(rank), ":updated_at": avString(now), ":event_id": avString(eventID),
			},
		}})
	}
	transactItems = append(transactItems,
		idempotentEventVersionUpdate(s.tables.events, eventID, now, version),
		idempotencyPut(s.tables.idempotency, record),
	)
	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: transactItems})
	if err == nil {
		return result, nil
	}
	if replay, found, replayErr := s.replayIdempotency(ctx, key, ownerID, route, eventID, requestHash); found || replayErr != nil {
		return replay, replayErr
	}
	if classified := s.classifyIdempotentEventError(ctx, eventID, version, err); classified != nil {
		return nil, classified
	}
	return nil, fmt.Errorf("idempotent reorder: %w", err)
}
