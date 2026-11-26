package event

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type fakeEventStore struct {
	store.EventStore
	events map[string]*models.Event
}

func newFakeEventStore() *fakeEventStore {
	return &fakeEventStore{events: make(map[string]*models.Event)}
}

func (s *fakeEventStore) CreateEvent(_ context.Context, ev *models.Event) error {
	cp := *ev
	s.events[ev.ID] = &cp
	return nil
}

func (s *fakeEventStore) GetEvent(_ context.Context, id string) (*models.Event, error) {
	ev, ok := s.events[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	cp := *ev
	return &cp, nil
}

func (s *fakeEventStore) GetEventByJoinCode(_ context.Context, code string) (*models.Event, error) {
	for _, ev := range s.events {
		if ev.JoinCode == code {
			cp := *ev
			return &cp, nil
		}
	}
	return nil, os.ErrNotExist
}

func (s *fakeEventStore) ListEventsByOwner(_ context.Context, ownerID string) ([]*models.Event, error) {
	list := make([]*models.Event, 0, len(s.events))
	for _, ev := range s.events {
		if ev.OwnerID == ownerID {
			cp := *ev
			list = append(list, &cp)
		}
	}
	return list, nil
}

func (s *fakeEventStore) ListEventsBySongbook(_ context.Context, _ string) ([]*models.Event, error) {
	return nil, nil
}

func (s *fakeEventStore) UpdateEvent(ctx context.Context, ev *models.Event) error {
	return s.UpdateEventWithVersion(ctx, ev, false)
}

func (s *fakeEventStore) UpdateEventWithVersion(_ context.Context, ev *models.Event, bump bool) error {
	current, ok := s.events[ev.ID]
	if !ok {
		return os.ErrNotExist
	}
	if current.QueueVersion != ev.QueueVersion || current.MetadataVersion != ev.MetadataVersion || current.Restarting {
		return &store.QueueVersionConflictError{Current: current.QueueVersion, Expected: ev.QueueVersion}
	}
	ev.MetadataVersion++

	if bump {
		ev.QueueVersion++
	}
	cp := *ev
	s.events[ev.ID] = &cp
	return nil
}

func (s *fakeEventStore) DeleteEvent(_ context.Context, id string) error {
	delete(s.events, id)
	return nil
}

func (s *fakeEventStore) BeginEventDeletion(_ context.Context, _ string) error {
	return nil
}

func (s *fakeEventStore) TransitionQueueRotation(_ context.Context, _ *models.Event, _ []*models.SongRequest) error {
	return ErrFailedTransition
}

func (s *fakeEventStore) BeginQueueRestart(_ context.Context, _ *models.Event) error {
	return errors.New("restart unsupported in fake store")
}

type fakeSongbookStore struct {
	store.SongbookStore
	books map[string]*models.Songbook
}

func (s *fakeSongbookStore) CreateSongbook(_ context.Context, _ *models.Songbook) error { return nil }
func (s *fakeSongbookStore) UpdateSongbook(_ context.Context, _ *models.Songbook) error { return nil }
func (s *fakeSongbookStore) DeleteSongbook(_ context.Context, _ string) error           { return nil }
func (s *fakeSongbookStore) GetSongbook(_ context.Context, id string) (*models.Songbook, error) {
	b, ok := s.books[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	cp := *b
	return &cp, nil
}
func (s *fakeSongbookStore) GetPublicSongbook(_ context.Context, _ string) (*models.Songbook, error) {
	return nil, nil
}
func (s *fakeSongbookStore) GetSongbookBySlug(_ context.Context, _ string) (*models.Songbook, error) {
	return nil, nil
}
func (s *fakeSongbookStore) ListSongbooks(_ context.Context) ([]*models.Songbook, error) {
	return nil, nil
}
func (s *fakeSongbookStore) ListSongbooksByOwner(_ context.Context, _ string) ([]*models.Songbook, error) {
	return nil, nil
}
func (s *fakeSongbookStore) NextCatalogGeneration(_ context.Context, _ string) (int64, error) {
	return 1, nil
}
func (s *fakeSongbookStore) BeginSongbookDeletion(_ context.Context, id string) (*models.Songbook, error) {
	return &models.Songbook{ID: id, Deleting: true}, nil
}

type rotationRequestStore struct {
	store.RequestStore
	requests []*models.SongRequest
	writes   int
}

func (s *rotationRequestStore) ListRequestsByEvent(context.Context, string) ([]*models.SongRequest, error) {
	return s.requests, nil
}

func (s *rotationRequestStore) UpdateRequest(context.Context, *models.SongRequest) error {
	s.writes++
	return nil
}

func (s *rotationRequestStore) SetRequestsTTLByEvent(context.Context, string, time.Time) error {
	return errors.New("retention unsupported in fake store")
}

func TestUpdateEventRequiresAtomicRotationTransition(t *testing.T) {
	ctx := context.Background()
	events := newFakeEventStore()
	event := &models.Event{ID: "event-1", Status: models.EventStatusActive,
		RotationMode: models.RotationModeRoundRobin, CurrentRound: 1, QueueVersion: 3}
	if err := events.CreateEvent(ctx, event); err != nil {
		t.Fatalf("create event: %v", err)
	}
	requests := &rotationRequestStore{requests: []*models.SongRequest{{
		ID: "request-1", EventID: event.ID, SingerName: "Singer", Status: models.StatusAccepted,
		Rank: 1, Round: 1, CreatedAt: time.Now().UTC(),
	}}}
	svc := NewService(events, nil, requests, 0)
	mode := models.RotationModeFairPriority
	updated, err := svc.UpdateEvent(ctx, event.ID, models.EventUpdate{RotationMode: &mode})
	if err == nil || updated != nil {
		t.Fatalf("transition without atomic store = (%v, %v), want error without event", updated, err)
	}
	saved, err := events.GetEvent(ctx, event.ID)
	if err != nil {
		t.Fatalf("get event: %v", err)
	}
	if saved.RotationMode != models.RotationModeRoundRobin || saved.QueueVersion != 3 || requests.writes != 0 {
		t.Fatalf("failed transition changed event or queue: event=%+v request writes=%d", saved, requests.writes)
	}
}

func TestEventService_Lifecycle(t *testing.T) {
	evStore := newFakeEventStore()
	sbStore := &fakeSongbookStore{books: map[string]*models.Songbook{
		"sb-1": {ID: "sb-1", OwnerID: "user-1", Name: "Host Book"},
	}}
	svc := NewService(evStore, sbStore, nil, 7*24*time.Hour)
	ctx := context.Background()

	// 1. Create Event
	ev, err := svc.CreateEvent(ctx, "user-1", models.EventCreate{
		SongbookID: "sb-1",
		Name:       "Rock Friday",
	})
	if err != nil {
		t.Fatalf("unexpected error creating event: %v", err)
	}
	if ev.Status != models.EventStatusActive || ev.QueueVersion != 1 || ev.JoinCode == "" || len(ev.KioskPIN) != 4 {
		t.Fatalf("unexpected created event: %+v", ev)
	}

	// 2. Verify Kiosk PIN
	res, err := svc.VerifyKioskPIN(ctx, ev.ID, ev.KioskPIN)
	if err != nil || res != PINResultSuccess {
		t.Fatalf("expected generated kiosk PIN %q to verify, got res=%v, err=%v", ev.KioskPIN, res, err)
	}

	// 3. Pause and Resume
	paused, err := svc.PauseEvent(ctx, ev.ID)
	if err != nil || paused.Status != models.EventStatusPaused {
		t.Fatalf("expected paused event, got status=%v, err=%v", paused.Status, err)
	}
	resumed, err := svc.ResumeEvent(ctx, ev.ID)
	if err != nil || resumed.Status != models.EventStatusActive {
		t.Fatalf("expected active event, got status=%v, err=%v", resumed.Status, err)
	}

	// 4. Rotate OpenKJ Key
	oldKey := ev.OpenKJKey
	rotated, err := svc.RotateOpenKJKey(ctx, ev.ID)
	if err != nil || rotated.OpenKJKey == oldKey {
		t.Fatalf("expected rotated key, got %s vs %s", rotated.OpenKJKey, oldKey)
	}

	// 5. End show sets TTL
	endedStatus := models.EventStatusEnded
	ended, err := svc.UpdateEvent(ctx, ev.ID, models.EventUpdate{Status: &endedStatus})
	if err != nil || ended.Status != models.EventStatusEnded || ended.TTL == 0 {
		t.Fatalf("expected ended event with TTL, got status=%v, TTL=%d", ended.Status, ended.TTL)
	}

	// 6. Restart without clearing the queue resets the event TTL.
	restarted, err := svc.RestartEvent(ctx, ev.ID, false)
	if err != nil || restarted.Status != models.EventStatusActive || restarted.TTL != 0 {
		t.Fatalf("expected restarted active event with 0 TTL, got %+v", restarted)
	}
}

func TestRestartEventRequiresAtomicStoreForClearQueue(t *testing.T) {
	ctx := context.Background()
	events := newFakeEventStore()
	if err := events.CreateEvent(ctx, &models.Event{ID: "show", Status: models.EventStatusEnded, QueueVersion: 4}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(events, nil, &rotationRequestStore{}, 0)
	got, err := svc.RestartEvent(ctx, "show", true)
	if err == nil || got != nil {
		t.Fatalf("restart with weak store = (%v, %v), want error and no event", got, err)
	}
	saved, err := events.GetEvent(ctx, "show")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != models.EventStatusEnded || saved.QueueVersion != 4 {
		t.Fatalf("weak restart changed event: %+v", saved)
	}
}

type restartConflictStore struct {
	*fakeEventStore
	beginCalls int
}

func (s *restartConflictStore) BeginQueueRestart(_ context.Context, ev *models.Event) error {
	s.beginCalls++
	current := s.events[ev.ID]
	if s.beginCalls == 1 {
		current.QueueVersion++ // another writer wins before the first begin
		return &store.QueueVersionConflictError{Current: current.QueueVersion}
	}
	if current.QueueVersion != ev.QueueVersion {
		return errors.New("begin did not reload event version")
	}
	current.Restarting = true
	current.Status = models.EventStatusEnded
	current.QueueVersion++
	return nil
}

func (s *restartConflictStore) ArchiveRestartBatch(context.Context, string, int64, []*models.SongRequest) error {
	return errors.New("unexpected archive batch")
}

func (s *restartConflictStore) ListRestartSlots(context.Context, string) ([]string, error) {
	return nil, nil
}

func (s *restartConflictStore) DeleteRestartSlots(context.Context, string, int64, []string) error {
	return errors.New("unexpected slot deletion")
}

func (s *restartConflictStore) FinishQueueRestart(_ context.Context, id string, version int64) error {
	current := s.events[id]
	if !current.Restarting || current.QueueVersion != version {
		return errors.New("finish used stale version")
	}
	current.Restarting = false
	current.Status = models.EventStatusActive
	current.QueueVersion++
	return nil
}

func TestRestartEventReloadsAfterBeginConflict(t *testing.T) {
	ctx := context.Background()
	events := &restartConflictStore{fakeEventStore: newFakeEventStore()}
	if err := events.CreateEvent(ctx, &models.Event{ID: "show", Status: models.EventStatusEnded, QueueVersion: 3}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(events, nil, &rotationRequestStore{}, 0)
	got, err := svc.RestartEvent(ctx, "show", true)
	if err != nil || got == nil || got.Status != models.EventStatusActive || got.Restarting || events.beginCalls != 2 {
		t.Fatalf("restart after begin conflict = (event=%+v, error=%v, begins=%d)", got, err, events.beginCalls)
	}
}

type retentionTestRequestStore struct {
	store.RequestStore
	failRetention  error
	retentionCalls int
	lastExpiresAt  time.Time
}

func (s *retentionTestRequestStore) SetRequestsTTLByEvent(_ context.Context, _ string, expiresAt time.Time) error {
	s.retentionCalls++
	s.lastExpiresAt = expiresAt
	return s.failRetention
}

func TestUpdateEvent_RetentionFailurePropagatesAndRetryDoesNotExtendRetention(t *testing.T) {
	ctx := context.Background()
	events := newFakeEventStore()
	ev := &models.Event{ID: "show-retention", Status: models.EventStatusActive, QueueVersion: 1}
	if err := events.CreateEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}

	reqStore := &retentionTestRequestStore{failRetention: errors.New("injected retention error")}
	svc := NewService(events, nil, reqStore, 7*24*time.Hour)

	endedStatus := models.EventStatusEnded
	// Attempt 1: Fails because retention write fails.
	got, err := svc.UpdateEvent(ctx, ev.ID, models.EventUpdate{Status: &endedStatus})
	if err == nil || got != nil {
		t.Fatalf("expected error from failed retention write, got (%v, %v)", got, err)
	}
	if reqStore.retentionCalls != 1 {
		t.Fatalf("retention calls = %d, want 1", reqStore.retentionCalls)
	}

	saved, err := events.GetEvent(ctx, ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != models.EventStatusEnded || saved.TTL == 0 || saved.EndedAt == nil {
		t.Fatalf("event status or TTL not established: %+v", saved)
	}
	originalTTL := saved.TTL
	originalEndedAt := *saved.EndedAt

	// Clear failure and advance time before retry.
	reqStore.failRetention = nil
	time.Sleep(10 * time.Millisecond)

	// Attempt 2: Retry must converge and MUST NOT extend TTL into the future.
	restarted, err := svc.UpdateEvent(ctx, ev.ID, models.EventUpdate{Status: &endedStatus})
	if err != nil || restarted == nil {
		t.Fatalf("retry UpdateEvent failed: %v", err)
	}
	if reqStore.retentionCalls != 2 {
		t.Fatalf("retention calls = %d, want 2", reqStore.retentionCalls)
	}
	if restarted.TTL != originalTTL {
		t.Fatalf("retry extended retention TTL: got %d, want original %d", restarted.TTL, originalTTL)
	}
	if !restarted.EndedAt.Equal(originalEndedAt) {
		t.Fatalf("retry altered EndedAt: got %v, want %v", restarted.EndedAt, originalEndedAt)
	}
	if reqStore.lastExpiresAt.Unix() != originalTTL {
		t.Fatalf("retention called with %v, want %v", reqStore.lastExpiresAt.Unix(), originalTTL)
	}
}

type failingUpdateEventStore struct {
	*fakeEventStore
}

func (s *failingUpdateEventStore) UpdateEventWithVersion(context.Context, *models.Event, bool) error {
	return errors.New("event update failed")
}

func TestUpdateEvent_EventUpdateFailureDoesNotSetRetention(t *testing.T) {
	ctx := context.Background()
	events := &failingUpdateEventStore{fakeEventStore: newFakeEventStore()}
	ev := &models.Event{ID: "show-fail-event", Status: models.EventStatusActive, QueueVersion: 1}
	if err := events.CreateEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}

	reqStore := &retentionTestRequestStore{}
	svc := NewService(events, nil, reqStore, 7*24*time.Hour)

	endedStatus := models.EventStatusEnded
	got, err := svc.UpdateEvent(ctx, ev.ID, models.EventUpdate{Status: &endedStatus})
	if err == nil || got != nil {
		t.Fatalf("expected error from failed event update, got (%v, %v)", got, err)
	}
	if reqStore.retentionCalls != 0 {
		t.Fatalf("retention was called %d times after event update failed", reqStore.retentionCalls)
	}
}

func TestRestartEvent_RetentionClearFailurePropagatesAndRetryConverges(t *testing.T) {
	ctx := context.Background()
	events := newFakeEventStore()
	ended := time.Now().UTC().Add(-2 * time.Hour)
	ev := &models.Event{
		ID:           "show-restart",
		Status:       models.EventStatusEnded,
		QueueVersion: 5,
		EndedAt:      &ended,
		TTL:          time.Now().UTC().Add(5 * 24 * time.Hour).Unix(),
	}
	if err := events.CreateEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}

	reqStore := &retentionTestRequestStore{failRetention: errors.New("clear retention error")}
	svc := NewService(events, nil, reqStore, 7*24*time.Hour)

	// Attempt 1: Clearing retention fails on restart.
	got, err := svc.RestartEvent(ctx, ev.ID, false)
	if err == nil || got != nil {
		t.Fatalf("expected restart error, got (%v, %v)", got, err)
	}
	if reqStore.retentionCalls != 1 {
		t.Fatalf("retention calls = %d, want 1", reqStore.retentionCalls)
	}

	// Attempt 2: Unblock retention and retry.
	reqStore.failRetention = nil
	resumed, err := svc.RestartEvent(ctx, ev.ID, false)
	if err != nil || resumed == nil {
		t.Fatalf("retry RestartEvent failed: %v", err)
	}
	if reqStore.retentionCalls != 2 {
		t.Fatalf("retention calls = %d, want 2", reqStore.retentionCalls)
	}
	if resumed.Status != models.EventStatusActive || resumed.TTL != 0 || resumed.EndedAt != nil {
		t.Fatalf("resumed event invalid: %+v", resumed)
	}
	if !reqStore.lastExpiresAt.IsZero() {
		t.Fatalf("clear retention passed non-zero expiresAt: %v", reqStore.lastExpiresAt)
	}
}

func TestUpdateEvent_StatusRequiresQueueRetentionStore(t *testing.T) {
	ctx := context.Background()
	events := newFakeEventStore()
	ev := &models.Event{ID: "show-weak", Status: models.EventStatusActive}
	if err := events.CreateEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}

	// Pass request store that does not implement QueueRetentionStore
	svc := NewService(events, nil, &rotationRequestStore{}, 7*24*time.Hour)
	endedStatus := models.EventStatusEnded
	got, err := svc.UpdateEvent(ctx, ev.ID, models.EventUpdate{Status: &endedStatus})
	if err == nil || got != nil {
		t.Fatalf("expected error for missing retention capability, got (%v, %v)", got, err)
	}

	gotRestart, err := svc.RestartEvent(ctx, ev.ID, false)
	if err == nil || gotRestart != nil {
		t.Fatalf("expected error for missing retention capability on restart, got (%v, %v)", gotRestart, err)
	}
}

type eventMutationConflictStore struct{ *fakeEventStore }

func (s *eventMutationConflictStore) UpdateEventWithVersion(_ context.Context, ev *models.Event, _ bool) error {
	return &store.QueueVersionConflictError{Current: ev.QueueVersion + 1, Expected: ev.QueueVersion}
}
func TestEventMutationConflictDoesNotApplyRetention(t *testing.T) {
	for _, action := range []string{"settings", "pause", "resume", "end", "key", "restart"} {
		t.Run(action, func(t *testing.T) {
			events := &eventMutationConflictStore{newFakeEventStore()}
			ctx := context.Background()
			original := &models.Event{ID: "event", Status: models.EventStatusActive, QueueVersion: 3}
			if err := events.CreateEvent(ctx, original); err != nil {
				t.Fatal(err)
			}
			requests := &retentionTestRequestStore{}
			svc := NewService(events, nil, requests, 0)
			var err error
			switch action {
			case "settings":
				name := "new"
				_, err = svc.UpdateEvent(ctx, "event", models.EventUpdate{Name: &name})
			case "pause":
				_, err = svc.PauseEvent(ctx, "event")
			case "resume":
				_, err = svc.ResumeEvent(ctx, "event")
			case "end":
				status := models.EventStatusEnded
				_, err = svc.UpdateEvent(ctx, "event", models.EventUpdate{Status: &status})
			case "key":
				_, err = svc.RotateOpenKJKey(ctx, "event")
			case "restart":
				_, err = svc.RestartEvent(ctx, "event", false)
			}
			var conflict *store.QueueVersionConflictError
			if !errors.As(err, &conflict) || requests.retentionCalls != 0 {
				t.Fatalf("conflict=%v retention=%d", err, requests.retentionCalls)
			}
			actual, _ := events.GetEvent(ctx, "event")
			if actual.Status != original.Status || actual.QueueVersion != original.QueueVersion || actual.MetadataVersion != original.MetadataVersion {
				t.Fatal("failed commit changed event")
			}
		})
	}
}

type eventReadFailureStore struct {
	*fakeEventStore
	failure error
}

func (s *eventReadFailureStore) GetEvent(context.Context, string) (*models.Event, error) {
	return nil, s.failure
}
func TestEventMutationReadFailurePropagates(t *testing.T) {
	failure := errors.New("event storage unavailable")
	events := &eventReadFailureStore{newFakeEventStore(), failure}
	svc := NewService(events, nil, nil, 0)
	name := "new"
	if _, err := svc.UpdateEvent(context.Background(), "event", models.EventUpdate{Name: &name}); !errors.Is(err, failure) {
		t.Fatalf("update read failure=%v", err)
	}
	if _, err := svc.RotateOpenKJKey(context.Background(), "event"); !errors.Is(err, failure) {
		t.Fatalf("key read failure=%v", err)
	}
}

type nilEventListStore struct {
	*fakeEventStore
}

func (s *nilEventListStore) ListEventsByOwner(_ context.Context, _ string) ([]*models.Event, error) {
	return nil, nil
}

func TestListEvents_NormalizesNilSliceToEmptyArray(t *testing.T) {
	svc := NewService(&nilEventListStore{newFakeEventStore()}, nil, nil, 0)
	events, err := svc.ListEvents(context.Background(), "host-with-no-events")
	if err != nil {
		t.Fatalf("ListEvents error: %v", err)
	}
	if events == nil {
		t.Fatal("expected non-nil empty slice from ListEvents when store returns nil, got nil")
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}
}

func TestEventService_AllowWriteInsCreateUpdateAndPublic(t *testing.T) {
	evStore := newFakeEventStore()
	sbStore := &fakeSongbookStore{books: map[string]*models.Songbook{
		"sb-wi": {ID: "sb-wi", OwnerID: "host-1", PublicID: "pub-wi", Name: "Book"},
	}}
	svc := NewService(evStore, sbStore, nil, 0)
	ctx := context.Background()

	ev, err := svc.CreateEvent(ctx, "host-1", models.EventCreate{SongbookID: "sb-wi", Name: "Write-In Show"})
	if err != nil {
		t.Fatalf("CreateEvent error: %v", err)
	}
	if ev.AllowWriteIns {
		t.Fatal("expected AllowWriteIns default to be false")
	}

	enable := true
	updated, err := svc.UpdateEvent(ctx, ev.ID, models.EventUpdate{AllowWriteIns: &enable})
	if err != nil {
		t.Fatalf("UpdateEvent error: %v", err)
	}
	if !updated.AllowWriteIns {
		t.Fatal("expected AllowWriteIns to be true after update")
	}

	pub, err := svc.GetPublicEvent(ctx, ev.ID)
	if err != nil {
		t.Fatalf("GetPublicEvent error: %v", err)
	}
	if !pub.AllowWriteIns {
		t.Fatal("expected PublicEvent.AllowWriteIns to be true")
	}
}
