package dynamo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

func TestStore_UpdateRequestAndReorder_AtomicNoReorder(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	eventID := uuid.NewString()
	event := &models.Event{
		ID:           eventID,
		OwnerID:      "host-1",
		Status:       models.EventStatusActive,
		JoinCode:     "JOIN",
		QueueVersion: 1,
	}
	helperCreateEvent(t, s, event)

	req := &models.SongRequest{
		ID:         uuid.NewString(),
		EventID:    eventID,
		SingerName: "Alice",
		SongTitle:  "Song A",
		Status:     models.StatusPending,
		Rank:       999,
		Round:      0,
		CreatedAt:  time.Now().UTC(),
	}
	if err := s.CreateRequest(ctx, req); err != nil {
		t.Fatalf("create request: %v", err)
	}

	// Update staged request to accepted with rank 1, without reordering other requests
	req.Status = models.StatusAccepted
	req.Rank = 1
	if err := s.UpdateRequestAndReorder(ctx, req, nil); err != nil {
		t.Fatalf("UpdateRequestAndReorder failed: %v", err)
	}

	// Verify request was updated
	savedReq, err := s.GetRequest(ctx, req.ID)
	if err != nil {
		t.Fatalf("get request: %v", err)
	}
	if savedReq.Status != models.StatusAccepted || savedReq.Rank != 1 {
		t.Fatalf("unexpected saved request: status=%s, rank=%d", savedReq.Status, savedReq.Rank)
	}

	// Verify event queue version was incremented atomically
	savedEvent, err := s.GetEvent(ctx, eventID)
	if err != nil {
		t.Fatalf("get event: %v", err)
	}
	if savedEvent.QueueVersion != 2 {
		t.Fatalf("expected event queue version 2, got %d", savedEvent.QueueVersion)
	}
}

func TestStore_UpdateRequestAndReorder_WithDisplacementAndRollback(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	eventID := uuid.NewString()
	event := &models.Event{
		ID:           eventID,
		OwnerID:      "host-1",
		Status:       models.EventStatusActive,
		JoinCode:     "JOIN",
		QueueVersion: 1,
	}
	helperCreateEvent(t, s, event)

	now := time.Now().UTC()
	r1 := &models.SongRequest{ID: uuid.NewString(), EventID: eventID, SingerName: "Alice", Status: models.StatusAccepted, Rank: 1, CreatedAt: now.Add(-3 * time.Minute)}
	r2 := &models.SongRequest{ID: uuid.NewString(), EventID: eventID, SingerName: "Bob", Status: models.StatusAccepted, Rank: 2, CreatedAt: now.Add(-2 * time.Minute)}
	r3 := &models.SongRequest{ID: uuid.NewString(), EventID: eventID, SingerName: "Charlie", Status: models.StatusAccepted, Rank: 3, CreatedAt: now.Add(-1 * time.Minute)}
	for _, r := range []*models.SongRequest{r1, r2, r3} {
		if err := s.CreateRequest(ctx, r); err != nil {
			t.Fatalf("create request: %v", err)
		}
	}

	rEve := &models.SongRequest{ID: uuid.NewString(), EventID: eventID, SingerName: "Eve", Status: models.StatusPending, Rank: 999, CreatedAt: now}
	if err := s.CreateRequest(ctx, rEve); err != nil {
		t.Fatalf("create rEve: %v", err)
	}

	// Eve accepted and inserted between r1 and r2: new order is [r1, rEve, r2, r3]
	rEve.Status = models.StatusAccepted
	reorderIDs := []string{r1.ID, rEve.ID, r2.ID, r3.ID}
	if err := s.UpdateRequestAndReorder(ctx, rEve, reorderIDs); err != nil {
		t.Fatalf("UpdateRequestAndReorder failed: %v", err)
	}

	// Verify all 4 requests received correct contiguous ranks
	up1, _ := s.GetRequest(ctx, r1.ID)
	upEve, _ := s.GetRequest(ctx, rEve.ID)
	up2, _ := s.GetRequest(ctx, r2.ID)
	up3, _ := s.GetRequest(ctx, r3.ID)

	if up1.Rank != 1 || upEve.Rank != 2 || up2.Rank != 3 || up3.Rank != 4 {
		t.Fatalf("unexpected ranks: r1=%d, rEve=%d, r2=%d, r3=%d", up1.Rank, upEve.Rank, up2.Rank, up3.Rank)
	}

	savedEvent, _ := s.GetEvent(ctx, eventID)
	if savedEvent.QueueVersion != 2 {
		t.Fatalf("expected queue version 2, got %d", savedEvent.QueueVersion)
	}

	// Verify atomic rollback when reorder list contains a non-existent request
	rEve.Status = models.StatusUpNext
	badReorder := []string{r1.ID, rEve.ID, "non-existent-id"}
	err := s.UpdateRequestAndReorder(ctx, rEve, badReorder)
	if err == nil {
		t.Fatalf("expected error on non-existent reorder ID, got nil")
	}

	// Verify Eve status was NOT modified to up_next and version was NOT incremented
	rollbackEve, _ := s.GetRequest(ctx, rEve.ID)
	if rollbackEve.Status != models.StatusAccepted {
		t.Fatalf("expected Eve status to remain accepted on rollback, got %s", rollbackEve.Status)
	}
	evAfterRollback, _ := s.GetEvent(ctx, eventID)
	if evAfterRollback.QueueVersion != 2 {
		t.Fatalf("expected queue version to remain 2 on rollback, got %d", evAfterRollback.QueueVersion)
	}
}

func TestStore_UpdateRequestAndReorderWithQueueVersion(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	eventID := uuid.NewString()
	event := &models.Event{
		ID:           eventID,
		OwnerID:      "host-1",
		Status:       models.EventStatusActive,
		JoinCode:     "JOIN",
		QueueVersion: 5,
	}
	helperCreateEvent(t, s, event)

	req := &models.SongRequest{
		ID:         uuid.NewString(),
		EventID:    eventID,
		SingerName: "Alice",
		Status:     models.StatusAccepted,
		Rank:       1,
		CreatedAt:  time.Now().UTC(),
	}
	if err := s.CreateRequest(ctx, req); err != nil {
		t.Fatalf("create request: %v", err)
	}

	// 1. Stale version conflict: expected 4, actual 5
	req.Status = models.StatusCompleted
	err := s.UpdateRequestAndReorderWithQueueVersion(ctx, req, nil, 4)
	if err == nil {
		t.Fatalf("expected conflict error on stale version, got nil")
	}
	var conflict *store.QueueVersionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected QueueVersionConflictError, got %T: %v", err, err)
	}
	if conflict.Current != 5 {
		t.Fatalf("expected conflict.Current 5, got %d", conflict.Current)
	}

	// Verify no changes applied
	unmodReq, _ := s.GetRequest(ctx, req.ID)
	if unmodReq.Status != models.StatusAccepted {
		t.Fatalf("request status changed on conflict: %s", unmodReq.Status)
	}

	// 2. Matching version: expected 5
	if err := s.UpdateRequestAndReorderWithQueueVersion(ctx, req, nil, 5); err != nil {
		t.Fatalf("expected success with matching version 5, got: %v", err)
	}
	savedEvent, _ := s.GetEvent(ctx, eventID)
	if savedEvent.QueueVersion != 6 {
		t.Fatalf("expected queue version 6, got %d", savedEvent.QueueVersion)
	}
	completedReq, _ := s.GetRequest(ctx, req.ID)
	if completedReq.Status != models.StatusCompleted {
		t.Fatalf("expected status completed, got %s", completedReq.Status)
	}
}

func TestStore_UpdateRequestAndReorderWithIdempotency(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	eventID := uuid.NewString()
	event := &models.Event{
		ID:           eventID,
		OwnerID:      "host-1",
		Status:       models.EventStatusActive,
		JoinCode:     "JOIN",
		QueueVersion: 10,
	}
	helperCreateEvent(t, s, event)

	r1 := &models.SongRequest{ID: uuid.NewString(), EventID: eventID, SingerName: "Alice", Status: models.StatusAccepted, Rank: 1, CreatedAt: time.Now().UTC()}
	r2 := &models.SongRequest{ID: uuid.NewString(), EventID: eventID, SingerName: "Bob", Status: models.StatusAccepted, Rank: 2, CreatedAt: time.Now().UTC()}
	_ = s.CreateRequest(ctx, r1)
	_ = s.CreateRequest(ctx, r2)

	r1.Status = models.StatusCompleted
	key := uuid.NewString()
	expectedVer := int64(10)
	route := fmt.Sprintf("events/%s/requests", eventID)
	requestHash := "hash-status-completed"

	// 1. Initial idempotent mutation
	res1, err := s.UpdateRequestAndReorderWithIdempotency(ctx, r1, nil, &expectedVer, key, event.OwnerID, route, requestHash)
	if err != nil {
		t.Fatalf("first idempotent call failed: %v", err)
	}
	if res1.QueueVersion != 11 || res1.Replay {
		t.Fatalf("unexpected res1: version=%d, replay=%v", res1.QueueVersion, res1.Replay)
	}

	savedEv, _ := s.GetEvent(ctx, eventID)
	if savedEv.QueueVersion != 11 {
		t.Fatalf("expected event version 11, got %d", savedEv.QueueVersion)
	}

	// 2. Replay with identical key returns cached result without bumping version
	res2, err := s.UpdateRequestAndReorderWithIdempotency(ctx, r1, nil, &expectedVer, key, event.OwnerID, route, requestHash)
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	if res2.QueueVersion != 11 || !res2.Replay {
		t.Fatalf("expected cached replay: version=%d, replay=%v", res2.QueueVersion, res2.Replay)
	}
	evAfterReplay, _ := s.GetEvent(ctx, eventID)
	if evAfterReplay.QueueVersion != 11 {
		t.Fatalf("replay bumped queue version to %d", evAfterReplay.QueueVersion)
	}

	// 3. Conflict on mismatched request hash
	_, err = s.UpdateRequestAndReorderWithIdempotency(ctx, r1, nil, &expectedVer, key, event.OwnerID, route, "different-hash")
	if err == nil {
		t.Fatalf("expected conflict on mismatched hash, got nil")
	}
	var idemConflict *store.IdempotencyConflictError
	if !errors.As(err, &idemConflict) {
		t.Fatalf("expected IdempotencyConflictError, got %T", err)
	}
}

func TestStore_TransitionQueueRotation(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	eventID := uuid.NewString()
	event := &models.Event{
		ID:           eventID,
		OwnerID:      "host-1",
		RotationMode: models.RotationModeRoundRobin,
		CurrentRound: 1,
		Status:       models.EventStatusActive,
		JoinCode:     "JOIN",
		QueueVersion: 3,
	}
	helperCreateEvent(t, s, event)

	now := time.Now().UTC()
	r1 := &models.SongRequest{ID: uuid.NewString(), EventID: eventID, SingerName: "Alice", Status: models.StatusAccepted, Rank: 1, Round: 1, CreatedAt: now.Add(-4 * time.Minute)}
	r2 := &models.SongRequest{ID: uuid.NewString(), EventID: eventID, SingerName: "Bob", Status: models.StatusAccepted, Rank: 2, Round: 1, CreatedAt: now.Add(-3 * time.Minute)}
	r3 := &models.SongRequest{ID: uuid.NewString(), EventID: eventID, SingerName: "Charlie", Status: models.StatusAccepted, Rank: 3, Round: 1, CreatedAt: now.Add(-2 * time.Minute)}
	r4 := &models.SongRequest{ID: uuid.NewString(), EventID: eventID, SingerName: "Dave", Status: models.StatusAccepted, Rank: 4, Round: 1, CreatedAt: now.Add(-1 * time.Minute)}
	for _, r := range []*models.SongRequest{r1, r2, r3, r4} {
		_ = s.CreateRequest(ctx, r)
	}

	// Switch event to fair_priority and update requests (rounds set to 0)
	event.RotationMode = models.RotationModeFairPriority
	r1.Round = 0
	r2.Round = 0
	r3.Round = 0
	r4.Round = 0
	reqsToUpdate := []*models.SongRequest{r1, r2, r3, r4}

	if err := s.TransitionQueueRotation(ctx, event, reqsToUpdate); err != nil {
		t.Fatalf("TransitionQueueRotation failed: %v", err)
	}

	// Verify event updated and queue version bumped
	savedEv, err := s.GetEvent(ctx, eventID)
	if err != nil {
		t.Fatalf("get event: %v", err)
	}
	if savedEv.RotationMode != models.RotationModeFairPriority {
		t.Fatalf("expected rotation mode fair_priority, got %s", savedEv.RotationMode)
	}
	if savedEv.QueueVersion != 4 {
		t.Fatalf("expected queue version 4, got %d", savedEv.QueueVersion)
	}

	// Verify all requests updated in DynamoDB
	for _, r := range reqsToUpdate {
		saved, err := s.GetRequest(ctx, r.ID)
		if err != nil {
			t.Fatalf("get request %s: %v", r.ID, err)
		}
		if saved.Round != 0 {
			t.Fatalf("expected request %s Round 0, got %d", r.ID, saved.Round)
		}
	}
}

func TestStore_TransitionQueueRotation_ConcurrentConflict(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	eventID := uuid.NewString()
	event := &models.Event{
		ID:           eventID,
		OwnerID:      "host-1",
		RotationMode: models.RotationModeRoundRobin,
		CurrentRound: 1,
		Status:       models.EventStatusActive,
		JoinCode:     "JOIN",
		QueueVersion: 3,
	}
	helperCreateEvent(t, s, event)

	req := &models.SongRequest{
		ID:         uuid.NewString(),
		EventID:    eventID,
		SingerName: "Alice",
		Status:     models.StatusAccepted,
		Rank:       1,
		Round:      1,
		CreatedAt:  time.Now().UTC(),
	}
	_ = s.CreateRequest(ctx, req)

	// Simulate concurrent queue activity by bumping queue version in DynamoDB
	newVer, err := s.BumpQueueVersion(ctx, eventID)
	if err != nil || newVer != 4 {
		t.Fatalf("bump queue version failed: %d, err=%v", newVer, err)
	}

	// Now try to transition with stale snapshot version 3
	event.RotationMode = models.RotationModeFairPriority
	event.QueueVersion = 3
	req.Round = 0

	err = s.TransitionQueueRotation(ctx, event, []*models.SongRequest{req})
	if err == nil {
		t.Fatalf("expected conflict on stale queue version, got nil")
	}
	var conflict *store.QueueVersionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected QueueVersionConflictError, got %T: %v", err, err)
	}
	if conflict.Current != 4 {
		t.Fatalf("expected conflict.Current 4, got %d", conflict.Current)
	}

	// Verify request was NOT updated in DynamoDB
	unmod, _ := s.GetRequest(ctx, req.ID)
	if unmod.Round != 1 {
		t.Fatalf("request Round was modified on conflict: %d", unmod.Round)
	}
}

func TestStore_TransitionQueueRotation_InitialVersion_ConcurrentConflict(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	eventID := uuid.NewString()
	event := &models.Event{
		ID:           eventID,
		OwnerID:      "host-1",
		RotationMode: models.RotationModeRoundRobin,
		CurrentRound: 1,
		Status:       models.EventStatusActive,
		JoinCode:     "INIT",
		// QueueVersion is not set (0), should default to 1 on CreateEvent
	}
	helperCreateEvent(t, s, event)
	if event.QueueVersion != 1 {
		t.Fatalf("expected initial QueueVersion 1, got %d", event.QueueVersion)
	}

	req := &models.SongRequest{
		ID:         uuid.NewString(),
		EventID:    eventID,
		SingerName: "Alice",
		Status:     models.StatusAccepted,
		Rank:       1,
		Round:      1,
		CreatedAt:  time.Now().UTC(),
	}
	_ = s.CreateRequest(ctx, req)

	// Simulate concurrent queue activity by bumping queue version from 1 to 2
	newVer, err := s.BumpQueueVersion(ctx, eventID)
	if err != nil || newVer != 2 {
		t.Fatalf("bump queue version failed: %d, err=%v", newVer, err)
	}

	// Now try to transition with snapshot version 1
	event.RotationMode = models.RotationModeFairPriority
	event.QueueVersion = 1
	req.Round = 0

	err = s.TransitionQueueRotation(ctx, event, []*models.SongRequest{req})
	if err == nil {
		t.Fatalf("expected conflict on initial stale queue version, got nil")
	}
	var conflict *store.QueueVersionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected QueueVersionConflictError, got %T: %v", err, err)
	}
	if conflict.Current != 2 {
		t.Fatalf("expected conflict.Current 2, got %d", conflict.Current)
	}

	// Verify request was NOT modified in DynamoDB
	unmod, _ := s.GetRequest(ctx, req.ID)
	if unmod.Round != 1 {
		t.Fatalf("request Round was modified on conflict: %d", unmod.Round)
	}
}

func TestStore_TransitionQueueRotation_ExceedsAtomicLimit(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	eventID := uuid.NewString()
	event := &models.Event{
		ID:           eventID,
		OwnerID:      "host-1",
		RotationMode: models.RotationModeRoundRobin,
		CurrentRound: 1,
		Status:       models.EventStatusActive,
		JoinCode:     "JOIN",
		QueueVersion: 1,
	}
	helperCreateEvent(t, s, event)

	// 100 requests exceeds max atomic size 99
	reqs := make([]*models.SongRequest, 100)
	for i := 0; i < 100; i++ {
		reqs[i] = &models.SongRequest{
			ID:         fmt.Sprintf("req-%d", i),
			EventID:    eventID,
			SingerName: fmt.Sprintf("Singer-%d", i),
			Status:     models.StatusAccepted,
			Rank:       int64(i + 1),
			CreatedAt:  time.Now().UTC(),
		}
	}

	event.RotationMode = models.RotationModeFairPriority
	err := s.TransitionQueueRotation(ctx, event, reqs)
	if err == nil {
		t.Fatalf("expected error for > 99 requests, got nil")
	}
	if !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("expected os.ErrInvalid, got: %v", err)
	}
}

func TestStore_CreateRequestAtomic(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	eventID := uuid.NewString()
	event := &models.Event{
		ID:           eventID,
		OwnerID:      "host-1",
		RotationMode: models.RotationModeFairPriority,
		Status:       models.EventStatusActive,
		JoinCode:     "JOIN",
		QueueVersion: 1,
	}
	helperCreateEvent(t, s, event)

	// Create an existing request
	req1 := &models.SongRequest{
		ID:         uuid.NewString(),
		EventID:    eventID,
		SingerName: "Alice",
		Status:     models.StatusAccepted,
		Rank:       1000,
		CreatedAt:  time.Now().UTC(),
	}
	if err := s.CreateRequest(ctx, req1); err != nil {
		t.Fatalf("create req1: %v", err)
	}

	// Now create a new request (req2) atomically:
	// - Claims slot for "Bob" (maxActivePerSinger: 1)
	// - Reorders req1 and req2 (req2 placed first at rank 1, req1 at rank 2)
	// - Bumps event QueueVersion to 2
	req2ID := uuid.NewString()
	req2 := &models.SongRequest{
		ID:         req2ID,
		EventID:    eventID,
		SingerName: "Bob",
		Status:     models.StatusAccepted,
		Rank:       500,
		CreatedAt:  time.Now().UTC(),
	}
	reorderIDs := []string{req2ID, req1.ID}
	if err := s.CreateRequestAtVersion(ctx, req2, 1, reorderIDs, 1); err != nil {
		t.Fatalf("CreateRequestAtVersion failed: %v", err)
	}

	// Verify req2 was created
	created2, err := s.GetRequest(ctx, req2ID)
	if err != nil {
		t.Fatalf("get req2: %v", err)
	}
	if created2.SingerName != "Bob" {
		t.Fatalf("expected Bob, got %s", created2.SingerName)
	}

	// Verify req1 was reordered to rank 2
	updated1, err := s.GetRequest(ctx, req1.ID)
	if err != nil {
		t.Fatalf("get req1: %v", err)
	}
	if updated1.Rank != 2 {
		t.Fatalf("expected req1 rank 2, got %d", updated1.Rank)
	}

	// Verify event QueueVersion was bumped to 2
	evUpdated, err := s.GetEvent(ctx, eventID)
	if err != nil {
		t.Fatalf("get event: %v", err)
	}
	if evUpdated.QueueVersion != 2 {
		t.Fatalf("expected event queue version 2, got %d", evUpdated.QueueVersion)
	}

	// Second request for Bob should exceed limit of 1
	req3 := &models.SongRequest{
		ID:         uuid.NewString(),
		EventID:    eventID,
		SingerName: "Bob",
		Status:     models.StatusAccepted,
		Rank:       600,
		CreatedAt:  time.Now().UTC(),
	}
	err = s.CreateRequestAtVersion(ctx, req3, 1, nil, 2)
	if !errors.Is(err, store.ErrSingerLimitExceeded) {
		t.Fatalf("expected ErrSingerLimitExceeded, got: %v", err)
	}
}

func TestStore_CreateRequestAtVersionConcurrentPending(t *testing.T) {
	for _, maxActive := range []int{0, 1} {
		t.Run(fmt.Sprintf("max-active-%d", maxActive), func(t *testing.T) {
			s := newLocalStore(t)
			ctx := context.Background()
			event := &models.Event{
				ID: uuid.NewString(), OwnerID: "host-1", JoinCode: "JOIN",
				Status: models.EventStatusActive, QueueVersion: 1,
			}
			helperCreateEvent(t, s, event)

			requests := []*models.SongRequest{
				{ID: uuid.NewString(), EventID: event.ID, SingerName: "Singer", Status: models.StatusPending},
				{ID: uuid.NewString(), EventID: event.ID, SingerName: "Singer", Status: models.StatusPending},
			}
			type result struct {
				id  string
				err error
			}
			start := make(chan struct{})
			results := make(chan result, len(requests))
			for _, req := range requests {
				go func(req *models.SongRequest) {
					<-start
					results <- result{id: req.ID, err: s.CreateRequestAtVersion(ctx, req, maxActive, nil, 1)}
				}(req)
			}
			close(start)

			succeeded := 0
			conflicted := 0
			for range requests {
				outcome := <-results
				if outcome.err == nil {
					succeeded++
					continue
				}
				var conflict *store.QueueVersionConflictError
				if !errors.As(outcome.err, &conflict) || conflict.Current != 2 {
					t.Fatalf("request %s error = %v, want version conflict at 2", outcome.id, outcome.err)
				}
				conflicted++
				if _, err := s.GetRequest(ctx, outcome.id); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("conflicted request %s was partially committed: %v", outcome.id, err)
				}
			}
			if succeeded != 1 || conflicted != 1 {
				t.Fatalf("commits=%d conflicts=%d, want one of each", succeeded, conflicted)
			}
			listed, err := s.ListRequestsByEvent(ctx, event.ID)
			if err != nil || len(listed) != 1 {
				t.Fatalf("event requests = %v, error = %v; want one", listed, err)
			}
			updated, err := s.GetEvent(ctx, event.ID)
			if err != nil || updated.QueueVersion != 2 {
				t.Fatalf("event version = %v, error = %v; want 2", updated, err)
			}
		})
	}
}

func TestStore_CreateRequestAtVersionRejectsHostReorderAfterPlan(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	event := &models.Event{
		ID: uuid.NewString(), OwnerID: "host-1", JoinCode: "JOIN",
		Status: models.EventStatusActive, QueueVersion: 1,
	}
	helperCreateEvent(t, s, event)
	existing := &models.SongRequest{
		ID: uuid.NewString(), EventID: event.ID, SingerName: "Host singer",
		Status: models.StatusAccepted, Rank: 100,
	}
	if err := s.CreateRequest(ctx, existing); err != nil {
		t.Fatalf("seed request: %v", err)
	}
	newcomer := &models.SongRequest{
		ID: uuid.NewString(), EventID: event.ID, SingerName: "New singer",
		Status: models.StatusAccepted, Rank: 50,
	}
	planReady := make(chan struct{})
	commitPlan := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(planReady)
		<-commitPlan
		result <- s.CreateRequestAtVersion(ctx, newcomer, 0, []string{newcomer.ID, existing.ID}, 1)
	}()
	<-planReady
	if err := s.ReorderRequestsWithQueueVersion(ctx, event.ID, []string{existing.ID}, 1); err != nil {
		t.Fatalf("host reorder: %v", err)
	}
	close(commitPlan)
	var conflict *store.QueueVersionConflictError
	if err := <-result; !errors.As(err, &conflict) || conflict.Current != 2 {
		t.Fatalf("stale placement error = %v, want version conflict at 2", err)
	}
	if _, err := s.GetRequest(ctx, newcomer.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale newcomer was partially committed: %v", err)
	}
	updated, err := s.GetRequest(ctx, existing.ID)
	if err != nil || updated.Rank != 1 {
		t.Fatalf("host placement = %v, error = %v; want rank 1", updated, err)
	}
}

func TestStore_CreateRequestAtomicRejectsStatusChangedBeforeCommit(t *testing.T) {
	for _, status := range []models.EventStatus{models.EventStatusPaused, models.EventStatusEnded} {
		for _, autoAccept := range []bool{false, true} {
			t.Run(string(status)+map[bool]string{false: "/pending", true: "/accepted"}[autoAccept], func(t *testing.T) {
				s := newLocalStore(t)
				ctx := context.Background()
				event := &models.Event{
					ID: uuid.NewString(), OwnerID: "host-1", JoinCode: "JOIN",
					Status: models.EventStatusActive, QueueVersion: 1,
				}
				helperCreateEvent(t, s, event)
				// Submission has already read the active event and planned its request.
				req := &models.SongRequest{
					ID: uuid.NewString(), EventID: event.ID, SingerName: "Alice",
					Status: models.StatusPending, CreatedAt: time.Now().UTC(),
				}
				if autoAccept {
					req.Status = models.StatusAccepted
				}
				event.Status = status
				if err := s.UpdateEventWithVersion(ctx, event, true); err != nil {
					t.Fatalf("change event status: %v", err)
				}
				if err := s.CreateRequestAtVersion(ctx, req, 1, nil, 1); !errors.Is(err, store.ErrEventNotActive) {
					t.Fatalf("CreateRequestAtVersion error = %v, want inactive event", err)
				}
				if _, err := s.GetRequest(ctx, req.ID); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("request after rejected transaction: %v", err)
				}
				updated, err := s.GetEvent(ctx, event.ID)
				if err != nil {
					t.Fatalf("get event: %v", err)
				}
				if updated.QueueVersion != 2 || updated.Status != status {
					t.Fatalf("event after rejected transaction: status=%s version=%d", updated.Status, updated.QueueVersion)
				}
			})
		}
	}
}

func TestStore_CreateRequestAtomicPendingWithoutSingerLimitChecksStatus(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	event := &models.Event{
		ID: uuid.NewString(), OwnerID: "host-1", JoinCode: "JOIN",
		Status: models.EventStatusPaused, QueueVersion: 2,
	}
	helperCreateEvent(t, s, event)
	req := &models.SongRequest{
		ID: uuid.NewString(), EventID: event.ID, SingerName: "Alice",
		Status: models.StatusPending, CreatedAt: time.Now().UTC(),
	}
	if err := s.CreateRequestAtVersion(ctx, req, 0, nil, 2); !errors.Is(err, store.ErrEventNotActive) {
		t.Fatalf("CreateRequestAtVersion error = %v, want inactive event", err)
	}
	if _, err := s.GetRequest(ctx, req.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("request after rejected transaction: %v", err)
	}
}

func TestStore_CreateRequestAtomicMirrorsEventRequest(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	event := &models.Event{ID: uuid.NewString(), OwnerID: "host-1", JoinCode: "JOIN", Status: models.EventStatusActive}
	helperCreateEvent(t, s, event)
	req := &models.SongRequest{ID: uuid.NewString(), EventID: event.ID, SingerName: "Alice", Status: models.StatusPending}
	if err := s.CreateRequestAtVersion(ctx, req, 0, nil, event.QueueVersion); err != nil {
		t.Fatalf("create request: %v", err)
	}
	shadow, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.tables.eventRequests),
		Key: map[string]types.AttributeValue{
			"event_id": avString(event.ID), "id": avString(req.ID),
		},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil || len(shadow.Item) == 0 {
		t.Fatalf("event-scoped request missing after commit: item=%v error=%v", shadow.Item, err)
	}
	var mirrored requestItem
	if err := unmarshal(shadow.Item, &mirrored); err != nil {
		t.Fatalf("decode event-scoped request: %v", err)
	}
	if mirrored.ID != req.ID || mirrored.EventID != event.ID || mirrored.Status != string(models.StatusPending) {
		t.Fatalf("event-scoped request = %+v", mirrored)
	}
	listed, err := s.listEventRequestsConsistent(ctx, event.ID)
	if err != nil || len(listed) != 1 || listed[0].ID != req.ID {
		t.Fatalf("consistent event list = %v, error = %v", listed, err)
	}
}

func TestStore_CreateRequestAtVersionRollsBackWhenEventRequestWriteFails(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	event := &models.Event{ID: uuid.NewString(), OwnerID: "host-1", JoinCode: "JOIN", Status: models.EventStatusActive}
	helperCreateEvent(t, s, event)
	shadowTable := s.tables.eventRequests
	s.tables.eventRequests += "-missing"
	req := &models.SongRequest{ID: uuid.NewString(), EventID: event.ID, SingerName: "Alice", Status: models.StatusAccepted}
	if err := s.CreateRequestAtVersion(ctx, req, 0, nil, event.QueueVersion); err == nil {
		t.Fatal("request committed despite unavailable event-scoped table")
	}
	s.tables.eventRequests = shadowTable
	if _, err := s.GetRequest(ctx, req.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("primary request after failed transaction: %v", err)
	}
	updated, err := s.GetEvent(ctx, event.ID)
	if err != nil || updated.QueueVersion != event.QueueVersion {
		t.Fatalf("event version after failed transaction: event=%v error=%v", updated, err)
	}
}

func TestStore_CreateRequestMirrorsEventRequest(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	ev := helperCreateEvent(t, s, &models.Event{ID: uuid.NewString(), Status: models.EventStatusActive})
	req := &models.SongRequest{ID: uuid.NewString(), EventID: ev.ID, SingerName: "Alice", Status: models.StatusPending}
	if err := s.CreateRequest(ctx, req); err != nil {
		t.Fatalf("create request: %v", err)
	}
	listed, err := s.listEventRequestsConsistent(ctx, req.EventID)
	if err != nil || len(listed) != 1 || listed[0].ID != req.ID {
		t.Fatalf("consistent event list = %v, error = %v", listed, err)
	}
}

func TestStore_UpdateRequestMirrorsEventRequestOrRollsBack(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	event := &models.Event{ID: uuid.NewString(), OwnerID: "host-1", JoinCode: "JOIN", Status: models.EventStatusActive, QueueVersion: 1}
	helperCreateEvent(t, s, event)
	req := &models.SongRequest{ID: uuid.NewString(), EventID: event.ID, SingerName: "Alice", Status: models.StatusPending}
	if err := s.CreateRequest(ctx, req); err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Status = models.StatusAccepted
	if err := s.UpdateRequest(ctx, req); err != nil {
		t.Fatalf("update request: %v", err)
	}
	listed, err := s.listEventRequestsConsistent(ctx, req.EventID)
	if err != nil || len(listed) != 1 || listed[0].Status != models.StatusAccepted {
		t.Fatalf("consistent event list after update = %v, error = %v", listed, err)
	}
	updatedEvent, err := s.GetEvent(ctx, event.ID)
	if err != nil || updatedEvent.QueueVersion != 2 {
		t.Fatalf("event after status update = %v, error = %v; want version 2", updatedEvent, err)
	}
	shadowTable := s.tables.eventRequests
	s.tables.eventRequests += "-missing"
	req.Status = models.StatusCompleted
	if err := s.UpdateRequest(ctx, req); err == nil {
		t.Fatal("update committed despite unavailable event-scoped table")
	}
	s.tables.eventRequests = shadowTable
	primary, err := s.GetRequest(ctx, req.ID)
	if err != nil || primary.Status != models.StatusAccepted {
		t.Fatalf("primary request after failed update = %v, error = %v", primary, err)
	}
	updatedEvent, err = s.GetEvent(ctx, event.ID)
	if err != nil || updatedEvent.QueueVersion != 2 {
		t.Fatalf("event after failed status update = %v, error = %v; want version 2", updatedEvent, err)
	}
}

func TestStore_DeleteRequestMirrorsEventRequestOrRollsBack(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	event := &models.Event{ID: uuid.NewString(), OwnerID: "host-1", JoinCode: "JOIN", Status: models.EventStatusActive}
	helperCreateEvent(t, s, event)
	req := &models.SongRequest{ID: uuid.NewString(), EventID: event.ID, SingerName: "Alice", Status: models.StatusPending}
	if err := s.CreateRequest(ctx, req); err != nil {
		t.Fatalf("create request: %v", err)
	}
	shadowTable := s.tables.eventRequests
	s.tables.eventRequests += "-missing"
	if err := s.DeleteRequest(ctx, req.ID); err == nil {
		t.Fatal("delete committed despite unavailable event-scoped table")
	}
	s.tables.eventRequests = shadowTable
	if _, err := s.GetRequest(ctx, req.ID); err != nil {
		t.Fatalf("primary request missing after failed delete: %v", err)
	}
	if err := s.DeleteRequest(ctx, req.ID); err != nil {
		t.Fatalf("delete request: %v", err)
	}
	if _, err := s.GetRequest(ctx, req.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("primary request after delete: %v", err)
	}
	listed, err := s.listEventRequestsConsistent(ctx, req.EventID)
	if err != nil || len(listed) != 0 {
		t.Fatalf("consistent event list after delete = %v, error = %v", listed, err)
	}
}

func TestStore_DeleteEventRequestBumpsVersionAtomically(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	event := &models.Event{
		ID: uuid.NewString(), OwnerID: "host-1", JoinCode: "JOIN",
		Status: models.EventStatusActive, QueueVersion: 1,
	}
	helperCreateEvent(t, s, event)
	req := &models.SongRequest{
		ID: uuid.NewString(), EventID: event.ID, SingerName: "Singer", Status: models.StatusPending,
	}
	if err := s.CreateRequest(ctx, req); err != nil {
		t.Fatalf("seed request: %v", err)
	}

	eventsTable := s.tables.events
	s.tables.events += "-missing"
	if err := s.DeleteRequest(ctx, req.ID); err == nil {
		t.Fatal("delete committed without the event version write")
	}
	s.tables.events = eventsTable
	if _, err := s.GetRequest(ctx, req.ID); err != nil {
		t.Fatalf("request missing after failed delete: %v", err)
	}
	if err := s.DeleteRequest(ctx, req.ID); err != nil {
		t.Fatalf("delete request: %v", err)
	}
	if err := s.DeleteRequest(ctx, req.ID); err != nil {
		t.Fatalf("repeat delete: %v", err)
	}
	if _, err := s.GetRequest(ctx, req.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("request remains after delete: %v", err)
	}
	updated, err := s.GetEvent(ctx, event.ID)
	if err != nil || updated.QueueVersion != 2 {
		t.Fatalf("event after delete = %v, error = %v; want version 2", updated, err)
	}
}

func TestStore_DeleteOrphanedEventRequestRecovers(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	event := &models.Event{ID: uuid.NewString(), OwnerID: "host-1", JoinCode: "JOIN", Status: models.EventStatusActive}
	helperCreateEvent(t, s, event)
	req := &models.SongRequest{ID: uuid.NewString(), EventID: event.ID, SingerName: "Singer", Status: models.StatusPending}
	if err := s.CreateRequest(ctx, req); err != nil {
		t.Fatalf("seed request: %v", err)
	}
	if _, err := s.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(s.tables.events), Key: stringKey("id", event.ID),
	}); err != nil {
		t.Fatalf("simulate event deleted before request cleanup: %v", err)
	}
	if err := s.DeleteRequest(ctx, req.ID); err != nil {
		t.Fatalf("recover orphaned request: %v", err)
	}
	if _, err := s.GetRequest(ctx, req.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan remains after cleanup: %v", err)
	}
}

func TestStore_CompatibilityReorderCommitsEventVersion(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	event := &models.Event{
		ID: uuid.NewString(), OwnerID: "host-1", SongbookID: "book-1", JoinCode: "JOIN",
		Status: models.EventStatusActive, QueueVersion: 1,
	}
	helperCreateEvent(t, s, event)
	first := &models.SongRequest{ID: uuid.NewString(), EventID: event.ID, SongbookID: event.SongbookID, SingerName: "First", Status: models.StatusAccepted, Rank: 1}
	second := &models.SongRequest{ID: uuid.NewString(), EventID: event.ID, SongbookID: event.SongbookID, SingerName: "Second", Status: models.StatusAccepted, Rank: 2}
	for _, req := range []*models.SongRequest{first, second} {
		if err := s.CreateRequest(ctx, req); err != nil {
			t.Fatalf("seed request %s: %v", req.ID, err)
		}
	}
	order := []string{second.ID, first.ID}
	eventsTable := s.tables.events
	s.tables.events += "-missing"
	if err := s.ReorderRequests(ctx, event.ID, order); err == nil {
		t.Fatal("reorder committed without event version update")
	}
	s.tables.events = eventsTable
	before, err := s.GetRequest(ctx, first.ID)
	if err != nil || before.Rank != 1 {
		t.Fatalf("first request after failed reorder = %v, error = %v; want rank 1", before, err)
	}
	if err := s.ReorderRequests(ctx, event.ID, order); err != nil {
		t.Fatalf("event reorder: %v", err)
	}
	updated, err := s.GetEvent(ctx, event.ID)
	if err != nil || updated.QueueVersion != 2 {
		t.Fatalf("event after reorder = %v, error = %v; want version 2", updated, err)
	}
	firstAfter, err := s.GetRequest(ctx, first.ID)
	if err != nil || firstAfter.Rank != 2 {
		t.Fatalf("first request after reorder = %v, error = %v; want rank 2", firstAfter, err)
	}
	if err := s.ReorderRequests(ctx, event.ID, []string{first.ID, first.ID}); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("duplicate reorder IDs = %v, want os.ErrInvalid", err)
	}
	if err := s.BeginEventDeletion(ctx, event.ID); err != nil {
		t.Fatalf("BeginEventDeletion: %v", err)
	}
	if err := s.ReorderRequests(ctx, event.ID, order); !errors.Is(err, store.ErrEventDeleting) {
		t.Fatalf("reorder on deleting event = %v, want ErrEventDeleting", err)
	}
}

func TestStore_IdempotentMutationsPreserveWriteInAndRejectDeletingEvent(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	event := &models.Event{
		ID:           uuid.NewString(),
		OwnerID:      "host-1",
		Status:       models.EventStatusActive,
		JoinCode:     "WRIN",
		QueueVersion: 1,
	}
	helperCreateEvent(t, s, event)

	r1 := &models.SongRequest{
		ID:         uuid.NewString(),
		EventID:    event.ID,
		SingerName: "Alice",
		SongTitle:  "Custom Write-In",
		IsWriteIn:  true,
		Status:     models.StatusPending,
		Rank:       1,
		CreatedAt:  time.Now().UTC(),
	}
	r2 := &models.SongRequest{
		ID:         uuid.NewString(),
		EventID:    event.ID,
		SingerName: "Bob",
		SongTitle:  "Catalog Track",
		IsWriteIn:  false,
		Status:     models.StatusAccepted,
		Rank:       2,
		CreatedAt:  time.Now().UTC(),
	}
	for _, r := range []*models.SongRequest{r1, r2} {
		if err := s.CreateRequest(ctx, r); err != nil {
			t.Fatalf("seed request: %v", err)
		}
	}

	// 1. Single idempotent update must preserve IsWriteIn.
	r1.Status = models.StatusAccepted
	ver1 := int64(1)
	if _, err := s.UpdateRequestWithIdempotency(ctx, r1, &ver1, uuid.NewString(), event.OwnerID, "events/"+event.ID+"/requests", "accept-writein"); err != nil {
		t.Fatalf("UpdateRequestWithIdempotency: %v", err)
	}
	reloaded, err := s.GetRequest(ctx, r1.ID)
	if err != nil {
		t.Fatalf("GetRequest after single idempotent update: %v", err)
	}
	if !reloaded.IsWriteIn {
		t.Fatal("single idempotent update silently dropped IsWriteIn=true")
	}

	// 2. Idempotent update+reorder must preserve IsWriteIn.
	reloaded.Status = models.StatusUpNext
	ver2 := int64(2)
	if _, err := s.UpdateRequestAndReorderWithIdempotency(ctx, reloaded, []string{r2.ID, reloaded.ID}, &ver2, uuid.NewString(), event.OwnerID, "events/"+event.ID+"/requests", "upnext-writein"); err != nil {
		t.Fatalf("UpdateRequestAndReorderWithIdempotency: %v", err)
	}
	reloaded, err = s.GetRequest(ctx, r1.ID)
	if err != nil {
		t.Fatalf("GetRequest after idempotent update+reorder: %v", err)
	}
	if !reloaded.IsWriteIn {
		t.Fatal("idempotent update+reorder silently dropped IsWriteIn=true")
	}

	// 3. Mark event as deleting and verify all idempotent queue mutations fail with ErrEventDeleting.
	if err := s.BeginEventDeletion(ctx, event.ID); err != nil {
		t.Fatalf("BeginEventDeletion: %v", err)
	}

	ver3 := int64(3)
	reloaded.Status = models.StatusCompleted
	if _, err := s.UpdateRequestWithIdempotency(ctx, reloaded, &ver3, uuid.NewString(), event.OwnerID, "events/"+event.ID+"/requests", "del-single"); !errors.Is(err, store.ErrEventDeleting) {
		t.Fatalf("UpdateRequestWithIdempotency on deleting event = %v, want ErrEventDeleting", err)
	}
	if _, err := s.UpdateRequestAndReorderWithIdempotency(ctx, reloaded, []string{reloaded.ID, r2.ID}, &ver3, uuid.NewString(), event.OwnerID, "events/"+event.ID+"/requests", "del-update-reorder"); !errors.Is(err, store.ErrEventDeleting) {
		t.Fatalf("UpdateRequestAndReorderWithIdempotency on deleting event = %v, want ErrEventDeleting", err)
	}
	if _, err := s.ReorderRequestsWithIdempotency(ctx, event.ID, []string{reloaded.ID, r2.ID}, &ver3, uuid.NewString(), event.OwnerID, "events/"+event.ID+"/requests/reorder", "del-reorder"); !errors.Is(err, store.ErrEventDeleting) {
		t.Fatalf("ReorderRequestsWithIdempotency on deleting event = %v, want ErrEventDeleting", err)
	}
	if _, err := s.ReorderRequestsWithIdempotency(ctx, event.ID, nil, &ver3, uuid.NewString(), event.OwnerID, "events/"+event.ID+"/requests/reorder", "del-empty-reorder"); !errors.Is(err, store.ErrEventDeleting) {
		t.Fatalf("empty ReorderRequestsWithIdempotency on deleting event = %v, want ErrEventDeleting", err)
	}
}
