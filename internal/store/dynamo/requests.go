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

func singerSlotPartition(eventID string) string {
	return "slots#" + eventID
}

func (s *Store) CreateRequest(ctx context.Context, req *models.SongRequest) error {
	if req == nil || req.EventID == "" {
		return fmt.Errorf("event ID is required to create song request: %w", os.ErrInvalid)
	}
	if req.ID == "" {
		req.ID = uuid.NewString()
	}
	created, updated := nowForCreate(req.CreatedAt)
	req.CreatedAt = created
	req.UpdatedAt = updated
	if req.Status == "" {
		req.Status = models.StatusPending
	}
	if req.Rank == 0 {
		req.Rank = created.UnixMilli()
	}

	payload, err := marshal(buildRequestItem(req, req.EventID, timestamp(updated)))
	if err != nil {
		return err
	}

	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{Put: &types.Put{
				TableName:           aws.String(s.tables.eventRequests),
				Item:                payload,
				ConditionExpression: aws.String("attribute_not_exists(event_id)"),
			}},
			{ConditionCheck: &types.ConditionCheck{
				TableName: aws.String(s.tables.events), Key: stringKey("id", req.EventID),
				ConditionExpression:       aws.String("#status = :active AND attribute_not_exists(restart_in_progress) AND (attribute_not_exists(deleting) OR deleting = :not_deleting)"),
				ExpressionAttributeNames:  map[string]string{"#status": "status"},
				ExpressionAttributeValues: map[string]types.AttributeValue{":active": avString(string(models.EventStatusActive)), ":not_deleting": avBool(false)},
			}},
		},
	})
	if err != nil {
		if isConditional(err) {
			if ev, getErr := s.GetEvent(ctx, req.EventID); errors.Is(getErr, os.ErrNotExist) {
				return fmt.Errorf("create song request: %w", os.ErrNotExist)
			} else if getErr == nil {
				if ev.Deleting {
					return fmt.Errorf("create song request: %w", store.ErrEventDeleting)
				}
				if ev.Status != models.EventStatusActive {
					return fmt.Errorf("create song request: %w", store.ErrEventNotActive)
				}
			}
		}
		return fmt.Errorf("create song request: %w", err)
	}
	return nil
}

type singerSlotItem struct {
	ID         string `dynamodbav:"id"`
	RequestID  string `dynamodbav:"request_id"`
	EventID    string `dynamodbav:"event_id"`
	CreatedKey string `dynamodbav:"created_key"`
	TTL        int64  `dynamodbav:"ttl,omitempty"`
}

// CreateRequestAtVersion commits a complete admission plan against the event
// version read by the service. A failed condition requires a fresh plan.
func (s *Store) CreateRequestAtVersion(ctx context.Context, req *models.SongRequest, maxActivePerSinger int, reorderIDs []string, expectedVersion int64) error {
	if req.EventID == "" || expectedVersion < 0 {
		return fmt.Errorf("event ID and nonnegative expected queue version are required: %w", os.ErrInvalid)
	}

	normSinger := strings.ToLower(strings.TrimSpace(req.SingerName))
	if req.ID == "" {
		req.ID = uuid.NewString()
	}
	created, updated := nowForCreate(req.CreatedAt)
	req.CreatedAt = created
	req.UpdatedAt = updated
	if req.Status == "" {
		req.Status = models.StatusPending
	}
	if req.Rank == 0 {
		req.Rank = created.UnixMilli()
	}

	reqPayload, err := marshal(buildRequestItem(req, req.EventID, timestamp(updated)))
	if err != nil {
		return err
	}

	// Validate reorder list if provided
	if len(reorderIDs) > 95 {
		return fmt.Errorf("too many requests for atomic creation and reorder: %w", os.ErrInvalid)
	}
	seen := make(map[string]struct{}, len(reorderIDs))
	for _, id := range reorderIDs {
		if _, exists := seen[id]; exists {
			return fmt.Errorf("duplicate request %s in reorder list: %w", id, os.ErrInvalid)
		}
		seen[id] = struct{}{}
	}

	now := timestamp(updated)
	baseTransactItems := []types.TransactWriteItem{
		{
			Put: &types.Put{
				TableName:           aws.String(s.tables.eventRequests),
				Item:                reqPayload,
				ConditionExpression: aws.String("attribute_not_exists(event_id)"),
			},
		},
		{
			Update: &types.Update{
				TableName:           aws.String(s.tables.events),
				Key:                 stringKey("id", req.EventID),
				UpdateExpression:    aws.String("ADD queue_version :one SET updated_at = :updated_at"),
				ConditionExpression: aws.String("#status = :active AND attribute_not_exists(restart_in_progress) AND (attribute_not_exists(deleting) OR deleting = :not_deleting) AND queue_version = :expected"),
				ExpressionAttributeNames: map[string]string{
					"#status": "status",
				},
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":one":          avNumber(1),
					":updated_at":   avString(now),
					":active":       avString(string(models.EventStatusActive)),
					":not_deleting": avBool(false),
					":expected":     avNumber(expectedVersion),
				},
			},
		},
	}

	for i, id := range reorderIDs {
		if id == req.ID {
			continue
		}
		otherReq, err := s.GetRequest(ctx, id)
		if err != nil {
			return fmt.Errorf("load request %s for reorder: %w", id, err)
		}
		if otherReq.EventID != req.EventID {
			return fmt.Errorf("request %s does not belong to event %s: %w", id, req.EventID, os.ErrPermission)
		}
		rank := int64(i + 1)
		if otherReq.Round > 0 {
			rank = (int64(otherReq.Round) * models.RoundRankMultiplier) + int64(i+1)
		}
		baseTransactItems = append(baseTransactItems, types.TransactWriteItem{
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

	if maxActivePerSinger <= 0 || normSinger == "" {
		_, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
			TransactItems: baseTransactItems,
		})
		if err != nil {
			return s.admissionCommitError(ctx, req.EventID, expectedVersion, fmt.Errorf("commit request: %w", err))
		}
		return nil
	}

	slotTTL := req.TTL
	if slotTTL == 0 {
		slotTTL = time.Now().Add(14 * 24 * time.Hour).Unix()
	}

	for slotNum := 1; slotNum <= maxActivePerSinger; slotNum++ {
		slotID := fmt.Sprintf("slot#%s#%s#%d", req.EventID, normSinger, slotNum)
		createdKey := fmt.Sprintf("slot#%s#%02d", normSinger, slotNum)
		slotsPartition := fmt.Sprintf("slots#%s", req.EventID)

		slotRes, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
			TableName:      aws.String(s.tables.eventRequests),
			Key:            eventRequestKey(slotsPartition, slotID),
			ConsistentRead: aws.Bool(true),
		})
		if err != nil {
			return fmt.Errorf("read slot %s: %w", slotID, err)
		}

		slotExists := len(slotRes.Item) > 0
		var currentReqID string
		if slotExists {
			var slot singerSlotItem
			if err := unmarshal(slotRes.Item, &slot); err == nil {
				currentReqID = slot.RequestID
			}
		}

		var oldStatus string
		oldReqExists := false
		if slotExists && currentReqID != "" {
			oldRes, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
				TableName:      aws.String(s.tables.eventRequests),
				Key:            eventRequestKey(req.EventID, currentReqID),
				ConsistentRead: aws.Bool(true),
			})
			if err != nil {
				return fmt.Errorf("read old request %s for slot %s: %w", currentReqID, slotID, err)
			}
			if len(oldRes.Item) > 0 {
				oldReqExists = true
				var oldItem requestItem
				if err := unmarshal(oldRes.Item, &oldItem); err == nil {
					oldStatus = oldItem.Status
				}
			}
		}

		if oldReqExists {
			if oldStatus == string(models.StatusPending) ||
				oldStatus == string(models.StatusAccepted) ||
				oldStatus == string(models.StatusUpNext) {
				continue
			}
		}

		slotItem := singerSlotItem{
			ID:         slotID,
			RequestID:  req.ID,
			EventID:    slotsPartition,
			CreatedKey: createdKey,
			TTL:        slotTTL,
		}
		slotPayload, err := marshal(slotItem)
		if err != nil {
			return err
		}

		attemptItems := make([]types.TransactWriteItem, len(baseTransactItems), len(baseTransactItems)+2)
		copy(attemptItems, baseTransactItems)

		if !slotExists {
			attemptItems = append(attemptItems, types.TransactWriteItem{
				Put: &types.Put{
					TableName:           aws.String(s.tables.eventRequests),
					Item:                slotPayload,
					ConditionExpression: aws.String("attribute_not_exists(event_id)"),
				},
			})
		} else {
			attemptItems = append(attemptItems, types.TransactWriteItem{
				Put: &types.Put{
					TableName:           aws.String(s.tables.eventRequests),
					Item:                slotPayload,
					ConditionExpression: aws.String("request_id = :old_req_id"),
					ExpressionAttributeValues: map[string]types.AttributeValue{
						":old_req_id": avString(currentReqID),
					},
				},
			})
			if oldReqExists && oldStatus != "" {
				attemptItems = append(attemptItems, types.TransactWriteItem{
					ConditionCheck: &types.ConditionCheck{
						TableName:           aws.String(s.tables.eventRequests),
						Key:                 eventRequestKey(req.EventID, currentReqID),
						ConditionExpression: aws.String("#st = :old_st"),
						ExpressionAttributeNames: map[string]string{
							"#st": "status",
						},
						ExpressionAttributeValues: map[string]types.AttributeValue{
							":old_st": avString(oldStatus),
						},
					},
				})
			} else if !oldReqExists && currentReqID != "" {
				attemptItems = append(attemptItems, types.TransactWriteItem{
					ConditionCheck: &types.ConditionCheck{
						TableName:           aws.String(s.tables.eventRequests),
						Key:                 eventRequestKey(req.EventID, currentReqID),
						ConditionExpression: aws.String("attribute_not_exists(id)"),
					},
				})
			}
		}

		_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
			TransactItems: attemptItems,
		})
		if err == nil {
			return nil
		}

		if isConditional(err) {
			return s.admissionCommitError(ctx, req.EventID, expectedVersion, err)
		}
		return fmt.Errorf("commit request with limit: %w", err)
	}

	current, err := s.GetEvent(ctx, req.EventID)
	if err != nil {
		return fmt.Errorf("check event after singer slot contention: %w", err)
	}
	if current.Deleting {
		return store.ErrEventDeleting
	}
	if current.Status != models.EventStatusActive {
		return store.ErrEventNotActive
	}
	if current.QueueVersion != expectedVersion {
		return &store.QueueVersionConflictError{Current: current.QueueVersion}
	}
	return store.ErrSingerLimitExceeded
}

func (s *Store) admissionCommitError(ctx context.Context, eventID string, expectedVersion int64, commitErr error) error {
	if !isConditional(commitErr) || eventID == "" {
		return commitErr
	}
	event, err := s.GetEvent(ctx, eventID)
	if err != nil {
		return fmt.Errorf("check event after admission conflict: %w", err)
	}
	if event.Deleting {
		return store.ErrEventDeleting
	}
	if event.Status != models.EventStatusActive {
		return store.ErrEventNotActive
	}
	if event.QueueVersion != expectedVersion {
		return &store.QueueVersionConflictError{Current: event.QueueVersion}
	}
	return store.ErrAdmissionConflict
}

func (s *Store) GetRequest(ctx context.Context, id string) (*models.SongRequest, error) {
	if id == "" {
		return nil, os.ErrNotExist
	}
	out, err := s.db.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.tables.eventRequests),
		IndexName:              aws.String(requestIndex),
		KeyConditionExpression: aws.String("id = :id"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":id": avString(id),
		},
		Limit: aws.Int32(1),
	})
	if err != nil {
		return nil, fmt.Errorf("get song request %s: %w", id, err)
	}
	if len(out.Items) == 0 {
		return nil, os.ErrNotExist
	}
	var item requestItem
	if err := unmarshal(out.Items[0], &item); err != nil {
		return nil, err
	}
	return modelRequest(item)
}

func (s *Store) GetEventRequest(ctx context.Context, eventID, id string) (*models.SongRequest, error) {
	if eventID == "" || id == "" {
		return nil, os.ErrNotExist
	}
	result, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(s.tables.eventRequests),
		Key:            eventRequestKey(eventID, id),
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("get event request: %w", err)
	}
	if len(result.Item) == 0 {
		return nil, os.ErrNotExist
	}
	var item requestItem
	if err := unmarshal(result.Item, &item); err != nil {
		return nil, err
	}
	return modelRequest(item)
}

func eventRequestKey(eventID, id string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"event_id": avString(eventID), "id": avString(id)}
}

func (s *Store) ListRequestsByEvent(ctx context.Context, eventID string) ([]*models.SongRequest, error) {
	return s.listEventRequestsConsistent(ctx, eventID)
}

// listEventRequestsConsistent reads the event-keyed table for queue decisions.
func (s *Store) listEventRequestsConsistent(ctx context.Context, eventID string) ([]*models.SongRequest, error) {
	paginator := dynamodb.NewQueryPaginator(s.db, &dynamodb.QueryInput{
		TableName:              aws.String(s.tables.eventRequests),
		KeyConditionExpression: aws.String("event_id = :event_id"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":event_id": avString(eventID),
		},
		ConsistentRead: aws.Bool(true),
	})
	var result []*models.SongRequest
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list consistent requests for event %s: %w", eventID, err)
		}
		for _, raw := range page.Items {
			var item requestItem
			if err := unmarshal(raw, &item); err != nil {
				return nil, err
			}
			req, err := modelRequest(item)
			if err != nil {
				return nil, err
			}
			result = append(result, req)
		}
	}
	sortEventRequests(result)
	return result, nil
}

func sortEventRequests(result []*models.SongRequest) {
	// Sort by Round ascending (if both round-assigned), then Rank ascending (primary queue ordering)
	sort.SliceStable(result, func(i, j int) bool {
		rI, rJ := result[i].Round, result[j].Round
		if rI > 0 && rJ > 0 && rI != rJ {
			return rI < rJ
		}
		if result[i].Rank != result[j].Rank {
			return result[i].Rank < result[j].Rank
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
}

// listEventItemIDs discovers all request and singer-slot item IDs for an event with strong consistency.
func (s *Store) listEventItemIDs(ctx context.Context, eventID string) ([]string, error) {
	seen := make(map[string]bool)
	var ids []string

	// 1. Strongly consistent query on canonical event-requests table
	if s.tables.eventRequests != "" {
		reqPaginator := dynamodb.NewQueryPaginator(s.db, &dynamodb.QueryInput{
			TableName:              aws.String(s.tables.eventRequests),
			KeyConditionExpression: aws.String("event_id = :event_id"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":event_id": avString(eventID),
			},
			ProjectionExpression: aws.String("id"),
			ConsistentRead:       aws.Bool(true),
		})
		for reqPaginator.HasMorePages() {
			page, err := reqPaginator.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("list event requests for %s: %w", eventID, err)
			}
			for _, raw := range page.Items {
				if idAttr, ok := raw["id"].(*types.AttributeValueMemberS); ok && idAttr.Value != "" {
					if !seen[idAttr.Value] {
						seen[idAttr.Value] = true
						ids = append(ids, idAttr.Value)
					}
				}
			}
		}
	}

	// 2. Strongly consistent query on canonical event-requests table for singer slots
	slotsKey := "slots#" + eventID
	slotPaginator := dynamodb.NewQueryPaginator(s.db, &dynamodb.QueryInput{
		TableName:              aws.String(s.tables.eventRequests),
		KeyConditionExpression: aws.String("event_id = :event_id"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":event_id": avString(slotsKey),
		},
		ProjectionExpression: aws.String("id"),
		ConsistentRead:       aws.Bool(true),
	})
	for slotPaginator.HasMorePages() {
		page, err := slotPaginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("scan event items for %s: %w", eventID, err)
		}
		for _, raw := range page.Items {
			if idAttr, ok := raw["id"].(*types.AttributeValueMemberS); ok && idAttr.Value != "" {
				if !seen[idAttr.Value] {
					seen[idAttr.Value] = true
					ids = append(ids, idAttr.Value)
				}
			}
		}
	}

	return ids, nil
}

func (s *Store) UpdateRequest(ctx context.Context, req *models.SongRequest) error {
	if req == nil || req.EventID == "" {
		return fmt.Errorf("event ID is required for request update: %w", os.ErrInvalid)
	}
	return s.updateRequestAndReorder(ctx, req, nil, nil)
}

// UpdateRequestWithQueueVersion atomically writes a request and advances its
// event queue version. The event condition prevents stale host snapshots from
// overwriting newer queue state.
func (s *Store) updateRequestAndReorder(ctx context.Context, req *models.SongRequest, reorderIDs []string, expectedVersion *int64) error {
	if expectedVersion != nil && *expectedVersion < 0 {
		return fmt.Errorf("expected queue version must not be negative: %w", os.ErrInvalid)
	}
	if req.EventID == "" {
		return fmt.Errorf("event ID is required for request update: %w", os.ErrInvalid)
	}

	req.UpdatedAt = time.Now().UTC()
	now := timestamp(req.UpdatedAt)

	if len(reorderIDs) == 0 {
		payload, err := marshal(buildRequestItem(req, req.EventID, now))
		if err != nil {
			return err
		}

		eventUpdate := &types.Update{
			TableName:        aws.String(s.tables.events),
			Key:              stringKey("id", req.EventID),
			UpdateExpression: aws.String("ADD queue_version :one SET updated_at = :updated_at"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":one":          avNumber(1),
				":updated_at":   avString(now),
				":not_deleting": avBool(false),
			},
		}
		if expectedVersion != nil {
			eventUpdate.ConditionExpression = aws.String("attribute_exists(id) AND attribute_not_exists(restart_in_progress) AND (attribute_not_exists(deleting) OR deleting = :not_deleting) AND queue_version = :expected")
			eventUpdate.ExpressionAttributeValues[":expected"] = avNumber(*expectedVersion)
		} else {
			eventUpdate.ConditionExpression = aws.String("attribute_exists(id) AND attribute_not_exists(restart_in_progress) AND (attribute_not_exists(deleting) OR deleting = :not_deleting)")
		}

		_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
			TransactItems: []types.TransactWriteItem{
				{Put: &types.Put{
					TableName:           aws.String(s.tables.eventRequests),
					Item:                payload,
					ConditionExpression: aws.String("attribute_exists(id) AND event_id = :event_id"),
					ExpressionAttributeValues: map[string]types.AttributeValue{
						":event_id": avString(req.EventID),
					},
				}},
				{Update: eventUpdate},
			},
		})
		if err == nil {
			return nil
		}
		if isConditional(err) {
			if current, getErr := s.GetEvent(ctx, req.EventID); getErr == nil {
				if current.Deleting {
					return store.ErrEventDeleting
				}
				if expectedVersion != nil && current.QueueVersion != *expectedVersion {
					return &store.QueueVersionConflictError{Current: current.QueueVersion}
				}
			}
		}
		return fmt.Errorf("request update: %w", err)
	}

	seen := make(map[string]struct{}, len(reorderIDs))
	reqInReorder := false
	for _, id := range reorderIDs {
		if _, exists := seen[id]; exists {
			return fmt.Errorf("duplicate request %s in reorder: %w", id, os.ErrInvalid)
		}
		seen[id] = struct{}{}
		if id == req.ID {
			reqInReorder = true
		}
	}
	if !reqInReorder {
		return fmt.Errorf("request %s missing from reorder list: %w", req.ID, os.ErrInvalid)
	}
	if len(reorderIDs) > 99 {
		return fmt.Errorf("too many requests for atomic update and reorder: %w", os.ErrInvalid)
	}

	transactItems := make([]types.TransactWriteItem, 0, len(reorderIDs)+1)
	for i, id := range reorderIDs {
		rank := int64(i + 1)
		if id == req.ID {
			if req.Round > 0 {
				rank = (int64(req.Round) * models.RoundRankMultiplier) + int64(i+1)
			}
			req.Rank = rank
			payload, err := marshal(buildRequestItem(req, req.EventID, now))
			if err != nil {
				return err
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
				return fmt.Errorf("load request %s for conditional reorder: %w", id, err)
			}
			if otherReq.EventID != req.EventID {
				return fmt.Errorf("request %s does not belong to event %s: %w", id, req.EventID, os.ErrPermission)
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

	eventUpdate := &types.Update{
		TableName:        aws.String(s.tables.events),
		Key:              stringKey("id", req.EventID),
		UpdateExpression: aws.String("ADD queue_version :one SET updated_at = :updated_at"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":one":          avNumber(1),
			":updated_at":   avString(now),
			":not_deleting": avBool(false),
		},
	}
	if expectedVersion != nil {
		eventUpdate.ConditionExpression = aws.String("attribute_exists(id) AND attribute_not_exists(restart_in_progress) AND (attribute_not_exists(deleting) OR deleting = :not_deleting) AND queue_version = :expected")
		eventUpdate.ExpressionAttributeValues[":expected"] = avNumber(*expectedVersion)
	} else {
		eventUpdate.ConditionExpression = aws.String("attribute_exists(id) AND attribute_not_exists(restart_in_progress) AND (attribute_not_exists(deleting) OR deleting = :not_deleting)")
	}
	transactItems = append(transactItems, types.TransactWriteItem{Update: eventUpdate})

	_, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: transactItems,
	})
	if err == nil {
		return nil
	}
	if isConditional(err) {
		if current, getErr := s.GetEvent(ctx, req.EventID); getErr == nil {
			if current.Deleting {
				return store.ErrEventDeleting
			}
			if expectedVersion != nil && current.QueueVersion != *expectedVersion {
				return &store.QueueVersionConflictError{Current: current.QueueVersion}
			}
		}
	}
	return fmt.Errorf("request update and reorder: %w", err)
}

// UpdateRequestWithQueueVersion atomically writes a request and advances its
// event queue version. The event condition prevents stale host snapshots from
// overwriting newer queue state.
func (s *Store) UpdateRequestWithQueueVersion(ctx context.Context, req *models.SongRequest, expectedVersion int64) error {
	return s.updateRequestAndReorder(ctx, req, nil, &expectedVersion)
}

// UpdateRequestAndReorderWithQueueVersion atomically writes a request, reorders the queue,
// and advances its event queue version.
func (s *Store) UpdateRequestAndReorderWithQueueVersion(ctx context.Context, req *models.SongRequest, reorderIDs []string, expectedVersion int64) error {
	return s.updateRequestAndReorder(ctx, req, reorderIDs, &expectedVersion)
}

// UpdateRequestAndReorder atomically updates a request and reorders the queue in a single transaction.
func (s *Store) UpdateRequestAndReorder(ctx context.Context, req *models.SongRequest, reorderIDs []string) error {
	return s.updateRequestAndReorder(ctx, req, reorderIDs, nil)
}

// SetRequestsTTLByEvent sets or clears the DynamoDB TTL for an event's queue history and singer-slot records.
func (s *Store) SetRequestsTTLByEvent(ctx context.Context, eventID string, expiresAt time.Time) error {
	ev, err := s.GetEvent(ctx, eventID)
	if err == nil {
		if ev.Restarting {
			return fmt.Errorf("set request retention for %s: restart in progress: %w", eventID, store.ErrAdmissionConflict)
		}
		if !expiresAt.IsZero() && ev.Status == models.EventStatusActive {
			return nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("get event for retention: %w", err)
	}

	itemIDs, err := s.listEventItemIDs(ctx, eventID)
	if err != nil {
		return err
	}
	for _, id := range itemIDs {
		partitionKey := eventID
		if strings.HasPrefix(id, "slot#") {
			partitionKey = "slots#" + eventID
		}
		items := []types.TransactWriteItem{
			{Update: requestTTLUpdate(s.tables.eventRequests, eventRequestKey(partitionKey, id), partitionKey, expiresAt)},
		}
		if !expiresAt.IsZero() {
			items = append(items, types.TransactWriteItem{ConditionCheck: &types.ConditionCheck{
				TableName:                 aws.String(s.tables.events),
				Key:                       stringKey("id", eventID),
				ConditionExpression:       aws.String("attribute_not_exists(id) OR (#status <> :active AND attribute_not_exists(restart_in_progress))"),
				ExpressionAttributeNames:  map[string]string{"#status": "status"},
				ExpressionAttributeValues: map[string]types.AttributeValue{":active": avString(string(models.EventStatusActive))},
			}})
		} else {
			items = append(items, types.TransactWriteItem{ConditionCheck: &types.ConditionCheck{
				TableName:           aws.String(s.tables.events),
				Key:                 stringKey("id", eventID),
				ConditionExpression: aws.String("attribute_not_exists(id) OR attribute_not_exists(restart_in_progress)"),
			}})
		}
		if _, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: items}); err != nil {
			if isConditional(err) {
				if ev, getErr := s.GetEvent(ctx, eventID); getErr == nil && ev != nil {
					if ev.Restarting {
						return fmt.Errorf("set request retention for %s: restart in progress: %w", id, err)
					}
					if !expiresAt.IsZero() && ev.Status == models.EventStatusActive {
						return nil
					}
				}
				if s.isItemCompletelyDeleted(ctx, eventID, id) {
					continue
				}
			}
			return fmt.Errorf("set request retention for %s: %w", id, err)
		}
	}
	return nil
}

func (s *Store) isItemCompletelyDeleted(ctx context.Context, eventID, id string) bool {
	partitionKey := eventID
	if strings.HasPrefix(id, "slot#") {
		partitionKey = "slots#" + eventID
	}
	canonical, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(s.tables.eventRequests),
		Key:            eventRequestKey(partitionKey, id),
		ConsistentRead: aws.Bool(true),
	})
	if err != nil || len(canonical.Item) > 0 {
		return false
	}
	return true
}

func requestTTLUpdate(table string, key map[string]types.AttributeValue, eventID string, expiresAt time.Time) *types.Update {
	update := &types.Update{
		TableName:           aws.String(table),
		Key:                 key,
		ConditionExpression: aws.String("attribute_exists(id) AND event_id = :event_id"),
		ExpressionAttributeNames: map[string]string{
			"#ttl": "ttl",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{":event_id": avString(eventID)},
	}
	if expiresAt.IsZero() {
		update.UpdateExpression = aws.String("REMOVE #ttl")
	} else {
		update.UpdateExpression = aws.String("SET #ttl = :ttl")
		update.ExpressionAttributeValues[":ttl"] = avNumber(expiresAt.Unix())
	}
	return update
}

// DeleteEventRequestAtomic enforces event scope and commits both request
// deletes and the event queue-version advance in one DynamoDB transaction.
func (s *Store) DeleteEventRequestAtomic(ctx context.Context, eventID, id string) error {
	if eventID == "" {
		return fmt.Errorf("event ID is required for atomic request deletion: %w", os.ErrInvalid)
	}
	return s.deleteRequest(ctx, id, eventID, true)
}

// DeleteRequest uses the same transaction for event requests during cleanup.
func (s *Store) DeleteRequest(ctx context.Context, id string) error {
	return s.deleteRequest(ctx, id, "", false)
}

func (s *Store) deleteRequest(ctx context.Context, id, expectedEventID string, enforceEventScope bool) error {
	if strings.HasPrefix(id, "slot#") {
		slotsKey := expectedEventID
		if slotsKey == "" {
			parts := strings.Split(id, "#")
			if len(parts) >= 2 {
				slotsKey = parts[1]
			}
		}
		if !strings.HasPrefix(slotsKey, "slots#") {
			slotsKey = "slots#" + slotsKey
		}
		_, err := s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
			TableName: aws.String(s.tables.eventRequests),
			Key:       eventRequestKey(slotsKey, id),
		})
		if err != nil {
			return fmt.Errorf("delete singer slot: %w", err)
		}
		return nil
	}

	req, err := s.GetRequest(ctx, id)
	if errors.Is(err, os.ErrNotExist) {
		if enforceEventScope {
			return os.ErrNotExist
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("load song request for delete: %w", err)
	}
	if enforceEventScope && req.EventID != expectedEventID {
		return os.ErrNotExist
	}

	if req.EventID != "" {
		_, eventErr := s.GetEvent(ctx, req.EventID)
		if eventErr != nil && !errors.Is(eventErr, os.ErrNotExist) {
			return fmt.Errorf("load event for request delete: %w", eventErr)
		}
		items := []types.TransactWriteItem{
			{Delete: &types.Delete{
				TableName:           aws.String(s.tables.eventRequests),
				Key:                 eventRequestKey(req.EventID, id),
				ConditionExpression: aws.String("attribute_exists(id)"),
			}},
		}
		if eventErr == nil {
			eventCond := "attribute_exists(id)"
			exprVals := map[string]types.AttributeValue{
				":one":        avNumber(1),
				":updated_at": avString(timestamp(time.Now().UTC())),
			}
			if enforceEventScope {
				eventCond = "attribute_exists(id) AND attribute_not_exists(restart_in_progress) AND (attribute_not_exists(deleting) OR deleting = :not_deleting)"
				exprVals[":not_deleting"] = avBool(false)
			}
			items = append(items, types.TransactWriteItem{Update: &types.Update{
				TableName:                 aws.String(s.tables.events),
				Key:                       stringKey("id", req.EventID),
				UpdateExpression:          aws.String("ADD queue_version :one SET updated_at = :updated_at"),
				ConditionExpression:       aws.String(eventCond),
				ExpressionAttributeValues: exprVals,
			}})
		} else {
			items = append(items, types.TransactWriteItem{ConditionCheck: &types.ConditionCheck{
				TableName:           aws.String(s.tables.events),
				Key:                 stringKey("id", req.EventID),
				ConditionExpression: aws.String("attribute_not_exists(id)"),
			}})
		}
		_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: items})
		if isConditional(err) {
			if enforceEventScope {
				if ev, getErr := s.GetEvent(ctx, req.EventID); getErr == nil && ev.Deleting {
					return store.ErrEventDeleting
				}
			}
			if _, readErr := s.GetRequest(ctx, id); errors.Is(readErr, os.ErrNotExist) {
				if enforceEventScope {
					return os.ErrNotExist
				}
				return nil
			} else if readErr != nil {
				return fmt.Errorf("check request after delete conflict: %w", readErr)
			}
		}
	} else {
		return fmt.Errorf("event ID is required to delete song request: %w", os.ErrInvalid)
	}
	if err != nil {
		return fmt.Errorf("delete song request: %w", err)
	}
	return nil
}

func (s *Store) ReorderRequests(ctx context.Context, eventID string, orderedIDs []string) error {
	if len(orderedIDs) == 0 {
		return nil
	}
	if eventID == "" {
		return fmt.Errorf("event ID is required to reorder requests: %w", os.ErrInvalid)
	}
	if len(orderedIDs) > 99 {
		return fmt.Errorf("event reorder exceeds atomic transaction limit: %w", os.ErrInvalid)
	}

	seen := make(map[string]struct{}, len(orderedIDs))
	now := timestamp(time.Now().UTC())
	transactItems := make([]types.TransactWriteItem, 0, len(orderedIDs)+1)
	for i, id := range orderedIDs {
		if _, dup := seen[id]; dup {
			return fmt.Errorf("duplicate request %s in reorder: %w", id, os.ErrInvalid)
		}
		seen[id] = struct{}{}
		req, err := s.GetRequest(ctx, id)
		if err != nil {
			return fmt.Errorf("load request %s for reordering: %w", id, err)
		}
		if req.EventID != eventID {
			return fmt.Errorf("request %s does not belong to event %s: %w", id, eventID, os.ErrPermission)
		}
		rank := int64(i + 1)
		if req.Round > 0 {
			rank = (int64(req.Round) * models.RoundRankMultiplier) + int64(i+1)
		}
		transactItems = append(transactItems, types.TransactWriteItem{
			Update: &types.Update{
				TableName:           aws.String(s.tables.eventRequests),
				Key:                 eventRequestKey(eventID, id),
				UpdateExpression:    aws.String("SET #r = :rank, #u = :updated_at"),
				ConditionExpression: aws.String("event_id = :event_id"),
				ExpressionAttributeNames: map[string]string{
					"#r": "rank",
					"#u": "updated_at",
				},
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":rank":       avNumber(rank),
					":updated_at": avString(now),
					":event_id":   avString(eventID),
				},
			},
		})
	}
	transactItems = append(transactItems, types.TransactWriteItem{
		Update: &types.Update{
			TableName:           aws.String(s.tables.events),
			Key:                 stringKey("id", eventID),
			UpdateExpression:    aws.String("ADD queue_version :one SET updated_at = :updated_at"),
			ConditionExpression: aws.String("attribute_exists(id) AND attribute_not_exists(restart_in_progress) AND (attribute_not_exists(deleting) OR deleting = :not_deleting)"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":one":          avNumber(1),
				":updated_at":   avString(now),
				":not_deleting": avBool(false),
			},
		},
	})

	_, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: transactItems,
	})
	if err != nil {
		if isConditional(err) {
			if ev, getErr := s.GetEvent(ctx, eventID); getErr == nil && ev.Deleting {
				return store.ErrEventDeleting
			}
		}
		return fmt.Errorf("reorder requests: %w", err)
	}
	return nil
}

// ReorderRequestsWithQueueVersion atomically reorders event requests and
// advances the event queue version exactly once.
func (s *Store) ReorderRequestsWithQueueVersion(ctx context.Context, eventID string, orderedIDs []string, expectedVersion int64) error {
	if expectedVersion < 0 || eventID == "" {
		return fmt.Errorf("invalid conditional reorder arguments: %w", os.ErrInvalid)
	}
	if len(orderedIDs) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(orderedIDs))
	for _, id := range orderedIDs {
		if _, exists := seen[id]; exists {
			return fmt.Errorf("duplicate request %s in conditional reorder: %w", id, os.ErrInvalid)
		}
		seen[id] = struct{}{}
	}
	// DynamoDB permits at most 100 actions per transaction; reserve one for
	// the event version update.
	if len(orderedIDs) > 99 {
		return fmt.Errorf("too many requests for atomic reorder: %w", os.ErrInvalid)
	}

	now := timestamp(time.Now().UTC())
	transactItems := make([]types.TransactWriteItem, 0, len(orderedIDs)+1)
	for i, id := range orderedIDs {
		req, err := s.GetRequest(ctx, id)
		if err != nil {
			return fmt.Errorf("load request %s for conditional reorder: %w", id, err)
		}
		if req.EventID != eventID {
			return fmt.Errorf("request %s does not belong to event %s: %w", id, eventID, os.ErrPermission)
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
	transactItems = append(transactItems, types.TransactWriteItem{Update: &types.Update{
		TableName:           aws.String(s.tables.events),
		Key:                 stringKey("id", eventID),
		UpdateExpression:    aws.String("ADD queue_version :one SET updated_at = :updated_at"),
		ConditionExpression: aws.String("attribute_exists(id) AND attribute_not_exists(restart_in_progress) AND queue_version = :expected"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":one": avNumber(1), ":updated_at": avString(now), ":expected": avNumber(expectedVersion),
		},
	}})

	_, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: transactItems})
	if err == nil {
		return nil
	}
	if current, getErr := s.GetEvent(ctx, eventID); getErr == nil && current.QueueVersion != expectedVersion {
		return &store.QueueVersionConflictError{Current: current.QueueVersion}
	}
	return fmt.Errorf("conditional reorder requests: %w", err)
}
