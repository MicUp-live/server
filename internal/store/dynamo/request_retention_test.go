package dynamo

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"

	"micup.live/server/internal/models"
)

func TestSetRequestsTTLByEventSetsAndClearsTTL(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	ev := helperCreateEvent(t, s, &models.Event{ID: uuid.NewString(), Status: models.EventStatusActive})
	req := &models.SongRequest{ID: uuid.NewString(), EventID: ev.ID, Status: models.StatusPending}
	if err := s.CreateRequest(ctx, req); err != nil {
		t.Fatalf("create event request: %v", err)
	}
	ev.Status = models.EventStatusEnded
	if err := s.UpdateEventWithVersion(ctx, ev, true); err != nil {
		t.Fatal(err)
	}

	expiresAt := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	if err := s.SetRequestsTTLByEvent(ctx, req.EventID, expiresAt); err != nil {
		t.Fatalf("set retention: %v", err)
	}
	assertRequestTTLs(t, s, req, avNumber(expiresAt.Unix()))

	if err := s.SetRequestsTTLByEvent(ctx, req.EventID, time.Time{}); err != nil {
		t.Fatalf("clear retention: %v", err)
	}
	assertRequestTTLs(t, s, req, nil)
}

func assertRequestTTLs(t *testing.T, s *Store, req *models.SongRequest, want types.AttributeValue) {
	t.Helper()
	got, err := s.db.GetItem(context.Background(), &dynamodb.GetItemInput{
		TableName:      aws.String(s.tables.eventRequests),
		Key:            eventRequestKey(req.EventID, req.ID),
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("read %s: %v", s.tables.eventRequests, err)
	}
	if want == nil {
		if _, exists := got.Item["ttl"]; exists {
			t.Fatalf("%s ttl still present: %v", s.tables.eventRequests, got.Item["ttl"])
		}
		return
	}
	if got.Item["ttl"] != want {
		gotNumber, gotOK := got.Item["ttl"].(*types.AttributeValueMemberN)
		wantNumber, wantOK := want.(*types.AttributeValueMemberN)
		if !gotOK || !wantOK || gotNumber.Value != wantNumber.Value {
			t.Fatalf("%s ttl = %v, want %v", s.tables.eventRequests, got.Item["ttl"], want)
		}
	}
}

func TestSetRequestsTTLByEventStronglyConsistentEnumeration(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	eventID := uuid.NewString()
	ev := helperCreateEvent(t, s, &models.Event{ID: eventID, Status: models.EventStatusActive})

	req1 := &models.SongRequest{ID: uuid.NewString(), EventID: eventID, Status: models.StatusAccepted}
	req2 := &models.SongRequest{ID: uuid.NewString(), EventID: eventID, Status: models.StatusPending}
	if err := s.CreateRequest(ctx, req1); err != nil {
		t.Fatalf("create req1: %v", err)
	}
	if err := s.CreateRequest(ctx, req2); err != nil {
		t.Fatalf("create req2: %v", err)
	}
	ev.Status = models.EventStatusEnded
	if err := s.UpdateEventWithVersion(ctx, ev, true); err != nil {
		t.Fatal(err)
	}

	slotID := "slot#" + eventID + "#singer#1"
	slotsKey := "slots#" + eventID
	slotPayload, err := marshal(singerSlotItem{
		ID:         slotID,
		EventID:    slotsKey,
		RequestID:  req1.ID,
		CreatedKey: "slot#singer#1",
		TTL:        0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.tables.eventRequests),
		Item:      slotPayload,
	}); err != nil {
		t.Fatal(err)
	}

	expiresAt := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	if err := s.SetRequestsTTLByEvent(ctx, eventID, expiresAt); err != nil {
		t.Fatalf("set retention for all items: %v", err)
	}

	assertRequestTTLs(t, s, req1, avNumber(expiresAt.Unix()))
	assertRequestTTLs(t, s, req2, avNumber(expiresAt.Unix()))

	gotSlot, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(s.tables.eventRequests),
		Key:            eventRequestKey(slotsKey, slotID),
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("read slot: %v", err)
	}
	if gotSlot.Item["ttl"] == nil || gotSlot.Item["ttl"].(*types.AttributeValueMemberN).Value != fmt.Sprintf("%d", expiresAt.Unix()) {
		t.Fatalf("slot ttl = %v, want %v", gotSlot.Item["ttl"], expiresAt.Unix())
	}

	// Clearing retention clears TTL for both requests and singer slots.
	if err := s.SetRequestsTTLByEvent(ctx, eventID, time.Time{}); err != nil {
		t.Fatalf("clear retention: %v", err)
	}
	assertRequestTTLs(t, s, req1, nil)
	assertRequestTTLs(t, s, req2, nil)

	clearedSlot, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(s.tables.eventRequests),
		Key:            eventRequestKey(slotsKey, slotID),
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("read cleared slot: %v", err)
	}
	if _, exists := clearedSlot.Item["ttl"]; exists {
		t.Fatalf("slot ttl still present after clear: %v", clearedSlot.Item["ttl"])
	}
}

func TestSetRequestsTTLByEventIdempotentRetry(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	eventID := uuid.NewString()
	ev := helperCreateEvent(t, s, &models.Event{ID: eventID, Status: models.EventStatusActive})

	req1 := &models.SongRequest{ID: "req-01-" + uuid.NewString(), EventID: eventID, Status: models.StatusAccepted}
	req2 := &models.SongRequest{ID: "req-02-" + uuid.NewString(), EventID: eventID, Status: models.StatusPending}
	if err := s.CreateRequest(ctx, req1); err != nil {
		t.Fatalf("create req1: %v", err)
	}
	if err := s.CreateRequest(ctx, req2); err != nil {
		t.Fatalf("create req2: %v", err)
	}
	ev.Status = models.EventStatusEnded
	if err := s.UpdateEventWithVersion(ctx, ev, true); err != nil {
		t.Fatal(err)
	}

	expiresAt := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)

	// Attempt 1
	if err := s.SetRequestsTTLByEvent(ctx, eventID, expiresAt); err != nil {
		t.Fatalf("initial SetRequestsTTLByEvent failed: %v", err)
	}
	assertRequestTTLs(t, s, req1, avNumber(expiresAt.Unix()))
	assertRequestTTLs(t, s, req2, avNumber(expiresAt.Unix()))

	// Attempt 2: Retry with same expiresAt converges idempotently.
	if err := s.SetRequestsTTLByEvent(ctx, eventID, expiresAt); err != nil {
		t.Fatalf("retry SetRequestsTTLByEvent failed: %v", err)
	}

	assertRequestTTLs(t, s, req1, avNumber(expiresAt.Unix()))
	assertRequestTTLs(t, s, req2, avNumber(expiresAt.Unix()))
}

func TestSetRequestsTTLByEventConcurrentlyDeletedItemHandled(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	eventID := uuid.NewString()
	ev := helperCreateEvent(t, s, &models.Event{ID: eventID, Status: models.EventStatusActive})

	req1 := &models.SongRequest{ID: uuid.NewString(), EventID: eventID, Status: models.StatusAccepted}
	req2 := &models.SongRequest{ID: uuid.NewString(), EventID: eventID, Status: models.StatusPending}
	if err := s.CreateRequest(ctx, req1); err != nil {
		t.Fatalf("create req1: %v", err)
	}
	if err := s.CreateRequest(ctx, req2); err != nil {
		t.Fatalf("create req2: %v", err)
	}
	ev.Status = models.EventStatusEnded
	if err := s.UpdateEventWithVersion(ctx, ev, true); err != nil {
		t.Fatal(err)
	}

	// Simulate req2 being completely deleted from both tables
	if err := s.DeleteRequest(ctx, req2.ID); err != nil {
		t.Fatal(err)
	}

	expiresAt := time.Now().UTC().Add(12 * time.Hour).Truncate(time.Second)
	if err := s.SetRequestsTTLByEvent(ctx, eventID, expiresAt); err != nil {
		t.Fatalf("expected SetRequestsTTLByEvent to succeed skipping deleted item: %v", err)
	}

	assertRequestTTLs(t, s, req1, avNumber(expiresAt.Unix()))
}

func TestSetRequestsTTLByEventRejectsWhenRestartInProgress(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	ev := helperCreateEvent(t, s, &models.Event{ID: uuid.NewString(), OwnerID: "host", JoinCode: "RTTL", Status: models.EventStatusActive})
	req := &models.SongRequest{ID: uuid.NewString(), EventID: ev.ID, Status: models.StatusAccepted}
	if err := s.CreateRequest(ctx, req); err != nil {
		t.Fatal(err)
	}

	if err := s.BeginQueueRestart(ctx, ev); err != nil {
		t.Fatal(err)
	}

	expiresAt := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	err := s.SetRequestsTTLByEvent(ctx, ev.ID, expiresAt)
	if err == nil {
		t.Fatal("expected SetRequestsTTLByEvent to fail when restart is in progress")
	}
}
