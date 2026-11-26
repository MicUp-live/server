package dynamo

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"

	"micup.live/server/internal/models"
)

func TestEventRequestReadsUseCanonicalRow(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	event := helperCreateEvent(t, s, &models.Event{ID: uuid.NewString(), OwnerID: "host", JoinCode: "JOIN", Status: models.EventStatusActive})
	req := &models.SongRequest{
		ID: uuid.NewString(), EventID: event.ID, SongbookID: uuid.NewString(),
		Status: models.StatusPending,
	}
	if err := s.CreateRequest(ctx, req); err != nil {
		t.Fatalf("create request: %v", err)
	}
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.tables.eventRequests),
		Key: map[string]types.AttributeValue{
			"event_id": avString(req.EventID), "id": avString(req.ID),
		},
		UpdateExpression:         aws.String("SET #status = :accepted"),
		ExpressionAttributeNames: map[string]string{"#status": "status"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":accepted": avString(string(models.StatusAccepted)),
		},
	})
	if err != nil {
		t.Fatalf("change canonical request: %v", err)
	}
	byID, err := s.GetRequest(ctx, req.ID)
	if err != nil || byID.Status != models.StatusAccepted {
		t.Fatalf("request-ID lookup = %v, error = %v; want accepted", byID, err)
	}
	byEvent, err := s.ListRequestsByEvent(ctx, req.EventID)
	if err != nil || len(byEvent) != 1 || byEvent[0].Status != models.StatusAccepted {
		t.Fatalf("event list = %v, error = %v; want accepted", byEvent, err)
	}
}

func TestCanonicalStatusMutationCommitsWithVersionAndRejectsMissingRow(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	eventID := uuid.NewString()
	helperCreateEvent(t, s, &models.Event{ID: eventID, OwnerID: "host", JoinCode: "JOIN", Status: models.EventStatusActive, QueueVersion: 4})
	req := &models.SongRequest{ID: uuid.NewString(), EventID: eventID, Status: models.StatusPending}
	if err := s.CreateRequest(ctx, req); err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Status = models.StatusAccepted
	if err := s.UpdateRequestWithQueueVersion(ctx, req, 4); err != nil {
		t.Fatalf("update canonical status: %v", err)
	}
	got, err := s.GetRequest(ctx, req.ID)
	if err != nil || got.Status != models.StatusAccepted {
		t.Fatalf("committed request = %v, error = %v", got, err)
	}
	gotEvent, err := s.GetEvent(ctx, eventID)
	if err != nil || gotEvent.QueueVersion != 5 {
		t.Fatalf("committed version = %v, error = %v", gotEvent, err)
	}

	deleteEventRequestMirror(t, s, req)
	req.Status = models.StatusCompleted
	if err := s.UpdateRequestWithQueueVersion(ctx, req, 5); err == nil {
		t.Fatal("status update succeeded with missing canonical row")
	}
	gotEvent, err = s.GetEvent(ctx, eventID)
	if err != nil || gotEvent.QueueVersion != 5 {
		t.Fatalf("version advanced after failed request update: %v, error = %v", gotEvent, err)
	}
	if _, err := s.GetRequest(ctx, req.ID); err == nil {
		t.Fatal("missing canonical row was found")
	}
	if err := s.DeleteEventRequestAtomic(ctx, eventID, req.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("delete missing row = %v, want ErrNotExist", err)
	}
}

func TestCanonicalReorderKeeps99RequestTransactionCapacity(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	eventID := uuid.NewString()
	helperCreateEvent(t, s, &models.Event{ID: eventID, OwnerID: "host", JoinCode: "JOIN", Status: models.EventStatusActive, QueueVersion: 7})
	ids := make([]string, 99)
	for i := range ids {
		ids[i] = uuid.NewString()
		if err := s.CreateRequest(ctx, &models.SongRequest{ID: ids[i], EventID: eventID, Status: models.StatusAccepted}); err != nil {
			t.Fatalf("create request %d: %v", i, err)
		}
	}
	if err := s.ReorderRequestsWithQueueVersion(ctx, eventID, ids, 7); err != nil {
		t.Fatalf("reorder 99 requests: %v", err)
	}
	gotEvent, err := s.GetEvent(ctx, eventID)
	if err != nil || gotEvent.QueueVersion != 8 {
		t.Fatalf("queue version after 99-request reorder = %v, error = %v", gotEvent, err)
	}
	got, err := s.GetRequest(ctx, ids[98])
	if err != nil || got.Rank != 99 {
		t.Fatalf("last reordered request = %v, error = %v", got, err)
	}
}

func TestSingerSlotReclaimReadsCanonicalStatus(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	eventID := uuid.NewString()
	createdEv := helperCreateEvent(t, s, &models.Event{ID: eventID, OwnerID: "host", JoinCode: "JOIN", Status: models.EventStatusActive})
	first := &models.SongRequest{ID: uuid.NewString(), EventID: eventID, SingerName: "Alice", Status: models.StatusAccepted}
	if err := s.CreateRequestAtVersion(ctx, first, 1, nil, createdEv.QueueVersion); err != nil {
		t.Fatalf("create first request: %v", err)
	}
	event, err := s.GetEvent(ctx, eventID)
	if err != nil {
		t.Fatalf("read event version: %v", err)
	}
	first.Status = models.StatusCompleted
	if err := s.UpdateRequestWithQueueVersion(ctx, first, event.QueueVersion); err != nil {
		t.Fatalf("complete first request: %v", err)
	}
	event, err = s.GetEvent(ctx, eventID)
	if err != nil {
		t.Fatalf("read event version after completion: %v", err)
	}
	second := &models.SongRequest{ID: uuid.NewString(), EventID: eventID, SingerName: "Alice", Status: models.StatusAccepted}
	if err := s.CreateRequestAtVersion(ctx, second, 1, nil, event.QueueVersion); err != nil {
		t.Fatalf("reclaim singer slot after canonical completion: %v", err)
	}
	got, err := s.GetRequest(ctx, second.ID)
	if err != nil || got.Status != models.StatusAccepted {
		t.Fatalf("reclaimed request = %v, error = %v", got, err)
	}
}

func TestDirectEventUpdateCannotRecreateMissingCanonicalRow(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	event := helperCreateEvent(t, s, &models.Event{ID: uuid.NewString(), OwnerID: "host", JoinCode: "JOIN", Status: models.EventStatusActive})
	req := &models.SongRequest{ID: uuid.NewString(), EventID: event.ID, Status: models.StatusPending}
	if err := s.CreateRequest(ctx, req); err != nil {
		t.Fatalf("create request: %v", err)
	}
	deleteEventRequestMirror(t, s, req)
	req.Status = models.StatusAccepted
	if err := s.UpdateRequest(ctx, req); err == nil {
		t.Fatal("direct update recreated missing canonical row")
	}
}

func deleteEventRequestMirror(t *testing.T, s *Store, req *models.SongRequest) {
	t.Helper()
	_, err := s.db.DeleteItem(context.Background(), &dynamodb.DeleteItemInput{
		TableName: aws.String(s.tables.eventRequests),
		Key: map[string]types.AttributeValue{
			"event_id": avString(req.EventID), "id": avString(req.ID),
		},
	})
	if err != nil {
		t.Fatalf("remove event-keyed test row: %v", err)
	}
}
