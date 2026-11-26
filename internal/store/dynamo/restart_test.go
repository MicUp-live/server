package dynamo

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"micup.live/server/internal/event"
	"micup.live/server/internal/models"
	"micup.live/server/internal/queue"
	"micup.live/server/internal/store"
)

func TestRestartClosesAdmissionAfterQueueRead(t *testing.T) {
	s := newLocalStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ev := helperCreateEvent(t, s, &models.Event{ID: "restart-admission", OwnerID: "host", JoinCode: "READ", Status: models.EventStatusActive})
	requests := &admissionSnapshotStore{Store: s, read: make(chan struct{}), resume: make(chan struct{})}
	service := queue.NewService(requests, s, []byte("test-secret"))
	type outcome struct {
		req *models.SongRequest
		err error
	}
	result := make(chan outcome, 1)
	go func() {
		req, err := service.SubmitRequest(ctx, queue.SubmitRequestCmd{
			EventID: ev.ID, SingerName: "Late Singer", SongTitle: "Late Song",
		})
		result <- outcome{req, err}
	}()
	select {
	case <-requests.read:
	case <-ctx.Done():
		t.Fatalf("admission did not read queue: %v", ctx.Err())
	}
	if err := s.BeginQueueRestart(ctx, ev); err != nil {
		close(requests.resume)
		t.Fatal(err)
	}
	close(requests.resume)
	got := <-result
	if got.req != nil || !errors.Is(got.err, queue.ErrShowEnded) {
		t.Fatalf("submission after restart barrier = (%v, %v), want ended", got.req, got.err)
	}
	listed, err := s.ListRequestsByEvent(ctx, ev.ID)
	if err != nil || len(listed) != 0 {
		t.Fatalf("request admitted during restart: requests=%v error=%v", listed, err)
	}
}

type secondBatchFailureRestartStore struct {
	store.EventStore
	failBatch2 bool
	batchCount int
}

func (w *secondBatchFailureRestartStore) ArchiveRestartBatch(ctx context.Context, eventID string, queueVersion int64, batch []*models.SongRequest) error {
	w.batchCount++
	if w.failBatch2 && w.batchCount == 2 {
		return errors.New("simulated second batch failure")
	}
	return w.EventStore.ArchiveRestartBatch(ctx, eventID, queueVersion, batch)
}

func TestRestartQueueLargeBatchFailureAndRetry(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	ev := helperCreateEvent(t, s, &models.Event{ID: "restart-large", OwnerID: "host", JoinCode: "RELA", Status: models.EventStatusActive})
	for i := 0; i < 105; i++ {
		req := &models.SongRequest{ID: fmt.Sprintf("request-%03d", i), EventID: ev.ID,
			Status: models.StatusAccepted, SingerName: fmt.Sprintf("Singer %d", i),
			TTL: time.Now().Add(24 * time.Hour).Unix(), Rank: int64(i + 1)}
		if err := s.CreateRequest(ctx, req); err != nil {
			t.Fatalf("create request %d: %v", i, err)
		}
	}
	slotPayload, err := marshal(singerSlotItem{ID: "slot#restart-large#singer#1", EventID: singerSlotPartition(ev.ID),
		RequestID: "request-000", CreatedKey: "slot#singer#01", TTL: time.Now().Add(24 * time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(s.tables.eventRequests), Item: slotPayload}); err != nil {
		t.Fatal(err)
	}
	ending, err := s.GetEvent(ctx, ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	ending.Status = models.EventStatusEnded
	if err := s.UpdateEventWithVersion(ctx, ending, true); err != nil {
		t.Fatal(err)
	}

	mockStore := &secondBatchFailureRestartStore{EventStore: s, failBatch2: true}
	svc := event.NewService(mockStore, nil, s, 0)
	if got, err := svc.RestartEvent(ctx, ev.ID, true); err == nil || got != nil {
		t.Fatalf("restart with failing second batch = (%v, %v), want failure", got, err)
	}
	closed, err := s.GetEvent(ctx, ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != models.EventStatusEnded || !closed.Restarting {
		t.Fatalf("failed restart reopened show: %+v", closed)
	}
	if _, err := svc.RestartEvent(ctx, ev.ID, false); err == nil {
		t.Fatal("restart without clearing bypassed an in-progress clear")
	}
	archived, err := s.GetRequest(ctx, "request-000")
	if err != nil || archived.Status != models.StatusCompleted {
		t.Fatalf("first batch not committed: request=%+v error=%v", archived, err)
	}
	remaining, err := s.GetRequest(ctx, "request-099")
	if err != nil || remaining.Status != models.StatusAccepted {
		t.Fatalf("failed batch changed request-099: request=%+v error=%v", remaining, err)
	}
	if err := s.CreateRequest(ctx, &models.SongRequest{ID: "late", EventID: ev.ID, Status: models.StatusPending}); err == nil {
		t.Fatal("request committed while restart was in progress")
	}
	resume := *closed
	resume.Status = models.EventStatusActive
	if err := s.UpdateEventWithVersion(ctx, &resume, true); err == nil {
		t.Fatal("resume committed while restart was in progress")
	}
	archived.Status = models.StatusAccepted
	if err := s.UpdateRequestWithQueueVersion(ctx, archived, closed.QueueVersion); err == nil {
		t.Fatal("host status update committed while restart was in progress")
	}
	if err := s.ReorderRequestsWithQueueVersion(ctx, ev.ID, []string{"request-000"}, closed.QueueVersion); err == nil {
		t.Fatal("host reorder committed while restart was in progress")
	}

	mockStore.failBatch2 = false
	restarted, err := svc.RestartEvent(ctx, ev.ID, true)
	if err != nil {
		t.Fatalf("retry restart: %v", err)
	}
	if restarted.Status != models.EventStatusActive || restarted.Restarting {
		t.Fatalf("retry did not open show: %+v", restarted)
	}
	for i := 0; i < 105; i++ {
		req, err := s.GetRequest(ctx, fmt.Sprintf("request-%03d", i))
		if err != nil || req.Status != models.StatusCompleted || req.TTL != 0 {
			t.Fatalf("request %d after restart = (%+v, %v)", i, req, err)
		}
	}
	slot, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(s.tables.eventRequests),
		Key: eventRequestKey(singerSlotPartition(ev.ID), "slot#restart-large#singer#1"), ConsistentRead: aws.Bool(true)})
	if err != nil || len(slot.Item) != 0 {
		t.Fatalf("slot remained after restart: item=%v error=%v", slot.Item, err)
	}
}

func TestRestartRejectsConcurrentRequestModification(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	ev := helperCreateEvent(t, s, &models.Event{ID: "restart-modified", OwnerID: "host", JoinCode: "MODI", Status: models.EventStatusActive})
	req := &models.SongRequest{ID: "old-request", EventID: ev.ID, Status: models.StatusPending}
	if err := s.CreateRequest(ctx, req); err != nil {
		t.Fatal(err)
	}
	ending, err := s.GetEvent(ctx, ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	ending.Status = models.EventStatusEnded
	if err := s.UpdateEventWithVersion(ctx, ending, true); err != nil {
		t.Fatal(err)
	}
	req.Status = models.StatusAccepted
	if err := s.UpdateRequest(ctx, req); err != nil {
		t.Fatal(err)
	}
	staleReq := &models.SongRequest{ID: "old-request", EventID: ev.ID, Status: models.StatusPending}
	err = s.ArchiveRestartBatch(ctx, ev.ID, ending.QueueVersion, []*models.SongRequest{staleReq})
	if err == nil {
		t.Fatal("archive batch accepted stale request status")
	}
}

func TestFinishQueueRestartRejectsStaleVersion(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	ev := helperCreateEvent(t, s, &models.Event{ID: "restart-conflict", OwnerID: "host", JoinCode: "RECO", Status: models.EventStatusActive})
	req := &models.SongRequest{ID: "old-request", EventID: ev.ID, Status: models.StatusPending}
	if err := s.CreateRequest(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginQueueRestart(ctx, ev); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetEvent(ctx, ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ArchiveRestartBatch(ctx, ev.ID, current.QueueVersion, []*models.SongRequest{req}); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishQueueRestart(ctx, ev.ID, current.QueueVersion); err == nil {
		t.Fatal("stale finalization succeeded")
	}
	closed, err := s.GetEvent(ctx, ev.ID)
	if err != nil || !closed.Restarting || closed.Status != models.EventStatusEnded {
		t.Fatalf("stale finalization opened show: event=%+v error=%v", closed, err)
	}
}

func TestRestartPreservesLegacyCompletedAt(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	ev := helperCreateEvent(t, s, &models.Event{
		ID:           "restart-legacy-completed",
		OwnerID:      "host",
		JoinCode:     "LEGC",
		Status:       models.EventStatusActive,
		RotationMode: models.RotationModeFairPriority,
	})

	// Insert a legacy completed request (CompletedAt == nil) with an older historical UpdatedAt and a retention TTL.
	historicalTime := time.Date(2026, 11, 24, 19, 0, 0, 0, time.UTC)
	legacyReq := &models.SongRequest{
		ID:          "legacy-completed-1",
		EventID:     ev.ID,
		SingerName:  "Alice",
		SongTitle:   "Old Song",
		SongArtist:  "Artist",
		Status:      models.StatusCompleted,
		Rank:        1,
		CreatedAt:   historicalTime.Add(-5 * time.Minute),
		UpdatedAt:   historicalTime,
		CompletedAt: nil,
		TTL:         time.Now().Add(24 * time.Hour).Unix(),
	}
	payload, err := marshal(buildRequestItem(legacyReq, ev.ID, timestamp(historicalTime)))
	if err != nil {
		t.Fatalf("marshal legacy request: %v", err)
	}
	if _, err := s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.tables.eventRequests),
		Item:      payload,
	}); err != nil {
		t.Fatalf("put legacy request: %v", err)
	}

	svc := event.NewService(s, nil, s, 0)
	if _, err := svc.RestartEvent(ctx, ev.ID, true); err != nil {
		t.Fatalf("RestartEvent: %v", err)
	}

	after, err := s.GetRequest(ctx, legacyReq.ID)
	if err != nil {
		t.Fatalf("GetRequest after restart: %v", err)
	}
	if after.TTL != 0 {
		t.Fatalf("expected TTL cleared after restart, got %d", after.TTL)
	}
	if after.CompletedAt == nil || !after.CompletedAt.Equal(historicalTime) {
		t.Fatalf("expected legacy CompletedAt to preserve historical UpdatedAt %v, got %v", historicalTime, after.CompletedAt)
	}
}
