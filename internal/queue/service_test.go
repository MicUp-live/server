package queue

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type failingAdmissionReadStore struct {
	store.RequestStore
	readErr     error
	createCalls int
}

type conflictingAdmissionStore struct {
	store.RequestStore
	events         *admissionEventStore
	requests       []*models.SongRequest
	commitCalls    int
	queueReads     int
	versionsSeen   []int64
	committed      *models.SongRequest
	firstWinner    *models.SongRequest
	alwaysConflict bool
}

func (s *conflictingAdmissionStore) ListRequestsByEvent(context.Context, string) ([]*models.SongRequest, error) {
	s.queueReads++
	return append([]*models.SongRequest(nil), s.requests...), nil
}

func (s *conflictingAdmissionStore) CreateRequestAtVersion(_ context.Context, req *models.SongRequest, _ int, _ []string, expectedVersion int64) error {
	s.commitCalls++
	s.versionsSeen = append(s.versionsSeen, expectedVersion)
	if s.alwaysConflict {
		s.events.event.QueueVersion++
		return &store.QueueVersionConflictError{Current: s.events.event.QueueVersion}
	}
	if s.commitCalls == 1 {
		s.requests = append(s.requests, s.firstWinner)
		s.events.event.QueueVersion++
		return &store.QueueVersionConflictError{Current: s.events.event.QueueVersion}
	}
	s.committed = req
	return nil
}

func TestSubmitRequestStopsAfterBoundedConflicts(t *testing.T) {
	events := &admissionEventStore{event: &models.Event{
		ID: "event-1", Status: models.EventStatusActive, QueueVersion: 1,
	}}
	requests := &conflictingAdmissionStore{events: events, alwaysConflict: true}
	created, err := NewService(requests, events, []byte("test-secret")).SubmitRequest(context.Background(), SubmitRequestCmd{
		EventID: "event-1", SingerName: "Singer", SongTitle: "Song",
	})
	if !errors.Is(err, store.ErrAdmissionConflict) || created != nil || requests.committed != nil {
		t.Fatalf("exhausted admission = (%v, %v), committed=%v", created, err, requests.committed)
	}
	if requests.commitCalls != 3 || events.reads != 3 || requests.queueReads != 3 {
		t.Fatalf("attempts=%d event reads=%d queue reads=%d, want 3 each", requests.commitCalls, events.reads, requests.queueReads)
	}
}

func TestSubmitRequestRecomputesAfterAdmissionConflict(t *testing.T) {
	for _, tc := range []struct {
		name       string
		autoAccept bool
		winner     *models.SongRequest
		wantErr    bool
	}{
		{
			name: "pending cooldown", winner: &models.SongRequest{
				SingerName: "Singer", Status: models.StatusPending, CreatedAt: time.Now().UTC(),
			}, wantErr: true,
		},
		{
			name: "accepted singer turn", autoAccept: true, winner: &models.SongRequest{
				SingerName: "Singer", Status: models.StatusCompleted, CreatedAt: time.Now().UTC(),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := &admissionEventStore{event: &models.Event{
				ID: "event-1", Status: models.EventStatusActive, QueueVersion: 1,
				AutoAccept: tc.autoAccept, SubmissionDelay: 60,
			}}
			requests := &conflictingAdmissionStore{events: events, firstWinner: tc.winner}
			created, err := NewService(requests, events, []byte("test-secret")).SubmitRequest(context.Background(), SubmitRequestCmd{
				EventID: "event-1", SingerName: "Singer", SongTitle: "Song",
			})
			if tc.wantErr {
				var cooldown *ErrCooldownActive
				if !errors.As(err, &cooldown) || created != nil || requests.commitCalls != 1 {
					t.Fatalf("retry after pending winner = (%v, %v), commits=%d; want cooldown without second commit", created, err, requests.commitCalls)
				}
			} else if err != nil || created == nil || created.SingerTurn != 2 || requests.commitCalls != 2 {
				t.Fatalf("retry after completed winner = (%v, %v), commits=%d; want singer turn 2", created, err, requests.commitCalls)
			}
			if events.reads != 2 || requests.queueReads != 2 {
				t.Fatalf("retry reads: event=%d queue=%d; want 2 each", events.reads, requests.queueReads)
			}
			if !tc.wantErr && (len(requests.versionsSeen) != 2 || requests.versionsSeen[0] != 1 || requests.versionsSeen[1] != 2) {
				t.Fatalf("commit versions = %v, want [1 2]", requests.versionsSeen)
			}
		})
	}
}

func (s *failingAdmissionReadStore) ListRequestsByEvent(context.Context, string) ([]*models.SongRequest, error) {
	return nil, s.readErr
}

func (s *failingAdmissionReadStore) CreateRequestAtVersion(context.Context, *models.SongRequest, int, []string, int64) error {
	s.createCalls++
	return nil
}

type admissionEventStore struct {
	store.EventStore
	event   *models.Event
	readErr error
	reads   int
}

func (s *admissionEventStore) GetEvent(context.Context, string) (*models.Event, error) {
	s.reads++
	return s.event, s.readErr
}

func TestSubmitRequestEventReadFailureDoesNotCommit(t *testing.T) {
	readErr := errors.New("event read unavailable")
	requests := &failingAdmissionReadStore{}
	events := &admissionEventStore{readErr: readErr}
	created, err := NewService(requests, events, []byte("test-secret")).SubmitRequest(context.Background(), SubmitRequestCmd{
		EventID: "event-1", SingerName: "Singer", SongTitle: "Song",
	})
	if !errors.Is(err, readErr) || created != nil || requests.createCalls != 0 {
		t.Fatalf("SubmitRequest = (%v, %v) with %d commits after event read failure", created, err, requests.createCalls)
	}
}

type statusRaceRequestStore struct {
	store.RequestStore
	events *admissionEventStore
	status models.EventStatus
}

func (s *statusRaceRequestStore) ListRequestsByEvent(context.Context, string) ([]*models.SongRequest, error) {
	return nil, nil
}

func (s *statusRaceRequestStore) CreateRequestAtVersion(context.Context, *models.SongRequest, int, []string, int64) error {
	s.events.event.Status = s.status
	return store.ErrEventNotActive
}

func TestSubmitRequestMapsStatusRace(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status models.EventStatus
		want   error
	}{
		{name: "paused", status: models.EventStatusPaused, want: ErrShowPaused},
		{name: "ended", status: models.EventStatusEnded, want: ErrShowEnded},
	} {
		for _, autoAccept := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/pending", true: "/accepted"}[autoAccept], func(t *testing.T) {
				events := &admissionEventStore{event: &models.Event{
					ID: "event-1", Status: models.EventStatusActive, AutoAccept: autoAccept,
				}}
				requests := &statusRaceRequestStore{events: events, status: tc.status}
				created, err := NewService(requests, events, []byte("test-secret")).SubmitRequest(context.Background(), SubmitRequestCmd{
					EventID: "event-1", SingerName: "Singer", SongTitle: "Song",
				})
				if !errors.Is(err, tc.want) || created != nil {
					t.Fatalf("SubmitRequest = (%v, %v), want (nil, %v)", created, err, tc.want)
				}
				if events.reads != 2 {
					t.Fatalf("event reads = %d, want initial read and status reload", events.reads)
				}
			})
		}
	}
}

func TestSubmitRequestQueueReadFailureDoesNotCommit(t *testing.T) {
	for _, autoAccept := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "auto_accept"}[autoAccept], func(t *testing.T) {
			readErr := errors.New("queue read unavailable")
			requests := &failingAdmissionReadStore{readErr: readErr}
			events := &admissionEventStore{event: &models.Event{
				ID:         "event-1",
				Status:     models.EventStatusActive,
				AutoAccept: autoAccept,
			}}
			service := NewService(requests, events, []byte("test-secret"))

			created, err := service.SubmitRequest(context.Background(), SubmitRequestCmd{
				EventID: "event-1", SingerName: "Singer", SongTitle: "Song",
			})
			if !errors.Is(err, readErr) {
				t.Fatalf("SubmitRequest error = %v, want queue read failure", err)
			}
			if created != nil || requests.createCalls != 0 {
				t.Fatalf("SubmitRequest created %v with %d commits after read failure", created, requests.createCalls)
			}
		})
	}
}

func TestTruncateUTF8PreservesRuneBoundaries(t *testing.T) {
	// 65 4-byte emoji runes: byte slicing at 60 would split a rune and produce invalid UTF-8.
	input := strings.Repeat("🎤", 65)
	got := truncateUTF8(input, 60)
	if len([]rune(got)) != 60 {
		t.Fatalf("rune len = %d, want 60", len([]rune(got)))
	}
	if !utf8.ValidString(got) {
		t.Fatal("truncated string is not valid UTF-8")
	}
	if truncateUTF8("short", 60) != "short" {
		t.Fatal("short string was modified")
	}
}

type updateLifecycleRequestStore struct {
	store.RequestStore
	req *models.SongRequest
}

func (s *updateLifecycleRequestStore) GetRequest(_ context.Context, id string) (*models.SongRequest, error) {
	if s.req != nil && s.req.ID == id {
		cp := *s.req
		return &cp, nil
	}
	return nil, errors.New("not found")
}

func (s *updateLifecycleRequestStore) UpdateRequest(_ context.Context, req *models.SongRequest) error {
	cp := *req
	s.req = &cp
	return nil
}

func TestUpdateEventRequest_CompletedAtLifecycle(t *testing.T) {
	reqStore := &updateLifecycleRequestStore{
		req: &models.SongRequest{
			ID:         "req-1",
			EventID:    "ev-1",
			SingerName: "Alice",
			Status:     models.StatusAccepted,
			Rank:       1,
			CreatedAt:  time.Now().UTC().Add(-10 * time.Minute),
		},
	}
	svc := NewService(reqStore, nil, []byte("test-secret"))

	// 1. Mark completed -> sets CompletedAt
	completed := models.StatusCompleted
	updated, _, err := svc.UpdateEventRequest(context.Background(), UpdateEventRequestCmd{
		EventID:   "ev-1",
		RequestID: "req-1",
		Update:    models.RequestUpdate{Status: &completed},
	})
	if err != nil {
		t.Fatalf("complete request: %v", err)
	}
	if updated.CompletedAt == nil || updated.CompletedAt.IsZero() {
		t.Fatal("expected CompletedAt to be set when transitioning to completed")
	}
	origCompletedAt := *updated.CompletedAt

	// 2. Edit notes on completed request -> preserves original CompletedAt
	notes := "Great performance!"
	edited, _, err := svc.UpdateEventRequest(context.Background(), UpdateEventRequestCmd{
		EventID:   "ev-1",
		RequestID: "req-1",
		Update:    models.RequestUpdate{Notes: &notes},
	})
	if err != nil {
		t.Fatalf("edit completed request: %v", err)
	}
	if edited.CompletedAt == nil || !edited.CompletedAt.Equal(origCompletedAt) {
		t.Fatalf("expected CompletedAt to remain %v after editing notes, got %v", origCompletedAt, edited.CompletedAt)
	}

	// 3. Transition back out of completed -> clears CompletedAt
	accepted := models.StatusAccepted
	reopened, _, err := svc.UpdateEventRequest(context.Background(), UpdateEventRequestCmd{
		EventID:   "ev-1",
		RequestID: "req-1",
		Update:    models.RequestUpdate{Status: &accepted},
	})
	if err != nil {
		t.Fatalf("reopen completed request: %v", err)
	}
	if reopened.CompletedAt != nil {
		t.Fatalf("expected CompletedAt to be cleared when leaving completed status, got %v", reopened.CompletedAt)
	}

	// 4. Legacy completed request (CompletedAt == nil): editing metadata preserves historical UpdatedAt as CompletedAt
	historicalCompletedAt := time.Date(2026, 11, 24, 20, 30, 0, 0, time.UTC)
	reqStore.req = &models.SongRequest{
		ID:          "req-legacy",
		EventID:     "ev-1",
		SingerName:  "Bob",
		Status:      models.StatusCompleted,
		Rank:        1,
		CreatedAt:   historicalCompletedAt.Add(-5 * time.Minute),
		UpdatedAt:   historicalCompletedAt,
		CompletedAt: nil,
	}
	legacyNotes := "Fixed typo"
	legacyEdited, _, err := svc.UpdateEventRequest(context.Background(), UpdateEventRequestCmd{
		EventID:   "ev-1",
		RequestID: "req-legacy",
		Update:    models.RequestUpdate{Notes: &legacyNotes},
	})
	if err != nil {
		t.Fatalf("edit legacy completed request: %v", err)
	}
	if legacyEdited.CompletedAt == nil || !legacyEdited.CompletedAt.Equal(historicalCompletedAt) {
		t.Fatalf("expected legacy CompletedAt to preserve historical UpdatedAt %v, got %v", historicalCompletedAt, legacyEdited.CompletedAt)
	}
}
