package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type testEventStore struct {
	store.EventStore
	events     map[string]*models.Event
	transition interface {
		TransitionQueueRotation(context.Context, *models.Event, []*models.SongRequest) error
	}
}

// restartTestEventStore models the atomic restart contract for the handler
// lifecycle test; DynamoDB transaction behavior is covered in dynamo tests.
type restartTestEventStore struct {
	*testEventStore
	requests *testQueueStore
}

func (s *restartTestEventStore) BeginQueueRestart(_ context.Context, ev *models.Event) error {
	current := s.events[ev.ID]
	if current.Restarting || current.QueueVersion != ev.QueueVersion {
		return errors.New("restart conflict")
	}
	current.Status = models.EventStatusEnded
	current.Restarting = true
	current.TTL = 0
	current.QueueVersion++
	return nil
}

func (s *restartTestEventStore) ArchiveRestartBatch(_ context.Context, id string, version int64, requests []*models.SongRequest) error {
	current := s.events[id]
	if !current.Restarting || current.QueueVersion != version {
		return errors.New("restart conflict")
	}
	if s.requests.failUpdate != nil {
		return s.requests.failUpdate
	}
	updated := make(map[string]*models.SongRequest, len(requests))
	for _, req := range requests {
		if _, ok := s.requests.requests[req.ID]; !ok || req.EventID != id {
			return errors.New("restart request missing")
		}
		copy := *req
		if copy.Status == models.StatusPending || copy.Status == models.StatusAccepted || copy.Status == models.StatusUpNext {
			copy.Status = models.StatusCompleted
		}
		copy.TTL = 0
		updated[req.ID] = &copy
	}
	for id, req := range updated {
		s.requests.requests[id] = req
	}
	current.QueueVersion++
	return nil
}

func (s *restartTestEventStore) ListRestartSlots(context.Context, string) ([]string, error) {
	return nil, nil
}

func (s *restartTestEventStore) DeleteRestartSlots(context.Context, string, int64, []string) error {
	return nil
}

func (s *restartTestEventStore) FinishQueueRestart(_ context.Context, id string, version int64) error {
	current := s.events[id]
	if !current.Restarting || current.QueueVersion != version {
		return errors.New("restart conflict")
	}
	current.Status = models.EventStatusActive
	current.Restarting = false
	current.EndedAt = nil
	current.QueueVersion++
	return nil
}

func newTestEventStore() *testEventStore {
	return &testEventStore{
		events: make(map[string]*models.Event),
	}
}

func (s *testEventStore) CreateEvent(ctx context.Context, ev *models.Event) error {
	ev.CreatedAt = time.Now().UTC()
	ev.UpdatedAt = time.Now().UTC()
	cp := *ev
	s.events[ev.ID] = &cp
	return nil
}

func (s *testEventStore) GetEvent(ctx context.Context, id string) (*models.Event, error) {
	ev, ok := s.events[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	cp := *ev
	return &cp, nil
}

func (s *testEventStore) GetEventByJoinCode(ctx context.Context, joinCode string) (*models.Event, error) {
	code := strings.ToUpper(strings.TrimSpace(joinCode))
	for _, ev := range s.events {
		if strings.ToUpper(ev.JoinCode) == code {
			return ev, nil
		}
	}
	return nil, os.ErrNotExist
}

func (s *testEventStore) ListEventsByOwner(ctx context.Context, ownerID string) ([]*models.Event, error) {
	var list []*models.Event
	for _, ev := range s.events {
		if ev.OwnerID == ownerID {
			list = append(list, ev)
		}
	}
	return list, nil
}

func (s *testEventStore) ListEventsBySongbook(ctx context.Context, songbookID string) ([]*models.Event, error) {
	var list []*models.Event
	for _, ev := range s.events {
		if ev.SongbookID == songbookID {
			list = append(list, ev)
		}
	}
	return list, nil
}

func (s *testEventStore) UpdateEvent(ctx context.Context, ev *models.Event) error {
	return s.UpdateEventWithVersion(ctx, ev, false)
}

func (s *testEventStore) UpdateEventWithVersion(ctx context.Context, ev *models.Event, bumpVersion bool) error {
	current, ok := s.events[ev.ID]
	if !ok {
		return os.ErrNotExist
	}
	if current.QueueVersion != ev.QueueVersion || current.MetadataVersion != ev.MetadataVersion || current.Restarting {
		return &store.QueueVersionConflictError{Current: current.QueueVersion, Expected: ev.QueueVersion}
	}
	ev.MetadataVersion++

	if bumpVersion {
		ev.QueueVersion++
	}
	ev.UpdatedAt = time.Now().UTC()
	cp := *ev
	s.events[ev.ID] = &cp
	return nil
}

func (s *testEventStore) TransitionQueueRotation(ctx context.Context, ev *models.Event, requests []*models.SongRequest) error {
	if s.transition == nil {
		return errors.New("atomic transition store is required")
	}
	current, ok := s.events[ev.ID]
	if !ok {
		return os.ErrNotExist
	}
	if current.QueueVersion != ev.QueueVersion || current.MetadataVersion != ev.MetadataVersion || current.Restarting {
		return &store.QueueVersionConflictError{Current: current.QueueVersion, Expected: ev.QueueVersion}
	}
	if err := s.transition.TransitionQueueRotation(ctx, ev, requests); err != nil {
		return err
	}
	ev.MetadataVersion++
	cp := *ev
	s.events[ev.ID] = &cp
	return nil
}

func (s *testEventStore) DeleteEvent(ctx context.Context, id string) error {
	delete(s.events, id)
	return nil
}

func (s *testEventStore) BeginEventDeletion(_ context.Context, _ string) error {
	return nil
}

func (s *testEventStore) BumpQueueVersion(ctx context.Context, id string) (int64, error) {
	ev, ok := s.events[id]
	if !ok {
		return 0, os.ErrNotExist
	}
	ev.QueueVersion++
	return ev.QueueVersion, nil
}

func TestEventHandler_LifecycleAndJoinCode(t *testing.T) {
	sbStore := newTestStore()
	evStore := newTestEventStore()
	qStore := newTestQueueStore()

	eventHandler := &EventHandler{Events: evStore, Songbooks: sbStore}
	queueHandler := &QueueHandler{Events: evStore, Songbooks: sbStore, Requests: qStore}

	sb := &models.Songbook{ID: "sb-master", PublicID: "pub-master", Name: "Master Catalog", OwnerID: "host-1", SongCount: 500}
	_ = sbStore.CreateSongbook(context.Background(), sb)

	// 1. Host creates an event "Friday Karaoke" at "O'Malley's" with code "OMALLEY"
	createBody, _ := json.Marshal(models.EventCreate{
		SongbookID: "sb-master",
		Name:       "Friday Karaoke",
		VenueName:  "O'Malley's Pub",
		JoinCode:   "OMALLEY",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", bytes.NewReader(createBody))
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &models.User{ID: "host-1"}))
	w := httptest.NewRecorder()
	eventHandler.CreateEvent(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create event failed: %d: %s", w.Code, w.Body.String())
	}
	var createdEvent models.Event
	_ = json.Unmarshal(w.Body.Bytes(), &createdEvent)
	if createdEvent.JoinCode != "OMALLEY" || createdEvent.Status != models.EventStatusActive {
		t.Fatalf("unexpected created event: %+v", createdEvent)
	}

	// 2. Singer resolves event by code "omalley"
	codeReq := httptest.NewRequest(http.MethodGet, "/api/v1/join/omalley", nil)
	codeReq.SetPathValue("code", "omalley")
	w = httptest.NewRecorder()
	eventHandler.GetEventByCode(w, codeReq)

	if w.Code != http.StatusOK {
		t.Fatalf("get event by code failed: %d: %s", w.Code, w.Body.String())
	}
	var pubEvent models.PublicEvent
	_ = json.Unmarshal(w.Body.Bytes(), &pubEvent)
	if pubEvent.ID != createdEvent.ID || pubEvent.PublicSongbookID != "pub-master" || pubEvent.VenueName != "O'Malley's Pub" {
		t.Fatalf("unexpected public event: %+v", pubEvent)
	}

	// 3. Singer submits request to event
	reqBody, _ := json.Marshal(models.RequestCreate{
		SongTitle:  "Don't Stop Believin'",
		SingerName: "Jordan",
		KeyChange:  -1,
	})
	qReq := httptest.NewRequest(http.MethodPost, "/api/v1/events/"+createdEvent.ID+"/requests", bytes.NewReader(reqBody))
	qReq.SetPathValue("id", createdEvent.ID)
	w = httptest.NewRecorder()
	queueHandler.CreateEventRequest(w, qReq)

	if w.Code != http.StatusCreated {
		t.Fatalf("submit request failed: %d: %s", w.Code, w.Body.String())
	}

	// 4. Host pauses event requests
	pausedStatus := models.EventStatusPaused
	updateBody, _ := json.Marshal(models.EventUpdate{Status: &pausedStatus})
	updateReq := httptest.NewRequest(http.MethodPatch, "/api/v1/events/"+createdEvent.ID, bytes.NewReader(updateBody))
	updateReq.SetPathValue("id", createdEvent.ID)
	w = httptest.NewRecorder()
	eventHandler.UpdateEvent(w, updateReq)

	if w.Code != http.StatusOK {
		t.Fatalf("update event failed: %d", w.Code)
	}

	// 5. Subsequent request during pause should be rejected with 409 Conflict
	w = httptest.NewRecorder()
	queueHandler.CreateEventRequest(w, qReq)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict during pause, got %d", w.Code)
	}

	// 6. Host sets Tip URL, Announcement, and Kiosk PIN
	tipURL := "https://venmo.com/karaoke-king"
	announcement := "Last call for singer signups!"
	kioskPIN := "4321"
	featureUpdate, _ := json.Marshal(models.EventUpdate{
		TipURL:       &tipURL,
		Announcement: &announcement,
		KioskPIN:     &kioskPIN,
	})
	updateReq = httptest.NewRequest(http.MethodPatch, "/api/v1/events/"+createdEvent.ID, bytes.NewReader(featureUpdate))
	updateReq.SetPathValue("id", createdEvent.ID)
	w = httptest.NewRecorder()
	eventHandler.UpdateEvent(w, updateReq)
	if w.Code != http.StatusOK {
		t.Fatalf("update event with tip/announcement failed: %d", w.Code)
	}

	// 7. Verify public details reflect tip URL and announcement
	pubReq := httptest.NewRequest(http.MethodGet, "/api/v1/events/"+createdEvent.ID+"/public", nil)
	pubReq.SetPathValue("id", createdEvent.ID)
	w = httptest.NewRecorder()
	eventHandler.GetPublicEvent(w, pubReq)
	if w.Code != http.StatusOK {
		t.Fatalf("get public event failed: %d", w.Code)
	}
	var pubDetails models.PublicEvent
	_ = json.Unmarshal(w.Body.Bytes(), &pubDetails)
	if pubDetails.TipURL != tipURL || pubDetails.Announcement != announcement {
		t.Fatalf("unexpected public event details: %+v", pubDetails)
	}
}

func TestEventHandler_RecurringAndRestartLifecycle(t *testing.T) {
	sbStore := newTestStore()
	qStore := newTestQueueStore()
	evStore := &restartTestEventStore{testEventStore: newTestEventStore(), requests: qStore}

	eventHandler := &EventHandler{Events: evStore, Songbooks: sbStore, Requests: qStore}
	queueHandler := &QueueHandler{Events: evStore, Songbooks: sbStore, Requests: qStore}

	sb := &models.Songbook{ID: "sb-weekly", PublicID: "pub-weekly", Name: "Weekly Catalog", OwnerID: "host-1", SongCount: 300}
	_ = sbStore.CreateSongbook(context.Background(), sb)

	// 1. Host creates a recurring event "Tuesday Night Karaoke"
	createBody, _ := json.Marshal(models.EventCreate{
		SongbookID: "sb-weekly",
		Name:       "Tuesday Night Karaoke",
		VenueName:  "O'Malley's",
		JoinCode:   "OMALLEY",
		Recurring:  true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", bytes.NewReader(createBody))
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &models.User{ID: "host-1", IsHost: true}))
	w := httptest.NewRecorder()
	eventHandler.CreateEvent(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", w.Code, w.Body.String())
	}
	var createdEvent models.Event
	_ = json.Unmarshal(w.Body.Bytes(), &createdEvent)
	if !createdEvent.Recurring {
		t.Fatal("expected event to be marked recurring")
	}

	// 2. Singer submits a request for the first session
	qBody, _ := json.Marshal(models.RequestCreate{
		SingerName: "Alice",
		SongTitle:  "Bohemian Rhapsody",
		SongArtist: "Queen",
	})
	qReq := httptest.NewRequest(http.MethodPost, "/api/v1/events/"+createdEvent.ID+"/requests", bytes.NewReader(qBody))
	qReq.SetPathValue("id", createdEvent.ID)
	w = httptest.NewRecorder()
	queueHandler.CreateEventRequest(w, qReq)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for request, got %d", w.Code)
	}

	// 3. Host ends the show at 2 AM
	endedStatus := models.EventStatusEnded
	endBody, _ := json.Marshal(models.EventUpdate{Status: &endedStatus})
	updateReq := httptest.NewRequest(http.MethodPatch, "/api/v1/events/"+createdEvent.ID, bytes.NewReader(endBody))
	updateReq.SetPathValue("id", createdEvent.ID)
	w = httptest.NewRecorder()
	eventHandler.UpdateEvent(w, updateReq)
	if w.Code != http.StatusOK {
		t.Fatalf("end event failed: %d", w.Code)
	}
	var endedEvent models.Event
	_ = json.Unmarshal(w.Body.Bytes(), &endedEvent)
	if endedEvent.Status != models.EventStatusEnded || endedEvent.EndedAt == nil {
		t.Fatalf("expected event status ended with EndedAt set, got %+v", endedEvent)
	}

	// 4. While ended, new singer requests should be rejected with 410 Gone
	w = httptest.NewRecorder()
	queueHandler.CreateEventRequest(w, qReq)
	if w.Code != http.StatusGone {
		t.Fatalf("expected 410 Gone while ended, got %d", w.Code)
	}

	// 5. Next week, host restarts the show with fresh queue
	restartBody, _ := json.Marshal(map[string]bool{"clear_queue": true})
	restartReq := httptest.NewRequest(http.MethodPost, "/api/v1/events/"+createdEvent.ID+"/restart", bytes.NewReader(restartBody))
	restartReq.SetPathValue("id", createdEvent.ID)
	w = httptest.NewRecorder()
	eventHandler.RestartEvent(w, restartReq)
	if w.Code != http.StatusOK {
		t.Fatalf("restart event failed: %d: %s", w.Code, w.Body.String())
	}
	var restartedEvent models.Event
	_ = json.Unmarshal(w.Body.Bytes(), &restartedEvent)
	if restartedEvent.Status != models.EventStatusActive {
		t.Fatalf("expected status active after restart, got %s", restartedEvent.Status)
	}
	if restartedEvent.EndedAt != nil {
		t.Fatalf("expected EndedAt to be nil after restart, got %v", restartedEvent.EndedAt)
	}

	// 6. Old pending request should now be marked completed (archived)
	reqs, err := qStore.ListRequestsByEvent(context.Background(), createdEvent.ID)
	if err != nil {
		t.Fatalf("list requests failed: %v", err)
	}
	if len(reqs) > 0 && reqs[0].Status != models.StatusCompleted {
		t.Fatalf("expected past request to be archived/completed, got %s", reqs[0].Status)
	}

	// 7. New singer can now submit a request for tonight's session
	newReqBody, _ := json.Marshal(models.RequestCreate{
		SingerName: "Bob",
		SongTitle:  "Don't Stop Believin'",
		SongArtist: "Journey",
	})
	newReq := httptest.NewRequest(http.MethodPost, "/api/v1/events/"+createdEvent.ID+"/requests", bytes.NewReader(newReqBody))
	newReq.SetPathValue("id", createdEvent.ID)
	w = httptest.NewRecorder()
	queueHandler.CreateEventRequest(w, newReq)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for request on restarted show, got %d: %s", w.Code, w.Body.String())
	}
}

func TestEventHandler_DeleteEvent_CascadesRequests(t *testing.T) {
	evStore := newTestEventStore()
	qStore := newTestQueueStore()

	eventHandler := &EventHandler{Events: evStore, Requests: qStore}

	ev := &models.Event{ID: "ev-delete-me", Name: "To Be Deleted", Status: models.EventStatusActive}
	if err := evStore.CreateEvent(context.Background(), ev); err != nil {
		t.Fatalf("create event: %v", err)
	}

	req1 := &models.SongRequest{ID: "req-del-1", EventID: ev.ID, SingerName: "Alice", SongTitle: "Song 1"}
	req2 := &models.SongRequest{ID: "req-del-2", EventID: ev.ID, SingerName: "Bob", SongTitle: "Song 2"}
	if err := qStore.CreateRequest(context.Background(), req1); err != nil {
		t.Fatalf("create request 1: %v", err)
	}
	if err := qStore.CreateRequest(context.Background(), req2); err != nil {
		t.Fatalf("create request 2: %v", err)
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/events/"+ev.ID, nil)
	delReq.SetPathValue("id", ev.ID)
	rec := httptest.NewRecorder()

	eventHandler.DeleteEvent(rec, delReq)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 No Content, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify event deleted
	if _, err := evStore.GetEvent(context.Background(), ev.ID); err == nil {
		t.Fatalf("expected event to be deleted, but still found")
	}

	// Verify requests deleted
	reqs, err := qStore.ListRequestsByEvent(context.Background(), ev.ID)
	if err != nil {
		t.Fatalf("list requests: %v", err)
	}
	if len(reqs) != 0 {
		t.Fatalf("expected 0 requests remaining, got %d", len(reqs))
	}
}

func TestEventHandler_CreateEvent_RotationMode(t *testing.T) {
	sbStore := newTestStore()
	evStore := newTestEventStore()
	eventHandler := &EventHandler{Events: evStore, Songbooks: sbStore}

	sb := &models.Songbook{ID: "sb-modes", Name: "Modes Songbook", OwnerID: "host-modes"}
	_ = sbStore.CreateSongbook(context.Background(), sb)

	tests := []struct {
		requestedMode string
		expectedMode  string
	}{
		{"", models.RotationModeRoundRobin},
		{"fcfs", models.RotationModeFCFS},
		{"FCFS", models.RotationModeFCFS},
		{"manual", models.RotationModeManual},
		{"round_robin", models.RotationModeRoundRobin},
		{"invalid_mode", models.RotationModeRoundRobin},
	}

	for _, tt := range tests {
		t.Run("mode_"+tt.requestedMode, func(t *testing.T) {
			body, _ := json.Marshal(models.EventCreate{
				SongbookID:   "sb-modes",
				Name:         "Show " + tt.requestedMode,
				RotationMode: tt.requestedMode,
			})
			req := httptest.NewRequest(http.MethodPost, "/api/v1/events", bytes.NewReader(body))
			req = req.WithContext(context.WithValue(req.Context(), userContextKey, &models.User{ID: "host-modes"}))
			w := httptest.NewRecorder()

			eventHandler.CreateEvent(w, req)
			if w.Code != http.StatusCreated {
				t.Fatalf("create event failed: %d: %s", w.Code, w.Body.String())
			}
			var ev models.Event
			_ = json.Unmarshal(w.Body.Bytes(), &ev)
			if ev.RotationMode != tt.expectedMode {
				t.Fatalf("requested %q, expected rotation mode %q, got %q", tt.requestedMode, tt.expectedMode, ev.RotationMode)
			}
		})
	}
}

func TestEventHandler_VerifyKioskPIN(t *testing.T) {
	evStore := newTestEventStore()
	eventHandler := &EventHandler{Events: evStore}

	// 1. Event with custom PIN
	ev1 := &models.Event{ID: "ev-pin-1", Name: "Show 1", KioskPIN: "5678"}
	_ = evStore.CreateEvent(context.Background(), ev1)

	// Correct PIN -> 204
	reqCorrect := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-pin-1/kiosk/verify-pin", strings.NewReader(`{"pin":"5678"}`))
	reqCorrect.SetPathValue("id", "ev-pin-1")
	wCorrect := httptest.NewRecorder()
	eventHandler.VerifyKioskPIN(wCorrect, reqCorrect)
	if wCorrect.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for correct PIN, got %d", wCorrect.Code)
	}

	// Incorrect PIN (same length) -> 401
	reqWrong := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-pin-1/kiosk/verify-pin", strings.NewReader(`{"pin":"1234"}`))
	reqWrong.SetPathValue("id", "ev-pin-1")
	wWrong := httptest.NewRecorder()
	eventHandler.VerifyKioskPIN(wWrong, reqWrong)
	if wWrong.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for incorrect PIN, got %d", wWrong.Code)
	}
	var errBody map[string]interface{}
	_ = json.Unmarshal(wWrong.Body.Bytes(), &errBody)
	if errBody["error"] != "invalid_pin" {
		t.Fatalf("expected error invalid_pin, got %v", errBody["error"])
	}

	// Incorrect PIN (different length, length-hiding) -> 401
	reqDiffLen := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-pin-1/kiosk/verify-pin", strings.NewReader(`{"pin":"987654321"}`))
	reqDiffLen.SetPathValue("id", "ev-pin-1")
	wDiffLen := httptest.NewRecorder()
	eventHandler.VerifyKioskPIN(wDiffLen, reqDiffLen)
	if wDiffLen.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for different length PIN, got %d", wDiffLen.Code)
	}

	// 2. Event with unset PIN -> 403 Forbidden
	evUnset := &models.Event{ID: "ev-pin-unset", Name: "Show Unset"}
	_ = evStore.CreateEvent(context.Background(), evUnset)

	reqUnset := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-pin-unset/kiosk/verify-pin", strings.NewReader(`{"pin":"1234"}`))
	reqUnset.SetPathValue("id", "ev-pin-unset")
	wUnset := httptest.NewRecorder()
	eventHandler.VerifyKioskPIN(wUnset, reqUnset)
	if wUnset.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for unset PIN, got %d", wUnset.Code)
	}
	var unsetErr map[string]interface{}
	_ = json.Unmarshal(wUnset.Body.Bytes(), &unsetErr)
	if unsetErr["error"] != "pin_not_configured" {
		t.Fatalf("expected error pin_not_configured, got %v", unsetErr["error"])
	}

	// 3. Unknown event -> 404
	reqNotFound := httptest.NewRequest(http.MethodPost, "/api/v1/events/nonexistent/kiosk/verify-pin", strings.NewReader(`{"pin":"1234"}`))
	reqNotFound.SetPathValue("id", "nonexistent")
	wNotFound := httptest.NewRecorder()
	eventHandler.VerifyKioskPIN(wNotFound, reqNotFound)
	if wNotFound.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for nonexistent event, got %d", wNotFound.Code)
	}
}

func TestEventHandler_MaxActivePerSinger_ConfigurableZeroUnlimited(t *testing.T) {
	sbStore := newTestStore()
	evStore := newTestEventStore()
	eventHandler := &EventHandler{Events: evStore, Songbooks: sbStore}

	sb := &models.Songbook{ID: "sb-limit", Name: "Limit Songbook", OwnerID: "host-limit"}
	_ = sbStore.CreateSongbook(context.Background(), sb)

	// 1. Create with MaxActivePerSinger = 0 (unlimited)
	zeroLimit := 0
	createBody, _ := json.Marshal(models.EventCreate{
		SongbookID:         "sb-limit",
		Name:               "Unlimited Show",
		MaxActivePerSinger: &zeroLimit,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", bytes.NewReader(createBody))
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &models.User{ID: "host-limit"}))
	w := httptest.NewRecorder()
	eventHandler.CreateEvent(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create event failed: %d: %s", w.Code, w.Body.String())
	}

	// Verify JSON explicitly includes "max_active_per_singer": 0
	var rawJSON map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &rawJSON)
	if val, ok := rawJSON["max_active_per_singer"]; !ok || val != float64(0) {
		t.Fatalf("expected JSON to have max_active_per_singer: 0, got %v (raw: %s)", val, w.Body.String())
	}

	eventID := rawJSON["id"].(string)

	// 2. Update to 3
	threeLimit := 3
	updateBody, _ := json.Marshal(models.EventUpdate{
		MaxActivePerSinger: &threeLimit,
	})
	reqUpdate := httptest.NewRequest(http.MethodPatch, "/api/v1/events/"+eventID, bytes.NewReader(updateBody))
	reqUpdate.SetPathValue("id", eventID)
	reqUpdate = reqUpdate.WithContext(context.WithValue(reqUpdate.Context(), userContextKey, &models.User{ID: "host-limit"}))
	wUpdate := httptest.NewRecorder()
	eventHandler.UpdateEvent(wUpdate, reqUpdate)
	if wUpdate.Code != http.StatusOK {
		t.Fatalf("update event failed: %d: %s", wUpdate.Code, wUpdate.Body.String())
	}
	var rawUpdated map[string]interface{}
	_ = json.Unmarshal(wUpdate.Body.Bytes(), &rawUpdated)
	if val, ok := rawUpdated["max_active_per_singer"]; !ok || val != float64(3) {
		t.Fatalf("expected max_active_per_singer: 3, got %v", val)
	}

	// 3. Update back to 0 (unlimited)
	updateZeroBody, _ := json.Marshal(models.EventUpdate{
		MaxActivePerSinger: &zeroLimit,
	})
	reqUpdateZero := httptest.NewRequest(http.MethodPatch, "/api/v1/events/"+eventID, bytes.NewReader(updateZeroBody))
	reqUpdateZero.SetPathValue("id", eventID)
	reqUpdateZero = reqUpdateZero.WithContext(context.WithValue(reqUpdateZero.Context(), userContextKey, &models.User{ID: "host-limit"}))
	wUpdateZero := httptest.NewRecorder()
	eventHandler.UpdateEvent(wUpdateZero, reqUpdateZero)
	if wUpdateZero.Code != http.StatusOK {
		t.Fatalf("update event to 0 failed: %d: %s", wUpdateZero.Code, wUpdateZero.Body.String())
	}
	var rawUpdatedZero map[string]interface{}
	_ = json.Unmarshal(wUpdateZero.Body.Bytes(), &rawUpdatedZero)
	if val, ok := rawUpdatedZero["max_active_per_singer"]; !ok || val != float64(0) {
		t.Fatalf("expected max_active_per_singer: 0 for unlimited, got %v", val)
	}
}

func TestEventHandler_GetEvent(t *testing.T) {
	events := newTestEventStore()
	songbooks := newTestStore()
	h := &EventHandler{Events: events, Songbooks: songbooks}

	ev := &models.Event{
		ID:        "evt-get-1",
		OwnerID:   "owner-1",
		Name:      "Show 1",
		OpenKJKey: "okj_evt-get-1_secret",
		KioskPIN:  "4321",
	}
	_ = events.CreateEvent(context.Background(), ev)

	// 1. Direct GetEvent
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/evt-get-1", nil)
	req.SetPathValue("id", "evt-get-1")
	w := httptest.NewRecorder()
	h.GetEvent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var loaded models.Event
	if err := json.Unmarshal(w.Body.Bytes(), &loaded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if loaded.OpenKJKey != "okj_evt-get-1_secret" || loaded.KioskPIN != "4321" {
		t.Errorf("expected host view to contain private keys, got %+v", loaded)
	}

	// 2. Direct GetEvent Not Found
	reqNotFound := httptest.NewRequest(http.MethodGet, "/api/v1/events/evt-missing", nil)
	reqNotFound.SetPathValue("id", "evt-missing")
	wNotFound := httptest.NewRecorder()
	h.GetEvent(wNotFound, reqNotFound)
	if wNotFound.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", wNotFound.Code)
	}

	// 3. Wrapped with RequireOwnership - Owner
	guarded := h.RequireOwnership(h.GetEvent)
	reqOwner := httptest.NewRequest(http.MethodGet, "/api/v1/events/evt-get-1", nil)
	reqOwner.SetPathValue("id", "evt-get-1")
	reqOwner = reqOwner.WithContext(context.WithValue(reqOwner.Context(), userContextKey, &models.User{ID: "owner-1"}))
	wOwner := httptest.NewRecorder()
	guarded(wOwner, reqOwner)
	if wOwner.Code != http.StatusOK {
		t.Fatalf("expected 200 for owner, got %d", wOwner.Code)
	}

	// 4. Wrapped with RequireOwnership - Non-Owner
	reqOther := httptest.NewRequest(http.MethodGet, "/api/v1/events/evt-get-1", nil)
	reqOther.SetPathValue("id", "evt-get-1")
	reqOther = reqOther.WithContext(context.WithValue(reqOther.Context(), userContextKey, &models.User{ID: "other-user"}))
	wOther := httptest.NewRecorder()
	guarded(wOther, reqOther)
	if wOther.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-owner, got %d", wOther.Code)
	}
}

func TestGetEventByCode_SongbookSlugFallback(t *testing.T) {
	events := newTestEventStore()
	songbooks := newTestStore()
	h := &EventHandler{Events: events, Songbooks: songbooks}

	sb := &models.Songbook{
		ID:        "sb-slug-resolve",
		OwnerID:   "owner-1",
		PublicID:  "pub-uuid-1234",
		Slug:      "davemic",
		Name:      "Dave's Songbook",
		IsPublic:  true,
		SongCount: 42,
	}
	_ = songbooks.CreateSongbook(context.Background(), sb)

	// 1. Resolve by exact slug
	reqSlug := httptest.NewRequest(http.MethodGet, "/api/v1/join/davemic", nil)
	reqSlug.SetPathValue("code", "davemic")
	wSlug := httptest.NewRecorder()
	h.GetEventByCode(wSlug, reqSlug)

	if wSlug.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", wSlug.Code, wSlug.Body.String())
	}
	var res map[string]interface{}
	if err := json.Unmarshal(wSlug.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if res["type"] != "songbook" || res["slug"] != "davemic" || res["public_songbook_id"] != "pub-uuid-1234" {
		t.Errorf("unexpected response: %+v", res)
	}

	// 2. Resolve case-insensitively (e.g. DAVEMIC entered by user)
	reqUpper := httptest.NewRequest(http.MethodGet, "/api/v1/join/DAVEMIC", nil)
	reqUpper.SetPathValue("code", "DAVEMIC")
	wUpper := httptest.NewRecorder()
	h.GetEventByCode(wUpper, reqUpper)

	if wUpper.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", wUpper.Code, wUpper.Body.String())
	}

	// 3. Unknown code returns 404
	reqUnknown := httptest.NewRequest(http.MethodGet, "/api/v1/join/nonexistent", nil)
	reqUnknown.SetPathValue("code", "nonexistent")
	wUnknown := httptest.NewRecorder()
	h.GetEventByCode(wUnknown, reqUnknown)
	if wUnknown.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", wUnknown.Code)
	}
}

func TestUpdateEvent_RotationModeTransitionPreservesStageTop3(t *testing.T) {
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	evStore.transition = qStore
	h := &EventHandler{Events: evStore, Requests: qStore}

	event := &models.Event{
		ID:           "ev-transition-1",
		OwnerID:      "host-user-1",
		RotationMode: models.RotationModeRoundRobin,
		CurrentRound: 1,
		Status:       models.EventStatusActive,
	}
	_ = evStore.CreateEvent(context.Background(), event)

	now := time.Now().UTC()
	req1 := &models.SongRequest{ID: "r1", EventID: event.ID, SingerName: "Alice", Status: models.StatusUpNext, Rank: 1, Round: 1, CreatedAt: now.Add(-5 * time.Minute)}
	req2 := &models.SongRequest{ID: "r2", EventID: event.ID, SingerName: "Bob", Status: models.StatusAccepted, Rank: 2, Round: 1, CreatedAt: now.Add(-4 * time.Minute)}
	req3 := &models.SongRequest{ID: "r3", EventID: event.ID, SingerName: "Charlie", Status: models.StatusAccepted, Rank: 3, Round: 1, CreatedAt: now.Add(-3 * time.Minute)}
	// Tail: Dave submitted 2nd song (Cycle 1) before Eve submitted 1st song (Cycle 0)
	req4 := &models.SongRequest{ID: "r4", EventID: event.ID, SingerName: "Dave", Status: models.StatusAccepted, Rank: 4, Round: 2, CreatedAt: now.Add(-2 * time.Minute)}
	req5 := &models.SongRequest{ID: "r5", EventID: event.ID, SingerName: "Eve", Status: models.StatusAccepted, Rank: 5, Round: 1, CreatedAt: now.Add(-1 * time.Minute)}

	_ = qStore.CreateRequest(context.Background(), req1)
	_ = qStore.CreateRequest(context.Background(), req2)
	_ = qStore.CreateRequest(context.Background(), req3)
	_ = qStore.CreateRequest(context.Background(), req4)
	_ = qStore.CreateRequest(context.Background(), req5)

	// Dave also has 1 song in protected (let's say req2 is Dave)
	req2.SingerName = "Dave"
	_ = qStore.UpdateRequest(context.Background(), req2)
	// Now Dave has 1 active in protected (req2), so Dave's req4 in tail is Cycle 1!
	// Eve (req5) has 0 in protected, so Eve is Cycle 0!

	// Host transitions rotation mode from round_robin to fair_priority
	body := `{"rotation_mode":"fair_priority"}`
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-transition-1", bytes.NewBufferString(body))
	r.SetPathValue("id", event.ID)
	r = r.WithContext(context.WithValue(r.Context(), userContextKey, &models.User{ID: "host-user-1", IsHost: true}))
	w := httptest.NewRecorder()
	h.UpdateEvent(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("UpdateEvent failed with code %d: %s", w.Code, w.Body.String())
	}

	// Verify top 3 stage positions survived completely:
	// Order must be [r1, r2, r3, r5, r4] because r5 (Eve, Cycle 0) re-weaves before r4 (Dave, Cycle 1)!
	if len(qStore.order) != 5 {
		t.Fatalf("expected 5 requests in order, got %d", len(qStore.order))
	}
	if qStore.order[0] != "r1" || qStore.order[1] != "r2" || qStore.order[2] != "r3" {
		t.Fatalf("top 3 stage positions did not survive transition! order=%v", qStore.order)
	}
	if qStore.order[3] != "r5" || qStore.order[4] != "r4" {
		t.Fatalf("tail was not re-slotted by fair priority! expected [r5, r4], got [%s, %s]", qStore.order[3], qStore.order[4])
	}
}

func TestUpdateEvent_RotationModeTransitionIntoRoundRobinPreservesStageTop3(t *testing.T) {
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	evStore.transition = qStore
	h := &EventHandler{Events: evStore, Requests: qStore}

	event := &models.Event{
		ID:           "ev-transition-rr",
		OwnerID:      "host-user-1",
		RotationMode: models.RotationModeFairPriority,
		CurrentRound: 1,
		Status:       models.EventStatusActive,
	}
	_ = evStore.CreateEvent(context.Background(), event)

	now := time.Now().UTC()
	// Stage buffer: Alice, Bob, Charlie submitted recently
	r1 := &models.SongRequest{ID: "r1", EventID: event.ID, SingerName: "Alice", Status: models.StatusAccepted, Rank: 1, CreatedAt: now.Add(-2 * time.Minute)}
	r2 := &models.SongRequest{ID: "r2", EventID: event.ID, SingerName: "Bob", Status: models.StatusAccepted, Rank: 2, CreatedAt: now.Add(-1 * time.Minute)}
	r3 := &models.SongRequest{ID: "r3", EventID: event.ID, SingerName: "Charlie", Status: models.StatusAccepted, Rank: 3, CreatedAt: now}
	// Tail: Dave submitted 30 minutes earlier (before Alice, Bob, Charlie)
	r4 := &models.SongRequest{ID: "r4", EventID: event.ID, SingerName: "Dave", Status: models.StatusAccepted, Rank: 4, CreatedAt: now.Add(-30 * time.Minute)}

	_ = qStore.CreateRequest(context.Background(), r1)
	_ = qStore.CreateRequest(context.Background(), r2)
	_ = qStore.CreateRequest(context.Background(), r3)
	_ = qStore.CreateRequest(context.Background(), r4)

	// Transition from fair_priority to round_robin
	body := `{"rotation_mode":"round_robin"}`
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-transition-rr", bytes.NewBufferString(body))
	r.SetPathValue("id", event.ID)
	r = r.WithContext(context.WithValue(r.Context(), userContextKey, &models.User{ID: "host-user-1", IsHost: true}))
	w := httptest.NewRecorder()
	h.UpdateEvent(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("UpdateEvent failed with code %d: %s", w.Code, w.Body.String())
	}

	// Verify top 3 stage positions strictly survived transition in exact order
	// Dave (r4), despite earlier CreatedAt, must NOT jump ahead of the stage buffer!
	if len(qStore.order) != 4 {
		t.Fatalf("expected 4 requests in order, got %d", len(qStore.order))
	}
	if qStore.order[0] != "r1" || qStore.order[1] != "r2" || qStore.order[2] != "r3" {
		t.Fatalf("top 3 stage positions did not survive transition into round robin! order=%v", qStore.order)
	}
	if qStore.order[3] != "r4" {
		t.Fatalf("tail request r4 expected at position 3, got %s", qStore.order[3])
	}
}

func TestUpdateEvent_RotationModeTransitionNormalizesPendingRequests(t *testing.T) {
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	evStore.transition = qStore
	h := &EventHandler{Events: evStore, Requests: qStore}

	event := &models.Event{
		ID:           "ev-norm-pend",
		OwnerID:      "host-user-1",
		RotationMode: models.RotationModeRoundRobin,
		CurrentRound: 1,
		Status:       models.EventStatusActive,
	}
	_ = evStore.CreateEvent(context.Background(), event)

	now := time.Now().UTC()
	rActive := &models.SongRequest{ID: "rActive", EventID: event.ID, SingerName: "Alice", Status: models.StatusAccepted, Rank: 1, Round: 1, CreatedAt: now}
	rPend := &models.SongRequest{ID: "rPend", EventID: event.ID, SingerName: "Bob", Status: models.StatusPending, Rank: 2, Round: 2, CreatedAt: now}
	_ = qStore.CreateRequest(context.Background(), rActive)
	_ = qStore.CreateRequest(context.Background(), rPend)

	// 1. Switch to fair_priority -> pending request Round normalized to 0
	body := `{"rotation_mode":"fair_priority"}`
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-norm-pend", bytes.NewBufferString(body))
	r.SetPathValue("id", event.ID)
	r = r.WithContext(context.WithValue(r.Context(), userContextKey, &models.User{ID: "host-user-1", IsHost: true}))
	w := httptest.NewRecorder()
	h.UpdateEvent(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("failed to update event: %d", w.Code)
	}
	upPend, _ := qStore.GetRequest(context.Background(), "rPend")
	if upPend.Round != 0 {
		t.Fatalf("expected pending request Round to normalize to 0 in fair_priority, got %d", upPend.Round)
	}

	// 2. Switch back to round_robin with current_round=3 -> pending request Round normalized to 3
	savedEv, _ := evStore.GetEvent(context.Background(), event.ID)
	savedEv.CurrentRound = 3
	_ = evStore.UpdateEvent(context.Background(), savedEv)

	bodyRR := `{"rotation_mode":"round_robin"}`
	rRR := httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-norm-pend", bytes.NewBufferString(bodyRR))
	rRR.SetPathValue("id", event.ID)
	rRR = rRR.WithContext(context.WithValue(rRR.Context(), userContextKey, &models.User{ID: "host-user-1", IsHost: true}))
	wRR := httptest.NewRecorder()
	h.UpdateEvent(wRR, rRR)
	if wRR.Code != http.StatusOK {
		t.Fatalf("failed to update event: %d", wRR.Code)
	}
	upPendRR, _ := qStore.GetRequest(context.Background(), "rPend")
	if upPendRR.Round != 3 {
		t.Fatalf("expected pending request Round to normalize to current round 3, got %d", upPendRR.Round)
	}
}

func TestUpdateEvent_RotationModeTransitionErrorHandling(t *testing.T) {
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	evStore.transition = qStore
	h := &EventHandler{Events: evStore, Requests: qStore}

	event := &models.Event{
		ID:           "ev-err-handle",
		OwnerID:      "host-user-1",
		RotationMode: models.RotationModeFairPriority,
		CurrentRound: 1,
		Status:       models.EventStatusActive,
	}
	_ = evStore.CreateEvent(context.Background(), event)

	now := time.Now().UTC()
	r1 := &models.SongRequest{ID: "r1", EventID: event.ID, SingerName: "Alice", Status: models.StatusAccepted, Rank: 1, CreatedAt: now}
	_ = qStore.CreateRequest(context.Background(), r1)

	// Simulate a DynamoDB failure during request update in transition
	qStore.failUpdate = errors.New("dynamo connection timeout")

	body := `{"rotation_mode":"round_robin"}`
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-err-handle", bytes.NewBufferString(body))
	r.SetPathValue("id", event.ID)
	r = r.WithContext(context.WithValue(r.Context(), userContextKey, &models.User{ID: "host-user-1", IsHost: true}))
	w := httptest.NewRecorder()
	h.UpdateEvent(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 InternalServerError on store failure, got %d", w.Code)
	}

	// Verify the event in evStore was NOT changed to round_robin
	savedEvent, _ := evStore.GetEvent(context.Background(), event.ID)
	if savedEvent.RotationMode != models.RotationModeFairPriority {
		t.Fatalf("expected event mode to remain fair_priority on transition failure, got %s", savedEvent.RotationMode)
	}

	// Verify request was not partially updated
	savedR1, _ := qStore.GetRequest(context.Background(), "r1")
	if savedR1.Round != 0 {
		t.Fatalf("expected request round to remain untouched on transition failure, got %d", savedR1.Round)
	}
}

func TestUpdateEvent_RotationModeTransition_MultipleRequestsAtomicRollback(t *testing.T) {
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	evStore.transition = qStore
	h := &EventHandler{Events: evStore, Requests: qStore}

	event := &models.Event{
		ID:           "ev-multi-rollback",
		OwnerID:      "host-user-1",
		RotationMode: models.RotationModeFairPriority,
		CurrentRound: 1,
		Status:       models.EventStatusActive,
	}
	_ = evStore.CreateEvent(context.Background(), event)

	now := time.Now().UTC()
	r1 := &models.SongRequest{ID: "r1", EventID: event.ID, SingerName: "Alice", Status: models.StatusAccepted, Rank: 100, Round: 0, CreatedAt: now.Add(-3 * time.Minute)}
	r2 := &models.SongRequest{ID: "r2", EventID: event.ID, SingerName: "Bob", Status: models.StatusAccepted, Rank: 200, Round: 0, CreatedAt: now.Add(-2 * time.Minute)}
	r3 := &models.SongRequest{ID: "r3", EventID: event.ID, SingerName: "Charlie", Status: models.StatusPending, Rank: 300, Round: 0, CreatedAt: now.Add(-1 * time.Minute)}
	_ = qStore.CreateRequest(context.Background(), r1)
	_ = qStore.CreateRequest(context.Background(), r2)
	_ = qStore.CreateRequest(context.Background(), r3)

	// Simulate failure in transition store
	qStore.failUpdate = errors.New("dynamodb transaction canceled")

	body := `{"rotation_mode":"round_robin"}`
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-multi-rollback", bytes.NewBufferString(body))
	r.SetPathValue("id", event.ID)
	r = r.WithContext(context.WithValue(r.Context(), userContextKey, &models.User{ID: "host-user-1", IsHost: true}))
	w := httptest.NewRecorder()
	h.UpdateEvent(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}

	// Verify all requests remain in their exact original states - no partial changes!
	got1, _ := qStore.GetRequest(context.Background(), "r1")
	got2, _ := qStore.GetRequest(context.Background(), "r2")
	got3, _ := qStore.GetRequest(context.Background(), "r3")
	if got1.Rank != 100 || got1.Round != 0 {
		t.Fatalf("r1 modified on failed transition: %+v", got1)
	}
	if got2.Rank != 200 || got2.Round != 0 {
		t.Fatalf("r2 modified on failed transition: %+v", got2)
	}
	if got3.Rank != 300 || got3.Round != 0 {
		t.Fatalf("r3 modified on failed transition: %+v", got3)
	}
	savedEv, _ := evStore.GetEvent(context.Background(), event.ID)
	if savedEv.RotationMode != models.RotationModeFairPriority {
		t.Fatalf("event rotation mode changed on failed transition: %s", savedEv.RotationMode)
	}
}

func TestUpdateEvent_CombinedModeAndCurrentRoundUpdate(t *testing.T) {
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	evStore.transition = qStore
	h := &EventHandler{Events: evStore, Requests: qStore}

	event := &models.Event{
		ID:           "ev-combined-round",
		OwnerID:      "host-user-1",
		RotationMode: models.RotationModeFairPriority,
		CurrentRound: 1,
		Status:       models.EventStatusActive,
	}
	_ = evStore.CreateEvent(context.Background(), event)

	now := time.Now().UTC()
	rPend := &models.SongRequest{ID: "rPend", EventID: event.ID, SingerName: "Alice", Status: models.StatusPending, Round: 0, CreatedAt: now}
	_ = qStore.CreateRequest(context.Background(), rPend)

	// Host sends combined update: switch to round_robin AND current_round = 4
	body := `{"rotation_mode":"round_robin","current_round":4}`
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-combined-round", bytes.NewBufferString(body))
	r.SetPathValue("id", event.ID)
	r = r.WithContext(context.WithValue(r.Context(), userContextKey, &models.User{ID: "host-user-1", IsHost: true}))
	w := httptest.NewRecorder()
	h.UpdateEvent(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on combined update, got %d: %s", w.Code, w.Body.String())
	}

	savedEvent, _ := evStore.GetEvent(context.Background(), event.ID)
	if savedEvent.CurrentRound != 4 {
		t.Fatalf("expected event CurrentRound 4, got %d", savedEvent.CurrentRound)
	}

	upPend, _ := qStore.GetRequest(context.Background(), "rPend")
	if upPend.Round != 4 {
		t.Fatalf("expected pending request Round to be normalized to updated round 4, got %d", upPend.Round)
	}
}

func TestUpdateEvent_RotationModeTransitionExceedsAtomicLimit(t *testing.T) {
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	evStore.transition = qStore
	h := &EventHandler{Events: evStore, Requests: qStore}

	event := &models.Event{
		ID:           "ev-limit",
		OwnerID:      "host-user-1",
		RotationMode: models.RotationModeFairPriority,
		CurrentRound: 1,
		Status:       models.EventStatusActive,
	}
	_ = evStore.CreateEvent(context.Background(), event)

	for i := 0; i < 100; i++ {
		req := &models.SongRequest{
			ID:         fmt.Sprintf("req-%d", i),
			EventID:    event.ID,
			SingerName: fmt.Sprintf("Singer-%d", i),
			Status:     models.StatusAccepted,
			Rank:       int64(i + 1),
			CreatedAt:  time.Now().UTC(),
		}
		_ = qStore.CreateRequest(context.Background(), req)
	}

	body := `{"rotation_mode":"round_robin"}`
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-limit", bytes.NewBufferString(body))
	r.SetPathValue("id", event.ID)
	r = r.WithContext(context.WithValue(r.Context(), userContextKey, &models.User{ID: "host-user-1", IsHost: true}))
	w := httptest.NewRecorder()
	h.UpdateEvent(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request when exceeding atomic transition limit, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateEvent_RotationModeTransitionConcurrentConflict(t *testing.T) {
	evStore := newTestEventStore()
	condStore := &conditionalTestQueueStore{testQueueStore: newTestQueueStore(), currentVersion: 5}
	evStore.transition = condStore
	h := &EventHandler{Events: evStore, Requests: condStore}

	event := &models.Event{
		ID:           "ev-conflict",
		OwnerID:      "host-user-1",
		RotationMode: models.RotationModeFairPriority,
		CurrentRound: 1,
		Status:       models.EventStatusActive,
		QueueVersion: 4, // Stale relative to condStore.currentVersion (5)
	}
	_ = evStore.CreateEvent(context.Background(), event)

	req := &models.SongRequest{
		ID:         "r-conflict-1",
		EventID:    event.ID,
		SingerName: "Alice",
		Status:     models.StatusAccepted,
		Rank:       10,
		CreatedAt:  time.Now().UTC(),
	}
	_ = condStore.CreateRequest(context.Background(), req)

	body := `{"rotation_mode":"round_robin"}`
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-conflict", bytes.NewBufferString(body))
	r.SetPathValue("id", event.ID)
	r = r.WithContext(context.WithValue(r.Context(), userContextKey, &models.User{ID: "host-user-1", IsHost: true}))
	w := httptest.NewRecorder()
	h.UpdateEvent(w, r)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict on concurrent queue version mismatch, got %d: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("X-Queue-Version") != "5" {
		t.Fatalf("expected X-Queue-Version 5, got %s", w.Header().Get("X-Queue-Version"))
	}
}

func TestUpdateEvent_InitialRotationModeTransitionConcurrentConflict(t *testing.T) {
	evStore := newTestEventStore()
	condStore := &conditionalTestQueueStore{testQueueStore: newTestQueueStore(), currentVersion: 2}
	evStore.transition = condStore
	h := &EventHandler{Events: evStore, Requests: condStore}

	// New event created with initial QueueVersion 1, but concurrent mutation bumped queue version to 2
	event := &models.Event{
		ID:           "ev-initial-conflict",
		OwnerID:      "host-user-1",
		RotationMode: models.RotationModeFairPriority,
		CurrentRound: 1,
		Status:       models.EventStatusActive,
		QueueVersion: 1,
	}
	_ = evStore.CreateEvent(context.Background(), event)

	req := &models.SongRequest{
		ID:         "r-initial-1",
		EventID:    event.ID,
		SingerName: "Alice",
		Status:     models.StatusAccepted,
		Rank:       1,
		CreatedAt:  time.Now().UTC(),
	}
	_ = condStore.CreateRequest(context.Background(), req)

	body := `{"rotation_mode":"round_robin"}`
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-initial-conflict", bytes.NewBufferString(body))
	r.SetPathValue("id", event.ID)
	r = r.WithContext(context.WithValue(r.Context(), userContextKey, &models.User{ID: "host-user-1", IsHost: true}))
	w := httptest.NewRecorder()
	h.UpdateEvent(w, r)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict on initial mode transition with concurrent version bump, got %d: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("X-Queue-Version") != "2" {
		t.Fatalf("expected X-Queue-Version 2, got %s", w.Header().Get("X-Queue-Version"))
	}
}

func TestEventHandler_SingerWeights_HostOnlyAndNeverLeakedPublicly(t *testing.T) {
	evStore := newTestEventStore()
	sbStore := newTestStore()
	qStore := newTestQueueStore()
	eventHandler := &EventHandler{Events: evStore, Songbooks: sbStore, Requests: qStore}
	queueHandler := &QueueHandler{Events: evStore, Songbooks: sbStore, Requests: qStore}

	sb := &models.Songbook{ID: "sb-weights", PublicID: "pub-weights", Name: "Weights Book", IsPublic: true}
	_ = sbStore.CreateSongbook(context.Background(), sb)

	ev := &models.Event{
		ID:           "ev-weights",
		OwnerID:      "host-1",
		SongbookID:   sb.ID,
		Name:         "VIP Night",
		JoinCode:     "VIPW",
		Status:       models.EventStatusActive,
		RotationMode: models.RotationModeFairPriority,
		CurrentRound: 1,
		QueueVersion: 1,
	}
	_ = evStore.CreateEvent(context.Background(), ev)
	_ = qStore.CreateRequest(context.Background(), &models.SongRequest{
		ID:         "req-alice",
		EventID:    ev.ID,
		SingerName: "Alice",
		SongTitle:  "Respect",
		SongArtist: "Aretha Franklin",
		Status:     models.StatusAccepted,
		Rank:       1,
		CreatedAt:  time.Now().UTC(),
	})

	// 1. Host sets singer weights (mixed case, whitespace, out-of-range clamping, zero omission)
	patchBody := `{"singer_weights":{"  Alice  ":15,"BOB":-20,"Zero":0,"   ":5}}`
	rPatch := httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-weights", bytes.NewBufferString(patchBody))
	rPatch.SetPathValue("id", ev.ID)
	rPatch = rPatch.WithContext(context.WithValue(rPatch.Context(), userContextKey, &models.User{ID: "host-1", IsHost: true}))
	wPatch := httptest.NewRecorder()
	eventHandler.RequireOwnership(eventHandler.UpdateEvent)(wPatch, rPatch)
	if wPatch.Code != http.StatusOK {
		t.Fatalf("host PATCH singer_weights failed: %d: %s", wPatch.Code, wPatch.Body.String())
	}

	var updated models.Event
	if err := json.Unmarshal(wPatch.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated event: %v", err)
	}
	if len(updated.SingerWeights) != 2 || updated.SingerWeights["alice"] != 10 || updated.SingerWeights["bob"] != -10 {
		t.Fatalf("expected normalized/clamped singer_weights {alice:10, bob:-10}, got %+v", updated.SingerWeights)
	}

	// 2. Host PATCH omitting singer_weights preserves existing weights
	rAnn := httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-weights", bytes.NewBufferString(`{"announcement":"Last call!"}`))
	rAnn.SetPathValue("id", ev.ID)
	rAnn = rAnn.WithContext(context.WithValue(rAnn.Context(), userContextKey, &models.User{ID: "host-1", IsHost: true}))
	wAnn := httptest.NewRecorder()
	eventHandler.RequireOwnership(eventHandler.UpdateEvent)(wAnn, rAnn)
	if wAnn.Code != http.StatusOK {
		t.Fatalf("host PATCH announcement failed: %d", wAnn.Code)
	}
	var afterAnn models.Event
	_ = json.Unmarshal(wAnn.Body.Bytes(), &afterAnn)
	if len(afterAnn.SingerWeights) != 2 || afterAnn.SingerWeights["alice"] != 10 || afterAnn.SingerWeights["bob"] != -10 {
		t.Fatalf("expected singer_weights preserved on unrelated PATCH, got %+v", afterAnn.SingerWeights)
	}

	// 3. Unauthenticated / non-owner cannot read or write host event endpoint
	rUnauth := httptest.NewRequest(http.MethodGet, "/api/v1/events/ev-weights", nil)
	rUnauth.SetPathValue("id", ev.ID)
	wUnauth := httptest.NewRecorder()
	eventHandler.RequireOwnership(eventHandler.GetEvent)(wUnauth, rUnauth)
	if wUnauth.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unauthenticated host GetEvent, got %d", wUnauth.Code)
	}

	// 4. Public event endpoint NEVER leaks singer_weights
	rPubEvent := httptest.NewRequest(http.MethodGet, "/api/v1/events/ev-weights/public", nil)
	rPubEvent.SetPathValue("id", ev.ID)
	wPubEvent := httptest.NewRecorder()
	eventHandler.GetPublicEvent(wPubEvent, rPubEvent)
	if wPubEvent.Code != http.StatusOK {
		t.Fatalf("GetPublicEvent failed: %d", wPubEvent.Code)
	}
	if bytes.Contains(wPubEvent.Body.Bytes(), []byte("singer_weights")) {
		t.Fatalf("GetPublicEvent leaked singer_weights: %s", wPubEvent.Body.String())
	}

	// 5. Public join-code resolution endpoint NEVER leaks singer_weights
	rCode := httptest.NewRequest(http.MethodGet, "/api/v1/codes/VIPW", nil)
	rCode.SetPathValue("code", "VIPW")
	wCode := httptest.NewRecorder()
	eventHandler.GetEventByCode(wCode, rCode)
	if wCode.Code != http.StatusOK {
		t.Fatalf("GetEventByCode failed: %d", wCode.Code)
	}
	if bytes.Contains(wCode.Body.Bytes(), []byte("singer_weights")) {
		t.Fatalf("GetEventByCode leaked singer_weights: %s", wCode.Body.String())
	}

	// 6. Public queue endpoint NEVER leaks singer_weights
	rPubQueue := httptest.NewRequest(http.MethodGet, "/api/v1/events/ev-weights/queue", nil)
	rPubQueue.SetPathValue("id", ev.ID)
	wPubQueue := httptest.NewRecorder()
	queueHandler.GetEventPublicQueue(wPubQueue, rPubQueue)
	if wPubQueue.Code != http.StatusOK {
		t.Fatalf("GetEventPublicQueue failed: %d", wPubQueue.Code)
	}
	if bytes.Contains(wPubQueue.Body.Bytes(), []byte("singer_weights")) {
		t.Fatalf("GetEventPublicQueue leaked singer_weights: %s", wPubQueue.Body.String())
	}

	// 7. Conflicting case/whitespace singer_weights aliases are rejected with 400 Bad Request
	rConflict := httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-weights", bytes.NewBufferString(`{"singer_weights":{"Alice":3," alice ":-4}}`))
	rConflict.SetPathValue("id", ev.ID)
	rConflict = rConflict.WithContext(context.WithValue(rConflict.Context(), userContextKey, &models.User{ID: "host-1", IsHost: true}))
	wConflict := httptest.NewRecorder()
	eventHandler.RequireOwnership(eventHandler.UpdateEvent)(wConflict, rConflict)
	if wConflict.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for conflicting singer_weights aliases, got %d: %s", wConflict.Code, wConflict.Body.String())
	}

	// 8. Matching case/whitespace singer_weights aliases are accepted and collapsed deterministically
	rSameAlias := httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-weights", bytes.NewBufferString(`{"singer_weights":{"Alice":3," alice ":3}}`))
	rSameAlias.SetPathValue("id", ev.ID)
	rSameAlias = rSameAlias.WithContext(context.WithValue(rSameAlias.Context(), userContextKey, &models.User{ID: "host-1", IsHost: true}))
	wSameAlias := httptest.NewRecorder()
	eventHandler.RequireOwnership(eventHandler.UpdateEvent)(wSameAlias, rSameAlias)
	if wSameAlias.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for agreeing singer_weights aliases, got %d: %s", wSameAlias.Code, wSameAlias.Body.String())
	}
	var collapsed models.Event
	_ = json.Unmarshal(wSameAlias.Body.Bytes(), &collapsed)
	if len(collapsed.SingerWeights) != 1 || collapsed.SingerWeights["alice"] != 3 {
		t.Fatalf("expected collapsed singer_weights {alice:3}, got %+v", collapsed.SingerWeights)
	}

	// 9. Host can clear singer_weights with empty map {}
	rClear := httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-weights", bytes.NewBufferString(`{"singer_weights":{}}`))
	rClear.SetPathValue("id", ev.ID)
	rClear = rClear.WithContext(context.WithValue(rClear.Context(), userContextKey, &models.User{ID: "host-1", IsHost: true}))
	wClear := httptest.NewRecorder()
	eventHandler.RequireOwnership(eventHandler.UpdateEvent)(wClear, rClear)
	if wClear.Code != http.StatusOK {
		t.Fatalf("host PATCH clear singer_weights failed: %d", wClear.Code)
	}
	var cleared models.Event
	_ = json.Unmarshal(wClear.Body.Bytes(), &cleared)
	if len(cleared.SingerWeights) != 0 {
		t.Fatalf("expected cleared singer_weights to be empty, got %+v", cleared.SingerWeights)
	}
}
