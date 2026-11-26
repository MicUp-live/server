package dynamo

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"micup.live/server/internal/models"
	"micup.live/server/internal/queue"
	"micup.live/server/internal/store"
)

// admissionBarrierStore lets both submissions read the same queue before either
// can commit. Later retry reads pass through without blocking.
type admissionBarrierStore struct {
	*Store
	mu    sync.Mutex
	reads int
	ready chan struct{}
}

// admissionSnapshotStore pauses the first submission after its strongly
// consistent queue read, so another writer can change the event before commit.
type admissionSnapshotStore struct {
	*Store
	mu     sync.Mutex
	paused bool
	reads  int
	read   chan struct{}
	resume chan struct{}
}

func (s *admissionSnapshotStore) ListRequestsByEvent(ctx context.Context, eventID string) ([]*models.SongRequest, error) {
	requests, err := s.Store.ListRequestsByEvent(ctx, eventID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.reads++
	pause := !s.paused
	s.paused = true
	s.mu.Unlock()
	if pause {
		close(s.read)
		select {
		case <-s.resume:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return requests, nil
}

func (s *admissionBarrierStore) ListRequestsByEvent(ctx context.Context, eventID string) ([]*models.SongRequest, error) {
	requests, err := s.Store.ListRequestsByEvent(ctx, eventID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.reads++
	block := s.reads <= 2
	if s.reads == 2 {
		close(s.ready)
	}
	s.mu.Unlock()
	if block {
		select {
		case <-s.ready:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return requests, nil
}

func TestSubmitRequestConcurrentSameSingerRecomputesWithDynamo(t *testing.T) {
	for _, tc := range []struct {
		name         string
		autoAccept   bool
		maxActive    int
		delaySeconds int
		wantCooldown bool
	}{
		{name: "pending cooldown without slots", delaySeconds: 60, wantCooldown: true},
		{name: "accepted with one singer slot", autoAccept: true, maxActive: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := newLocalStore(t)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			event := &models.Event{
				ID: uuid.NewString(), OwnerID: "host-1", JoinCode: "JOIN",
				Status: models.EventStatusActive, QueueVersion: 1,
				AutoAccept: tc.autoAccept, MaxActivePerSinger: tc.maxActive,
				SubmissionDelay: tc.delaySeconds,
			}
			helperCreateEvent(t, base, event)
			requests := &admissionBarrierStore{Store: base, ready: make(chan struct{})}
			service := queue.NewService(requests, base, []byte("test-secret"))
			type outcome struct {
				req *models.SongRequest
				err error
			}
			outcomes := make(chan outcome, 2)
			for range 2 {
				go func() {
					req, err := service.SubmitRequest(ctx, queue.SubmitRequestCmd{
						EventID: event.ID, SingerName: "Singer", SongTitle: "Song",
					})
					outcomes <- outcome{req: req, err: err}
				}()
			}
			succeeded, denied := 0, 0
			for range 2 {
				result := <-outcomes
				if result.err == nil && result.req != nil {
					succeeded++
					continue
				}
				if tc.wantCooldown {
					var cooldown *queue.ErrCooldownActive
					if !errors.As(result.err, &cooldown) {
						t.Fatalf("losing submission = (%v, %v), want cooldown", result.req, result.err)
					}
				} else if !errors.Is(result.err, store.ErrSingerLimitExceeded) {
					t.Fatalf("losing submission = (%v, %v), want singer limit", result.req, result.err)
				}
				denied++
			}
			if succeeded != 1 || denied != 1 {
				t.Fatalf("successes=%d denials=%d, want one each", succeeded, denied)
			}
			listed, err := base.ListRequestsByEvent(ctx, event.ID)
			if err != nil || len(listed) != 1 {
				t.Fatalf("event requests=%v, error=%v; want one", listed, err)
			}
			updated, err := base.GetEvent(ctx, event.ID)
			if err != nil || updated.QueueVersion != 2 {
				t.Fatalf("event after submissions=%v, error=%v; want version 2", updated, err)
			}
		})
	}
}

func TestSubmitRequestRejectsPauseOrEndAfterQueueReadWithDynamo(t *testing.T) {
	for _, status := range []models.EventStatus{models.EventStatusPaused, models.EventStatusEnded} {
		for _, autoAccept := range []bool{false, true} {
			name := string(status) + "/pending"
			if autoAccept {
				name = string(status) + "/accepted"
			}
			t.Run(name, func(t *testing.T) {
				base := newLocalStore(t)
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				event := &models.Event{
					ID: uuid.NewString(), OwnerID: "host-1", JoinCode: "JOIN",
					Status: models.EventStatusActive, QueueVersion: 1, AutoAccept: autoAccept,
				}
				helperCreateEvent(t, base, event)
				requests := &admissionSnapshotStore{Store: base, read: make(chan struct{}), resume: make(chan struct{})}
				service := queue.NewService(requests, base, []byte("test-secret"))
				type outcome struct {
					req *models.SongRequest
					err error
				}
				result := make(chan outcome, 1)
				go func() {
					req, err := service.SubmitRequest(ctx, queue.SubmitRequestCmd{
						EventID: event.ID, SingerName: "Singer", SongTitle: "Song",
					})
					result <- outcome{req: req, err: err}
				}()
				select {
				case <-requests.read:
				case <-ctx.Done():
					t.Fatalf("submission did not read queue: %v", ctx.Err())
				}
				event.Status = status
				if err := base.UpdateEventWithVersion(ctx, event, true); err != nil {
					close(requests.resume)
					t.Fatalf("change event status: %v", err)
				}
				close(requests.resume)
				got := <-result
				want := queue.ErrShowPaused
				if status == models.EventStatusEnded {
					want = queue.ErrShowEnded
				}
				if got.req != nil || !errors.Is(got.err, want) {
					t.Fatalf("submission after %s = (%v, %v), want (nil, %v)", status, got.req, got.err, want)
				}
				listed, err := base.ListRequestsByEvent(ctx, event.ID)
				if err != nil || len(listed) != 0 {
					t.Fatalf("requests after rejected admission = %v, error = %v", listed, err)
				}
				updated, err := base.GetEvent(ctx, event.ID)
				if err != nil || updated.Status != status || updated.QueueVersion != 2 {
					t.Fatalf("event after rejected admission = %v, error = %v", updated, err)
				}
			})
		}
	}
}

func TestSubmitRequestReplansAfterHostReorderWithDynamo(t *testing.T) {
	base := newLocalStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	event := &models.Event{
		ID: uuid.NewString(), OwnerID: "host-1", JoinCode: "JOIN",
		Status: models.EventStatusActive, QueueVersion: 1,
		AutoAccept: true, RotationMode: models.RotationModeFCFS,
	}
	helperCreateEvent(t, base, event)
	first := &models.SongRequest{ID: uuid.NewString(), EventID: event.ID, SingerName: "First", Status: models.StatusAccepted, Rank: 1}
	second := &models.SongRequest{ID: uuid.NewString(), EventID: event.ID, SingerName: "Second", Status: models.StatusAccepted, Rank: 2}
	for _, req := range []*models.SongRequest{first, second} {
		if err := base.CreateRequest(ctx, req); err != nil {
			t.Fatalf("seed request: %v", err)
		}
	}
	requests := &admissionSnapshotStore{Store: base, read: make(chan struct{}), resume: make(chan struct{})}
	service := queue.NewService(requests, base, []byte("test-secret"))
	type outcome struct {
		req *models.SongRequest
		err error
	}
	result := make(chan outcome, 1)
	go func() {
		req, err := service.SubmitRequest(ctx, queue.SubmitRequestCmd{
			EventID: event.ID, SingerName: "Newcomer", SongTitle: "Song",
		})
		result <- outcome{req: req, err: err}
	}()
	select {
	case <-requests.read:
	case <-ctx.Done():
		t.Fatalf("submission did not read queue: %v", ctx.Err())
	}
	if err := base.ReorderRequestsWithQueueVersion(ctx, event.ID, []string{second.ID, first.ID}, 1); err != nil {
		close(requests.resume)
		t.Fatalf("host reorder: %v", err)
	}
	close(requests.resume)
	got := <-result
	if got.err != nil || got.req == nil {
		t.Fatalf("submission after host reorder = (%v, %v)", got.req, got.err)
	}
	requests.mu.Lock()
	reads := requests.reads
	requests.mu.Unlock()
	if reads != 2 {
		t.Fatalf("queue reads = %d, want initial read and fresh retry read", reads)
	}
	listed, err := base.ListRequestsByEvent(ctx, event.ID)
	if err != nil || len(listed) != 3 {
		t.Fatalf("requests after retry = %v, error = %v; want three", listed, err)
	}
	byID := make(map[string]*models.SongRequest, len(listed))
	for _, req := range listed {
		byID[req.ID] = req
	}
	if byID[second.ID] == nil || byID[first.ID] == nil || byID[got.req.ID] == nil {
		t.Fatalf("final queue is missing an expected request: %v", listed)
	}
	if byID[second.ID].Rank != 1 || byID[first.ID].Rank != 2 || byID[got.req.ID].Rank <= 2 {
		t.Fatalf("reorder was not preserved in final placement: second=%d first=%d newcomer=%d",
			byID[second.ID].Rank, byID[first.ID].Rank, byID[got.req.ID].Rank)
	}
	updated, err := base.GetEvent(ctx, event.ID)
	if err != nil || updated.QueueVersion != 3 {
		t.Fatalf("event after reorder and admission = %v, error = %v; want version 3", updated, err)
	}
}
