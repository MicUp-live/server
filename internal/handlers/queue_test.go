package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type testQueueStore struct {
	store.RequestStore
	requests     map[string]*models.SongRequest
	order        []string
	failUpdate   error
	failReorder  error
	admissionErr error
	events       store.EventStore
}

type failingDeleteLookupStore struct {
	*testQueueStore
}

func (s *failingDeleteLookupStore) GetRequest(context.Context, string) (*models.SongRequest, error) {
	return nil, fmt.Errorf("request store unavailable")
}

type conditionalTestQueueStore struct {
	*testQueueStore
	currentVersion int64
	idempotency    map[string]fakeIdempotencyRecord
}

type fakeIdempotencyRecord struct {
	ownerID, route, entityID, requestHash string
	result                                *store.QueueMutationResult
}

func (s *conditionalTestQueueStore) GetRequest(ctx context.Context, id string) (*models.SongRequest, error) {
	req, err := s.testQueueStore.GetRequest(ctx, id)
	if err != nil {
		return nil, err
	}
	copy := *req
	return &copy, nil
}

func (s *conditionalTestQueueStore) UpdateRequestWithQueueVersion(ctx context.Context, req *models.SongRequest, expected int64) error {
	return s.UpdateRequestAndReorderWithQueueVersion(ctx, req, nil, expected)
}

func (s *conditionalTestQueueStore) UpdateRequestAndReorderWithQueueVersion(_ context.Context, req *models.SongRequest, reorderIDs []string, expected int64) error {
	if expected != s.currentVersion {
		return &store.QueueVersionConflictError{Current: s.currentVersion}
	}
	if len(reorderIDs) > 0 {
		if err := s.testQueueStore.UpdateRequestAndReorder(context.Background(), req, reorderIDs); err != nil {
			return err
		}
	} else {
		if err := s.testQueueStore.UpdateRequest(context.Background(), req); err != nil {
			return err
		}
		if s.testQueueStore.events != nil {
			_, _ = s.testQueueStore.events.BumpQueueVersion(context.Background(), req.EventID)
		}
	}
	s.currentVersion++
	return nil
}

func (s *conditionalTestQueueStore) UpdateRequestAndReorder(ctx context.Context, req *models.SongRequest, reorderIDs []string) error {
	if len(reorderIDs) > 0 {
		if err := s.testQueueStore.UpdateRequestAndReorder(ctx, req, reorderIDs); err != nil {
			return err
		}
	} else {
		if err := s.testQueueStore.UpdateRequest(ctx, req); err != nil {
			return err
		}
		if s.testQueueStore.events != nil {
			_, _ = s.testQueueStore.events.BumpQueueVersion(ctx, req.EventID)
		}
	}
	s.currentVersion++
	return nil
}

func (s *conditionalTestQueueStore) TransitionQueueRotation(ctx context.Context, event *models.Event, requests []*models.SongRequest) error {
	if event != nil && event.QueueVersion != s.currentVersion {
		return &store.QueueVersionConflictError{Current: s.currentVersion}
	}
	if len(requests) > 99 {
		return fmt.Errorf("queue exceeds maximum atomic transition size of 99 requests: %w", os.ErrInvalid)
	}
	if err := s.testQueueStore.TransitionQueueRotation(ctx, event, requests); err != nil {
		return err
	}
	s.currentVersion++
	return nil
}

func (s *conditionalTestQueueStore) UpdateRequestWithIdempotency(ctx context.Context, req *models.SongRequest, expected *int64, key, ownerID, route, requestHash string) (*store.QueueMutationResult, error) {
	return s.UpdateRequestAndReorderWithIdempotency(ctx, req, nil, expected, key, ownerID, route, requestHash)
}

func (s *conditionalTestQueueStore) UpdateRequestAndReorderWithIdempotency(_ context.Context, req *models.SongRequest, reorderIDs []string, expected *int64, key, ownerID, route, requestHash string) (*store.QueueMutationResult, error) {
	if replay, found, err := s.replayIdempotency(key, ownerID, route, req.ID, requestHash); found || err != nil {
		return replay, err
	}
	version := s.currentVersion
	if expected != nil && *expected != version {
		return nil, &store.QueueVersionConflictError{Current: version}
	}
	if len(reorderIDs) > 0 {
		if err := s.testQueueStore.UpdateRequestAndReorder(context.Background(), req, reorderIDs); err != nil {
			return nil, err
		}
	} else {
		if err := s.testQueueStore.UpdateRequest(context.Background(), req); err != nil {
			return nil, err
		}
	}
	s.currentVersion++
	body, _ := json.Marshal(req)
	result := &store.QueueMutationResult{StatusCode: http.StatusOK, Body: body, QueueVersion: s.currentVersion}
	s.saveIdempotency(key, ownerID, route, req.ID, requestHash, result)
	return result, nil
}

func (s *conditionalTestQueueStore) ReorderRequestsWithQueueVersion(_ context.Context, eventID string, ids []string, expected int64) error {
	if expected != s.currentVersion {
		return &store.QueueVersionConflictError{Current: s.currentVersion}
	}
	if err := s.testQueueStore.ReorderRequests(context.Background(), eventID, ids); err != nil {
		return err
	}
	if len(ids) > 0 {
		s.currentVersion++
	}
	return nil
}

func (s *conditionalTestQueueStore) GetIdempotencyResult(_ context.Context, key, ownerID, route, entityID, requestHash string) (*store.QueueMutationResult, bool, error) {
	return s.replayIdempotency(key, ownerID, route, entityID, requestHash)
}

func (s *conditionalTestQueueStore) ReorderRequestsWithIdempotency(_ context.Context, eventID string, ids []string, expected *int64, key, ownerID, route, requestHash string) (*store.QueueMutationResult, error) {
	if replay, found, err := s.replayIdempotency(key, ownerID, route, eventID, requestHash); found || err != nil {
		return replay, err
	}
	version := s.currentVersion
	if expected != nil && *expected != version {
		return nil, &store.QueueVersionConflictError{Current: version}
	}
	if err := s.testQueueStore.ReorderRequests(context.Background(), eventID, ids); err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		s.currentVersion++
	}
	body := []byte(`{"status":"ok"}`)
	result := &store.QueueMutationResult{StatusCode: http.StatusOK, Body: body, QueueVersion: s.currentVersion}
	s.saveIdempotency(key, ownerID, route, eventID, requestHash, result)
	return result, nil
}

func (s *conditionalTestQueueStore) replayIdempotency(key, ownerID, route, entityID, requestHash string) (*store.QueueMutationResult, bool, error) {
	if s.idempotency == nil {
		s.idempotency = make(map[string]fakeIdempotencyRecord)
	}
	record, ok := s.idempotency[key]
	if !ok {
		return nil, false, nil
	}
	if record.ownerID != ownerID || record.route != route || record.entityID != entityID || record.requestHash != requestHash {
		return nil, true, &store.IdempotencyConflictError{}
	}
	result := *record.result
	result.Body = append([]byte(nil), result.Body...)
	result.Replay = true
	return &result, true, nil
}

func (s *conditionalTestQueueStore) saveIdempotency(key, ownerID, route, entityID, requestHash string, result *store.QueueMutationResult) {
	if s.idempotency == nil {
		s.idempotency = make(map[string]fakeIdempotencyRecord)
	}
	s.idempotency[key] = fakeIdempotencyRecord{ownerID: ownerID, route: route, entityID: entityID, requestHash: requestHash, result: result}
}

func newTestQueueStore() *testQueueStore {
	return &testQueueStore{
		requests: make(map[string]*models.SongRequest),
		order:    []string{},
	}
}

func (s *testQueueStore) CreateRequest(ctx context.Context, req *models.SongRequest) error {
	req.CreatedAt = time.Now().UTC()
	req.UpdatedAt = time.Now().UTC()
	s.requests[req.ID] = req
	s.order = append(s.order, req.ID)
	return nil
}

func (s *testQueueStore) CreateRequestAtVersion(ctx context.Context, req *models.SongRequest, maxActivePerSinger int, reorderIDs []string, expectedVersion int64) error {
	if s.admissionErr != nil {
		return s.admissionErr
	}
	if maxActivePerSinger > 0 {
		normSinger := strings.ToLower(strings.TrimSpace(req.SingerName))
		activeCount := 0
		for _, er := range s.requests {
			if er.EventID == req.EventID && strings.ToLower(strings.TrimSpace(er.SingerName)) == normSinger {
				if er.Status == models.StatusPending || er.Status == models.StatusAccepted || er.Status == models.StatusUpNext {
					activeCount++
				}
			}
		}
		if activeCount >= maxActivePerSinger {
			return store.ErrSingerLimitExceeded
		}
	}
	if err := s.CreateRequest(ctx, req); err != nil {
		return err
	}
	if len(reorderIDs) > 0 {
		if err := s.ReorderRequests(ctx, req.EventID, reorderIDs); err != nil {
			return err
		}
	}
	if s.events != nil {
		if _, err := s.events.BumpQueueVersion(ctx, req.EventID); err != nil {
			return err
		}
	}
	return nil
}

func (s *testQueueStore) GetRequest(ctx context.Context, id string) (*models.SongRequest, error) {
	req, ok := s.requests[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	cp := *req
	return &cp, nil
}

func (s *testQueueStore) ListRequestsByEvent(ctx context.Context, eventID string) ([]*models.SongRequest, error) {
	var list []*models.SongRequest
	seen := make(map[string]bool)
	for _, id := range s.order {
		if req, ok := s.requests[id]; ok && (req.EventID == eventID || req.SongbookID == eventID) {
			cp := *req
			list = append(list, &cp)
			seen[id] = true
		}
	}
	for id, req := range s.requests {
		if !seen[id] && (req.EventID == eventID || req.SongbookID == eventID) {
			cp := *req
			list = append(list, &cp)
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Rank != list[j].Rank {
			return list[i].Rank < list[j].Rank
		}
		return list[i].CreatedAt.Before(list[j].CreatedAt)
	})
	return list, nil
}

func (s *testQueueStore) UpdateRequest(ctx context.Context, req *models.SongRequest) error {
	if s.failUpdate != nil {
		return s.failUpdate
	}
	if _, ok := s.requests[req.ID]; !ok {
		return os.ErrNotExist
	}
	cp := *req
	cp.UpdatedAt = time.Now().UTC()
	s.requests[req.ID] = &cp
	return nil
}

func (s *testQueueStore) DeleteRequest(ctx context.Context, id string) error {
	delete(s.requests, id)
	return nil
}

func (s *testQueueStore) SetRequestsTTLByEvent(_ context.Context, eventID string, expiresAt time.Time) error {
	for _, req := range s.requests {
		if req.EventID == eventID {
			if expiresAt.IsZero() {
				req.TTL = 0
			} else {
				req.TTL = expiresAt.Unix()
			}
		}
	}
	return nil
}

func (s *testQueueStore) DeleteEventRequestAtomic(ctx context.Context, eventID, id string) error {
	req, ok := s.requests[id]
	if !ok {
		return nil
	}
	if req.EventID != eventID {
		return os.ErrNotExist
	}
	if s.events == nil {
		return fmt.Errorf("event version store is required for atomic deletion")
	}
	if _, err := s.events.BumpQueueVersion(ctx, eventID); err != nil {
		return err
	}
	delete(s.requests, id)
	return nil
}

func (s *testQueueStore) ReorderRequests(ctx context.Context, scopeID string, orderedIDs []string) error {
	if s.failReorder != nil {
		return s.failReorder
	}
	for _, id := range orderedIDs {
		req, ok := s.requests[id]
		if !ok {
			return fmt.Errorf("request %s not found: %w", id, os.ErrNotExist)
		}
		if req.EventID != scopeID && req.SongbookID != scopeID {
			return fmt.Errorf("request %s does not belong to scope %s: %w", id, scopeID, os.ErrPermission)
		}
	}
	s.order = orderedIDs
	for i, id := range orderedIDs {
		if req, ok := s.requests[id]; ok {
			if req.Round > 0 {
				req.Rank = int64(req.Round)*models.RoundRankMultiplier + int64(i+1)
			} else {
				req.Rank = int64(i + 1)
			}
		}
	}
	return nil
}

func (s *testQueueStore) UpdateRequestAndReorder(ctx context.Context, req *models.SongRequest, reorderIDs []string) error {
	if s.failUpdate != nil {
		return s.failUpdate
	}
	if s.failReorder != nil {
		return s.failReorder
	}
	for _, id := range reorderIDs {
		if id == req.ID {
			continue
		}
		if _, ok := s.requests[id]; !ok {
			return fmt.Errorf("request %s not found: %w", id, os.ErrNotExist)
		}
	}
	if len(reorderIDs) == 0 {
		if err := s.UpdateRequest(ctx, req); err != nil {
			return err
		}
		if s.events != nil {
			_, _ = s.events.BumpQueueVersion(ctx, req.EventID)
		}
		return nil
	}
	s.order = reorderIDs
	for i, id := range reorderIDs {
		rank := int64(i + 1)
		if id == req.ID {
			if req.Round > 0 {
				rank = int64(req.Round)*models.RoundRankMultiplier + int64(i+1)
			}
			req.Rank = rank
			req.UpdatedAt = time.Now().UTC()
			s.requests[req.ID] = req
		} else {
			if r, ok := s.requests[id]; ok {
				if r.Round > 0 {
					rank = int64(r.Round)*models.RoundRankMultiplier + int64(i+1)
				}
				r.Rank = rank
				r.UpdatedAt = time.Now().UTC()
			}
		}
	}
	if s.events != nil {
		_, _ = s.events.BumpQueueVersion(ctx, req.EventID)
	}
	return nil
}

func (s *testQueueStore) TransitionQueueRotation(ctx context.Context, event *models.Event, requests []*models.SongRequest) error {
	if s.failUpdate != nil {
		return s.failUpdate
	}
	if s.failReorder != nil {
		return s.failReorder
	}
	if len(requests) > 99 {
		return fmt.Errorf("queue exceeds maximum atomic transition size of 99 requests: %w", os.ErrInvalid)
	}
	for _, req := range requests {
		if _, ok := s.requests[req.ID]; !ok {
			return fmt.Errorf("request %s not found: %w", req.ID, os.ErrNotExist)
		}
	}
	var newOrder []string
	for _, req := range requests {
		req.UpdatedAt = time.Now().UTC()
		s.requests[req.ID] = req
		if req.Status == models.StatusAccepted || req.Status == models.StatusUpNext {
			newOrder = append(newOrder, req.ID)
		}
	}
	if len(newOrder) > 0 {
		s.order = newOrder
	}
	event.QueueVersion++
	return nil
}

func TestQueueHandler_SubmissionConflictReturns409(t *testing.T) {
	songbooks := newTestStore()
	events := newTestEventStore()
	requests := newTestQueueStore()
	requests.admissionErr = store.ErrAdmissionConflict
	handler := &QueueHandler{Songbooks: songbooks, Events: events, Requests: requests}
	if err := songbooks.CreateSongbook(context.Background(), &models.Songbook{
		ID: "book-1", PublicID: "public-1", IsPublic: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := events.CreateEvent(context.Background(), &models.Event{
		ID: "event-1", SongbookID: "book-1", Status: models.EventStatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events/event-1/requests", strings.NewReader(`{"singer_name":"Singer","song_title":"Song"}`))
	req.SetPathValue("id", "event-1")
	w := httptest.NewRecorder()
	handler.CreateEventRequest(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("submission status=%d body=%q, want 409", w.Code, w.Body.String())
	}
	if len(requests.requests) != 0 {
		t.Fatalf("conflicted submission committed %d requests", len(requests.requests))
	}
}

func TestQueueHandler_CreateEventRequest_EmptySingerName(t *testing.T) {
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	handler := &QueueHandler{Events: evStore, Requests: qStore}
	_ = evStore.CreateEvent(context.Background(), &models.Event{
		ID: "ev-1", SongbookID: "sb-1", Status: models.EventStatusActive, QueueVersion: 1,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-1/requests", strings.NewReader(`{"song_title":"Creep","singer_name":""}`))
	req.SetPathValue("id", "ev-1")
	w := httptest.NewRecorder()
	handler.CreateEventRequest(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty singer name, got %d: %s", w.Code, w.Body.String())
	}
}

func TestQueueHandler_GetEventPublicQueueSinceVersion(t *testing.T) {
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	handler := &QueueHandler{Events: evStore, Requests: qStore}
	event := &models.Event{ID: "ev-1", SongbookID: "sb-1", Status: models.EventStatusActive, QueueVersion: 2}
	_ = evStore.CreateEvent(context.Background(), event)
	_ = qStore.CreateRequest(context.Background(), &models.SongRequest{ID: "pending", EventID: "ev-1", Status: models.StatusPending})
	_ = qStore.CreateRequest(context.Background(), &models.SongRequest{ID: "accepted", EventID: "ev-1", Status: models.StatusAccepted})
	_ = qStore.CreateRequest(context.Background(), &models.SongRequest{ID: "up-next", EventID: "ev-1", Status: models.StatusUpNext})
	_ = qStore.CreateRequest(context.Background(), &models.SongRequest{ID: "completed", EventID: "ev-1", Status: models.StatusCompleted})

	unchanged := httptest.NewRequest(http.MethodGet, "/api/v1/events/ev-1/queue?since=2", nil)
	unchanged.SetPathValue("id", "ev-1")
	w := httptest.NewRecorder()
	handler.GetEventPublicQueue(w, unchanged)
	if w.Code != http.StatusNoContent || w.Header().Get("X-Queue-Version") != "2" {
		t.Fatalf("expected unchanged 204 at version 2, got %d version %q", w.Code, w.Header().Get("X-Queue-Version"))
	}

	changed := httptest.NewRequest(http.MethodGet, "/api/v1/events/ev-1/queue?since=1", nil)
	changed.SetPathValue("id", "ev-1")
	w = httptest.NewRecorder()
	handler.GetEventPublicQueue(w, changed)
	if w.Code != http.StatusOK {
		t.Fatalf("expected changed 200, got %d", w.Code)
	}
	var response struct {
		QueueVersion int64                    `json:"queue_version"`
		Queue        []models.PublicQueueItem `json:"queue"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.QueueVersion != 2 {
		t.Fatalf("expected queue version 2, got %+v (err: %v)", response, err)
	}
	if len(response.Queue) != 2 {
		t.Fatalf("expected only 2 host-approved active event requests, got %+v", response.Queue)
	}
	for _, item := range response.Queue {
		if item.Status != models.StatusAccepted && item.Status != models.StatusUpNext {
			t.Fatalf("unexpected non-live status in public queue: %+v", item)
		}
	}
}

func TestQueueHandler_HostManageAndReorder(t *testing.T) {
	sbStore := newTestStore()
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	handler := &QueueHandler{Songbooks: sbStore, Events: evStore, Requests: qStore}

	sb := &models.Songbook{ID: "sb-1", Name: "Host Night", OwnerID: "owner-1"}
	_ = sbStore.CreateSongbook(context.Background(), sb)
	event := &models.Event{ID: "ev-1", OwnerID: "owner-1", SongbookID: "sb-1", Name: "Host Night Live", Status: models.EventStatusActive}
	_ = evStore.CreateEvent(context.Background(), event)

	_ = qStore.CreateRequest(context.Background(), &models.SongRequest{
		ID: "req-1", EventID: "ev-1", SongbookID: "sb-1", SongTitle: "Song 1", SingerName: "Alice", Status: models.StatusPending,
	})
	_ = qStore.CreateRequest(context.Background(), &models.SongRequest{
		ID: "req-2", EventID: "ev-1", SongbookID: "sb-1", SongTitle: "Song 2", SingerName: "Bob", Status: models.StatusPending,
	})

	// Host lists requests
	hostUser := &models.User{ID: "owner-1", IsHost: true}
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/events/ev-1/requests", nil)
	listReq = listReq.WithContext(context.WithValue(listReq.Context(), userContextKey, hostUser))
	listReq.SetPathValue("id", "ev-1")
	w := httptest.NewRecorder()
	handler.ListEventRequests(w, listReq)
	if w.Code != http.StatusOK {
		t.Fatalf("host list failed: %d", w.Code)
	}

	// Host bumps Bob to Up Next
	statusUpNext := models.StatusUpNext
	body, _ := json.Marshal(models.RequestUpdate{Status: &statusUpNext})
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-1/requests/req-2", bytes.NewReader(body))
	patchReq = patchReq.WithContext(context.WithValue(patchReq.Context(), userContextKey, hostUser))
	patchReq.SetPathValue("id", "ev-1")
	patchReq.SetPathValue("requestID", "req-2")
	w = httptest.NewRecorder()
	handler.UpdateEventRequest(w, patchReq)
	if w.Code != http.StatusOK {
		t.Fatalf("patch request failed: %d: %s", w.Code, w.Body.String())
	}

	// Host reorders [req-2, req-1]
	orderBody, _ := json.Marshal(map[string][]string{"order": {"req-2", "req-1"}})
	reorderReq := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-1/requests/reorder", bytes.NewReader(orderBody))
	reorderReq = reorderReq.WithContext(context.WithValue(reorderReq.Context(), userContextKey, hostUser))
	reorderReq.SetPathValue("id", "ev-1")
	w = httptest.NewRecorder()
	handler.ReorderEventRequests(w, reorderReq)
	if w.Code != http.StatusOK {
		t.Fatalf("reorder failed: %d", w.Code)
	}

	// Host deletes req-1
	qStore.events = evStore
	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/events/ev-1/requests/req-1", nil)
	delReq = delReq.WithContext(context.WithValue(delReq.Context(), userContextKey, hostUser))
	delReq.SetPathValue("id", "ev-1")
	delReq.SetPathValue("requestID", "req-1")
	w = httptest.NewRecorder()
	handler.DeleteRequest(w, delReq)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete failed: %d", w.Code)
	}
	updatedEvent, err := evStore.GetEvent(context.Background(), event.ID)
	if err != nil || updatedEvent.QueueVersion != 1 {
		t.Fatalf("host deletion version = %v, error = %v; want 1", updatedEvent, err)
	}
	w = httptest.NewRecorder()
	handler.DeleteRequest(w, delReq)
	if w.Code != http.StatusNotFound {
		t.Fatalf("repeated deletion = %d, want 404", w.Code)
	}
}

func TestQueueHandler_DeleteRequestAuthorization(t *testing.T) {
	sbStore := newTestStore()
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	handler := &QueueHandler{
		Songbooks: sbStore,
		Events:    evStore,
		Requests:  qStore,
		Secret:    []byte("test-auth-secret-key-at-least-32b!"),
	}

	sb := &models.Songbook{ID: "sb-sec", Name: "Secure Catalog", OwnerID: "host-sec"}
	_ = sbStore.CreateSongbook(context.Background(), sb)
	event := &models.Event{ID: "ev-sec", OwnerID: "host-sec", SongbookID: "sb-sec", Name: "Secure Show", Status: models.EventStatusActive}
	_ = evStore.CreateEvent(context.Background(), event)

	// 1. Singer creates a request
	createBody, _ := json.Marshal(models.RequestCreate{
		SongTitle:  "Bohemian Rhapsody",
		SongArtist: "Queen",
		SingerName: "Freddie",
	})
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-sec/requests", bytes.NewReader(createBody))
	createReq.SetPathValue("id", "ev-sec")
	w := httptest.NewRecorder()
	handler.CreateEventRequest(w, createReq)
	if w.Code != http.StatusCreated {
		t.Fatalf("create request failed: %d: %s", w.Code, w.Body.String())
	}
	var created models.SongRequest
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.CancelToken == "" {
		t.Fatal("expected cancel_token to be returned in creation response")
	}

	// 2. Unauthenticated attacker tries to delete the request without token or auth -> must be 403
	attReq := httptest.NewRequest(http.MethodDelete, "/api/v1/events/ev-sec/requests/"+created.ID, nil)
	attReq.SetPathValue("id", "ev-sec")
	attReq.SetPathValue("requestID", created.ID)
	w = httptest.NewRecorder()
	handler.DeleteRequest(w, attReq)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for unauthorized delete, got %d", w.Code)
	}

	// 3. Delete with invalid cancel token -> must be 403
	badReq := httptest.NewRequest(http.MethodDelete, "/api/v1/events/ev-sec/requests/"+created.ID, nil)
	badReq.Header.Set("X-Cancel-Token", "fake.token")
	badReq.SetPathValue("id", "ev-sec")
	badReq.SetPathValue("requestID", created.ID)
	w = httptest.NewRecorder()
	handler.DeleteRequest(w, badReq)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for invalid cancel token, got %d", w.Code)
	}

	// 4. Singer deletes with valid cancel token in X-Cancel-Token header -> must be 204
	qStore.events = evStore
	validReq := httptest.NewRequest(http.MethodDelete, "/api/v1/events/ev-sec/requests/"+created.ID, nil)
	validReq.Header.Set("X-Cancel-Token", created.CancelToken)
	validReq.SetPathValue("id", "ev-sec")
	validReq.SetPathValue("requestID", created.ID)
	w = httptest.NewRecorder()
	handler.DeleteRequest(w, validReq)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 No Content for authorized delete with cancel token, got %d", w.Code)
	}
	updatedEvent, err := evStore.GetEvent(context.Background(), event.ID)
	if err != nil || updatedEvent.QueueVersion != 1 {
		t.Fatalf("singer cancellation version = %v, error = %v; want 1", updatedEvent, err)
	}
}

func TestQueueHandler_DeleteEventRequestAsHost(t *testing.T) {
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	qStore.events = evStore
	handler := &QueueHandler{Events: evStore, Requests: qStore}
	event := &models.Event{ID: "event-1", OwnerID: "host-1", Status: models.EventStatusActive, QueueVersion: 4}
	_ = evStore.CreateEvent(context.Background(), event)
	_ = qStore.CreateRequest(context.Background(), &models.SongRequest{ID: "req-1", EventID: event.ID})
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/events/event-1/requests/req-1", nil)
	r.SetPathValue("id", event.ID)
	r.SetPathValue("requestID", "req-1")
	r = r.WithContext(context.WithValue(r.Context(), userContextKey, &models.User{ID: event.OwnerID, IsHost: true}))
	w := httptest.NewRecorder()
	handler.DeleteRequest(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("host deletion = %d: %s", w.Code, w.Body.String())
	}
	updated, err := evStore.GetEvent(context.Background(), event.ID)
	if err != nil || updated.QueueVersion != 5 {
		t.Fatalf("host deletion event = %v, error = %v; want version 5", updated, err)
	}
}

func TestQueueHandler_DeleteRequestLookupFailureReturnsServerError(t *testing.T) {
	h := &QueueHandler{Requests: &failingDeleteLookupStore{testQueueStore: newTestQueueStore()}}
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/events/event-1/requests/req-1", nil)
	r.SetPathValue("id", "event-1")
	r.SetPathValue("requestID", "req-1")
	w := httptest.NewRecorder()
	h.DeleteRequest(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("request lookup failure = %d, want 500", w.Code)
	}
}

func TestQueueHandler_CancelTokenWorksViaJoinCode(t *testing.T) {
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	qStore.events = evStore
	secret := []byte("test-secret-with-at-least-32-bytes")
	handler := &QueueHandler{Events: evStore, Requests: qStore, Secret: secret}

	ev := &models.Event{ID: "ev-join-1", OwnerID: "host-1", SongbookID: "sb-1", JoinCode: "JOIN42", Status: models.EventStatusActive}
	_ = evStore.CreateEvent(context.Background(), ev)
	reqModel := &models.SongRequest{ID: "req-join-1", EventID: ev.ID, SongbookID: "sb-1", SongTitle: "Wonderwall", SingerName: "Liam", Status: models.StatusPending}
	_ = qStore.CreateRequest(context.Background(), reqModel)

	// Token is signed with canonical ev.ID (matching queue.Service.SubmitRequest).
	token := handler.generateCancelToken(reqModel.ID, ev.ID)

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/events/JOIN42/requests/req-join-1?cancel_token="+token, nil)
	getReq.SetPathValue("id", "JOIN42")
	getReq.SetPathValue("requestID", reqModel.ID)
	wGet := httptest.NewRecorder()
	handler.GetRequest(wGet, getReq)
	if wGet.Code != http.StatusOK {
		t.Fatalf("GetRequest via join code with canonical cancel token = %d: %s", wGet.Code, wGet.Body.String())
	}

	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/events/JOIN42/requests/req-join-1?cancel_token="+token, nil)
	delReq.SetPathValue("id", "JOIN42")
	delReq.SetPathValue("requestID", reqModel.ID)
	wDel := httptest.NewRecorder()
	handler.DeleteRequest(wDel, delReq)
	if wDel.Code != http.StatusNoContent {
		t.Fatalf("DeleteRequest via join code with canonical cancel token = %d: %s", wDel.Code, wDel.Body.String())
	}
}

func TestQueueHandler_DeleteRequestRejectsCrossEventRequest(t *testing.T) {
	sbStore := newTestStore()
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	handler := &QueueHandler{Songbooks: sbStore, Events: evStore, Requests: qStore}

	_ = evStore.CreateEvent(context.Background(), &models.Event{ID: "event-a", OwnerID: "host-a", SongbookID: "book-a", Status: models.EventStatusActive})
	_ = evStore.CreateEvent(context.Background(), &models.Event{ID: "event-b", OwnerID: "host-b", SongbookID: "book-b", Status: models.EventStatusActive})
	_ = qStore.CreateRequest(context.Background(), &models.SongRequest{ID: "request-b", EventID: "event-b", SongbookID: "book-b"})

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/events/event-a/requests/request-b", nil)
	req.SetPathValue("id", "event-a")
	req.SetPathValue("requestID", "request-b")
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, &models.User{ID: "host-a", IsHost: true}))
	w := httptest.NewRecorder()
	handler.DeleteRequest(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected cross-event deletion to be rejected with 404, got %d", w.Code)
	}
	if _, err := qStore.GetRequest(context.Background(), "request-b"); err != nil {
		t.Fatal("cross-event request was deleted")
	}
}

func TestQueueHandler_ReorderRequestsTenantScoping(t *testing.T) {
	sbStore := newTestStore()
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	handler := &QueueHandler{Songbooks: sbStore, Events: evStore, Requests: qStore}

	_ = evStore.CreateEvent(context.Background(), &models.Event{ID: "event-a", OwnerID: "host-a", SongbookID: "book-a", Status: models.EventStatusActive})
	_ = evStore.CreateEvent(context.Background(), &models.Event{ID: "event-b", OwnerID: "host-b", SongbookID: "book-b", Status: models.EventStatusActive})
	_ = qStore.CreateRequest(context.Background(), &models.SongRequest{ID: "req-a1", EventID: "event-a", SongbookID: "book-a"})
	_ = qStore.CreateRequest(context.Background(), &models.SongRequest{ID: "req-b1", EventID: "event-b", SongbookID: "book-b"})

	// 1. Attempt to reorder request belonging to another event under event-a -> must be 400 Bad Request
	crossOrder, _ := json.Marshal(map[string][]string{"order": {"req-a1", "req-b1"}})
	crossReq := httptest.NewRequest(http.MethodPost, "/api/v1/events/event-a/requests/reorder", bytes.NewReader(crossOrder))
	crossReq.SetPathValue("id", "event-a")
	w := httptest.NewRecorder()
	handler.ReorderEventRequests(w, crossReq)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected cross-event reorder to be rejected with 400, got %d: %s", w.Code, w.Body.String())
	}

	// 2. Attempt to reorder non-existent request under event-a -> must be 400 Bad Request
	missingOrder, _ := json.Marshal(map[string][]string{"order": {"req-a1", "req-nonexistent"}})
	missingReq := httptest.NewRequest(http.MethodPost, "/api/v1/events/event-a/requests/reorder", bytes.NewReader(missingOrder))
	missingReq.SetPathValue("id", "event-a")
	w = httptest.NewRecorder()
	handler.ReorderEventRequests(w, missingReq)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected missing request reorder to be rejected with 400, got %d: %s", w.Code, w.Body.String())
	}

	// 3. Valid reorder under event-a -> 200 OK
	validOrder, _ := json.Marshal(map[string][]string{"order": {"req-a1"}})
	validReq := httptest.NewRequest(http.MethodPost, "/api/v1/events/event-a/requests/reorder", bytes.NewReader(validOrder))
	validReq.SetPathValue("id", "event-a")
	w = httptest.NewRecorder()
	handler.ReorderEventRequests(w, validReq)
	if w.Code != http.StatusOK {
		t.Fatalf("expected valid reorder to return 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestQueueHandler_QueueRotationAndBurstProtection(t *testing.T) {
	sbStore := newTestStore()
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	handler := &QueueHandler{Songbooks: sbStore, Events: evStore, Requests: qStore}

	sb := &models.Songbook{ID: "sb-1", Name: "Karaoke Night", OwnerID: "owner-1"}
	_ = sbStore.CreateSongbook(context.Background(), sb)

	// 1. Burst Protection Test (FCFS Mode Only)
	eventBurst := &models.Event{
		ID:              "ev-burst",
		SongbookID:      "sb-1",
		Name:            "Burst Test Show",
		Status:          models.EventStatusActive,
		RotationMode:    models.RotationModeFCFS,
		SubmissionDelay: 60, // 60s cooldown in FCFS mode
		AutoAccept:      true,
	}
	_ = evStore.CreateEvent(context.Background(), eventBurst)

	// First submission from Alice -> 201 Created
	req1Body, _ := json.Marshal(models.RequestCreate{
		SongTitle:  "Don't Stop Believin'",
		SongArtist: "Journey",
		SingerName: "Alice",
	})
	r1 := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-burst/requests", bytes.NewReader(req1Body))
	r1.SetPathValue("id", "ev-burst")
	w1 := httptest.NewRecorder()
	handler.CreateEventRequest(w1, r1)
	if w1.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for first request, got: %d (%s)", w1.Code, w1.Body.String())
	}

	// Immediate second submission from Alice -> 429 Too Many Requests
	req2Body, _ := json.Marshal(models.RequestCreate{
		SongTitle:  "Bohemian Rhapsody",
		SongArtist: "Queen",
		SingerName: "Alice",
	})
	r2 := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-burst/requests", bytes.NewReader(req2Body))
	r2.SetPathValue("id", "ev-burst")
	w2 := httptest.NewRecorder()
	handler.CreateEventRequest(w2, r2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 Too Many Requests for rapid second request, got: %d (%s)", w2.Code, w2.Body.String())
	}

	// Bob submits immediately -> 201 Created (different singer, no cooldown)
	reqBobBody, _ := json.Marshal(models.RequestCreate{
		SongTitle:  "Livin' On A Prayer",
		SongArtist: "Bon Jovi",
		SingerName: "Bob",
	})
	rBob := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-burst/requests", bytes.NewReader(reqBobBody))
	rBob.SetPathValue("id", "ev-burst")
	wBob := httptest.NewRecorder()
	handler.CreateEventRequest(wBob, rBob)
	if wBob.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for Bob, got: %d (%s)", wBob.Code, wBob.Body.String())
	}

	// 2. AutoAccept vs Staging Test
	eventStaging := &models.Event{
		ID:              "ev-staging",
		SongbookID:      "sb-1",
		Name:            "Staging Inbox Show",
		Status:          models.EventStatusActive,
		SubmissionDelay: 0,
		AutoAccept:      false, // held in staging inbox
	}
	_ = evStore.CreateEvent(context.Background(), eventStaging)

	rStaging := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-staging/requests", bytes.NewReader(req1Body))
	rStaging.SetPathValue("id", "ev-staging")
	wStaging := httptest.NewRecorder()
	handler.CreateEventRequest(wStaging, rStaging)
	if wStaging.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", wStaging.Code)
	}
	var createdStaging models.SongRequest
	_ = json.Unmarshal(wStaging.Body.Bytes(), &createdStaging)
	if createdStaging.Status != models.StatusPending {
		t.Fatalf("expected StatusPending for staging show, got %s", createdStaging.Status)
	}

	// 3. FCFS Sequential Queue Ranking Test
	eventFCFS := &models.Event{
		ID:              "ev-fcfs",
		SongbookID:      "sb-1",
		Name:            "FCFS Show",
		Status:          models.EventStatusActive,
		RotationMode:    models.RotationModeFCFS,
		SubmissionDelay: 0,
		AutoAccept:      true,
	}
	_ = evStore.CreateEvent(context.Background(), eventFCFS)

	// Alice submits Song 1
	rAlice1 := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-fcfs/requests", bytes.NewReader(req1Body))
	rAlice1.SetPathValue("id", "ev-fcfs")
	wAlice1 := httptest.NewRecorder()
	handler.CreateEventRequest(wAlice1, rAlice1)
	var a1 models.SongRequest
	_ = json.Unmarshal(wAlice1.Body.Bytes(), &a1)

	// Bob submits Song 1
	rBob1 := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-fcfs/requests", bytes.NewReader(reqBobBody))
	rBob1.SetPathValue("id", "ev-fcfs")
	wBob1 := httptest.NewRecorder()
	handler.CreateEventRequest(wBob1, rBob1)
	var b1 models.SongRequest
	_ = json.Unmarshal(wBob1.Body.Bytes(), &b1)

	// FCFS ensures requests are ordered chronologically by arrival
	if a1.Rank >= b1.Rank {
		t.Fatalf("expected Alice 1 to precede Bob 1 in FCFS queue, got a1=%d, b1=%d", a1.Rank, b1.Rank)
	}
}

func TestQueueHandler_GetRequest_SecurityAndDeclineReason(t *testing.T) {
	sbStore := newTestStore()
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	handler := &QueueHandler{Songbooks: sbStore, Events: evStore, Requests: qStore}

	sb := &models.Songbook{ID: "sb-getreq", Name: "Karaoke Night", OwnerID: "host-owner"}
	_ = sbStore.CreateSongbook(context.Background(), sb)

	event := &models.Event{
		ID:         "ev-getreq",
		SongbookID: "sb-getreq",
		OwnerID:    "host-owner",
		Name:       "GetReq Show",
		Status:     models.EventStatusActive,
		AutoAccept: false,
	}
	_ = evStore.CreateEvent(context.Background(), event)

	// Singer submits request
	createBody, _ := json.Marshal(models.RequestCreate{
		SongTitle:  "Don't Stop Me Now",
		SongArtist: "Queen",
		SingerName: "Freddie",
	})
	rCreate := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-getreq/requests", bytes.NewReader(createBody))
	rCreate.SetPathValue("id", "ev-getreq")
	wCreate := httptest.NewRecorder()
	handler.CreateEventRequest(wCreate, rCreate)
	if wCreate.Code != http.StatusCreated {
		t.Fatalf("create failed: %d", wCreate.Code)
	}
	var created models.SongRequest
	_ = json.Unmarshal(wCreate.Body.Bytes(), &created)

	// 1. Unauthenticated request without cancel token -> 403 Forbidden
	rNoAuth := httptest.NewRequest(http.MethodGet, "/api/v1/events/ev-getreq/requests/"+created.ID, nil)
	rNoAuth.SetPathValue("id", "ev-getreq")
	rNoAuth.SetPathValue("requestID", created.ID)
	wNoAuth := httptest.NewRecorder()
	handler.GetRequest(wNoAuth, rNoAuth)
	if wNoAuth.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden without auth or token, got %d", wNoAuth.Code)
	}

	// 2. Request with valid cancel token via query parameter -> 200 OK
	rWithQuery := httptest.NewRequest(http.MethodGet, "/api/v1/events/ev-getreq/requests/"+created.ID+"?cancel_token="+created.CancelToken, nil)
	rWithQuery.SetPathValue("id", "ev-getreq")
	rWithQuery.SetPathValue("requestID", created.ID)
	wWithQuery := httptest.NewRecorder()
	handler.GetRequest(wWithQuery, rWithQuery)
	if wWithQuery.Code != http.StatusOK {
		t.Fatalf("expected 200 OK with query cancel token, got %d: %s", wWithQuery.Code, wWithQuery.Body.String())
	}
	var resBody map[string]interface{}
	_ = json.Unmarshal(wWithQuery.Body.Bytes(), &resBody)
	if resBody["status"] != string(models.StatusPending) {
		t.Fatalf("expected status pending, got %v", resBody["status"])
	}

	// 3. Host declines request with reason
	declineReason := "Track is missing"
	reqInStore, _ := qStore.GetRequest(context.Background(), created.ID)
	reqInStore.Status = models.StatusRejected
	reqInStore.DeclineReason = declineReason
	_ = qStore.UpdateRequest(context.Background(), reqInStore)

	// 4. Singer polls with valid cancel token via header -> gets rejected status and decline reason
	rHeader := httptest.NewRequest(http.MethodGet, "/api/v1/events/ev-getreq/requests/"+created.ID, nil)
	rHeader.Header.Set("X-Cancel-Token", created.CancelToken)
	rHeader.SetPathValue("id", "ev-getreq")
	rHeader.SetPathValue("requestID", created.ID)
	wHeader := httptest.NewRecorder()
	handler.GetRequest(wHeader, rHeader)
	if wHeader.Code != http.StatusOK {
		t.Fatalf("expected 200 OK with header cancel token, got %d", wHeader.Code)
	}
	resBody = nil
	_ = json.Unmarshal(wHeader.Body.Bytes(), &resBody)
	if resBody["status"] != string(models.StatusRejected) {
		t.Fatalf("expected status rejected, got %v", resBody["status"])
	}
	if resBody["decline_reason"] != declineReason {
		t.Fatalf("expected decline_reason %q, got %v", declineReason, resBody["decline_reason"])
	}
}

func TestQueueHandler_MaxActivePerSinger(t *testing.T) {
	sbStore := newTestStore()
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	handler := &QueueHandler{Songbooks: sbStore, Events: evStore, Requests: qStore}

	sb := &models.Songbook{ID: "sb-limits", Name: "Limits Songbook", OwnerID: "host-limits"}
	_ = sbStore.CreateSongbook(context.Background(), sb)

	event := &models.Event{
		ID:                 "ev-limits",
		SongbookID:         "sb-limits",
		OwnerID:            "host-limits",
		Name:               "Singer Limits Show",
		Status:             models.EventStatusActive,
		MaxActivePerSinger: 2,
		SubmissionDelay:    0,
		AutoAccept:         true,
	}
	_ = evStore.CreateEvent(context.Background(), event)

	makeReq := func(singer, title string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(models.RequestCreate{
			SingerName: singer,
			SongTitle:  title,
		})
		r := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-limits/requests", bytes.NewReader(body))
		r.SetPathValue("id", "ev-limits")
		w := httptest.NewRecorder()
		handler.CreateEventRequest(w, r)
		return w
	}

	// 1. Alice submits 1st request -> 201 Created
	w1 := makeReq("Alice", "Song 1")
	if w1.Code != http.StatusCreated {
		t.Fatalf("expected 201 for 1st request, got %d", w1.Code)
	}
	var req1 models.SongRequest
	_ = json.Unmarshal(w1.Body.Bytes(), &req1)

	// 2. Alice submits 2nd request -> 201 Created
	w2 := makeReq("Alice", "Song 2")
	if w2.Code != http.StatusCreated {
		t.Fatalf("expected 201 for 2nd request, got %d", w2.Code)
	}

	// 3. Alice submits 3rd request -> 409 Conflict (limit is 2)
	w3 := makeReq("Alice", "Song 3")
	if w3.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for 3rd request over limit, got %d: %s", w3.Code, w3.Body.String())
	}
	var errBody map[string]interface{}
	_ = json.Unmarshal(w3.Body.Bytes(), &errBody)
	if errBody["error"] != "max_active_limit" {
		t.Fatalf("expected error max_active_limit, got %v", errBody["error"])
	}

	// 4. Bob submits 1st request -> 201 Created (independent singer)
	wBob := makeReq("Bob", "Bob's Song")
	if wBob.Code != http.StatusCreated {
		t.Fatalf("expected 201 for Bob, got %d", wBob.Code)
	}

	// 5. Host marks Alice's 1st request completed
	req1InStore, _ := qStore.GetRequest(context.Background(), req1.ID)
	req1InStore.Status = models.StatusCompleted
	_ = qStore.UpdateRequest(context.Background(), req1InStore)

	// 6. Alice can now submit another request because she only has 1 active request remaining
	w4 := makeReq("Alice", "Song 4")
	if w4.Code != http.StatusCreated {
		t.Fatalf("expected 201 for Alice after earlier song completed, got %d: %s", w4.Code, w4.Body.String())
	}
}

func TestQueueHandler_RotationModeRoundRobin(t *testing.T) {
	sbStore := newTestStore()
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	handler := &QueueHandler{Songbooks: sbStore, Events: evStore, Requests: qStore}

	sb := &models.Songbook{ID: "sb-rr", Name: "RR Songbook", OwnerID: "host-rr"}
	_ = sbStore.CreateSongbook(context.Background(), sb)

	event := &models.Event{
		ID:                 "ev-rr",
		SongbookID:         "sb-rr",
		OwnerID:            "host-rr",
		Name:               "Round Robin Show",
		Status:             models.EventStatusActive,
		RotationMode:       models.RotationModeRoundRobin,
		MaxActivePerSinger: 0, // unlimited active
		SubmissionDelay:    0,
		AutoAccept:         true,
	}
	_ = evStore.CreateEvent(context.Background(), event)

	makeReq := func(singer, title string) models.SongRequest {
		body, _ := json.Marshal(models.RequestCreate{
			SingerName: singer,
			SongTitle:  title,
		})
		r := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-rr/requests", bytes.NewReader(body))
		r.SetPathValue("id", "ev-rr")
		w := httptest.NewRecorder()
		handler.CreateEventRequest(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("request failed: %d: %s", w.Code, w.Body.String())
		}
		var req models.SongRequest
		_ = json.Unmarshal(w.Body.Bytes(), &req)
		return req
	}

	// Alice submits Song 1 (Round 0)
	alice1 := makeReq("Alice", "Alice Song 1")
	// Alice submits Song 2 (Round 1)
	alice2 := makeReq("Alice", "Alice Song 2")
	// Bob submits Song 1 (Round 0)
	bob1 := makeReq("Bob", "Bob Song 1")

	// Fair round-robin:
	// Alice 1 (Round 1) < Bob 1 (Round 1) < Alice 2 (Round 2)
	if alice1.Rank >= bob1.Rank {
		t.Fatalf("expected Alice 1 to rank before Bob 1 in Round 1 (alice1=%d, bob1=%d)", alice1.Rank, bob1.Rank)
	}
	if bob1.Rank >= alice2.Rank {
		t.Fatalf("expected Bob 1 (Round 1) to rank before Alice 2 (Round 2) (bob1=%d, alice2=%d)", bob1.Rank, alice2.Rank)
	}
	if alice1.Round != 1 || bob1.Round != 1 || alice2.Round != 2 {
		t.Fatalf("expected rounds alice1=1, bob1=1, alice2=2; got %d, %d, %d", alice1.Round, bob1.Round, alice2.Round)
	}
}

func TestQueueHandler_RoundAssignmentAndNewcomerPrioritization(t *testing.T) {
	sbStore := newTestStore()
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	handler := &QueueHandler{Songbooks: sbStore, Events: evStore, Requests: qStore}

	sb := &models.Songbook{ID: "sb-newcomer", Name: "Newcomer Songbook", OwnerID: "host-1"}
	_ = sbStore.CreateSongbook(context.Background(), sb)

	event := &models.Event{
		ID:           "ev-newcomer",
		SongbookID:   "sb-newcomer",
		OwnerID:      "host-1",
		Name:         "Newcomer Show",
		Status:       models.EventStatusActive,
		RotationMode: models.RotationModeRoundRobin,
		CurrentRound: 1,
		AutoAccept:   true,
	}
	_ = evStore.CreateEvent(context.Background(), event)

	makeReq := func(singer, title string) models.SongRequest {
		body, _ := json.Marshal(models.RequestCreate{
			SingerName: singer,
			SongTitle:  title,
		})
		r := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-newcomer/requests", bytes.NewReader(body))
		r.SetPathValue("id", "ev-newcomer")
		w := httptest.NewRecorder()
		handler.CreateEventRequest(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("request failed: %d: %s", w.Code, w.Body.String())
		}
		var req models.SongRequest
		_ = json.Unmarshal(w.Body.Bytes(), &req)
		return req
	}

	// 1. Initial Round 1 submissions
	alice1 := makeReq("Alice", "Song A1")
	alice2 := makeReq("Alice", "Song A2")
	bob1 := makeReq("Bob", "Song B1")

	if alice1.Round != 1 {
		t.Errorf("expected alice1 round 1, got %d", alice1.Round)
	}
	if alice2.Round != 2 {
		t.Errorf("expected alice2 round 2, got %d", alice2.Round)
	}
	if bob1.Round != 1 {
		t.Errorf("expected bob1 round 1, got %d", bob1.Round)
	}

	// 2. Advance event to Round 2 (e.g. host completes round 1)
	event.CurrentRound = 2
	_ = evStore.UpdateEvent(context.Background(), event)

	// Mark alice1 and bob1 as completed
	alice1Req, _ := qStore.GetRequest(context.Background(), alice1.ID)
	alice1Req.Status = models.StatusCompleted
	_ = qStore.UpdateRequest(context.Background(), alice1Req)

	bob1Req, _ := qStore.GetRequest(context.Background(), bob1.ID)
	bob1Req.Status = models.StatusCompleted
	_ = qStore.UpdateRequest(context.Background(), bob1Req)

	// 3. Newcomer Charlie arrives during Round 2 with 0 turns
	charlie1 := makeReq("Charlie", "Song C1")
	// Charlie should be prioritized into the active round (Round 2)!
	if charlie1.Round != 2 {
		t.Errorf("expected newcomer charlie1 to be prioritized into active Round 2, got %d", charlie1.Round)
	}

	// 4. Alice (who has 1 completed song and alice2 currently in Round 2) submits her 3rd song
	alice3 := makeReq("Alice", "Song A3")
	// alice3 should slot into Round 3 (reserve)
	if alice3.Round != 3 {
		t.Errorf("expected alice3 to slot into Round 3 reserve, got %d", alice3.Round)
	}

	// 5. Ranking check:
	// Round 2 requests: alice2 and charlie1 should rank before alice3 (Round 3)
	if alice2.Rank >= alice3.Rank {
		t.Errorf("expected Round 2 alice2 (%d) to rank before Round 3 alice3 (%d)", alice2.Rank, alice3.Rank)
	}
	if charlie1.Rank >= alice3.Rank {
		t.Errorf("expected Round 2 charlie1 (%d) to rank before Round 3 alice3 (%d)", charlie1.Rank, alice3.Rank)
	}
}

func TestQueueHandler_RotationModeManual(t *testing.T) {
	sbStore := newTestStore()
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	handler := &QueueHandler{Songbooks: sbStore, Events: evStore, Requests: qStore}

	sb := &models.Songbook{ID: "sb-manual", Name: "Manual Songbook", OwnerID: "host-manual"}
	_ = sbStore.CreateSongbook(context.Background(), sb)

	event := &models.Event{
		ID:              "ev-manual",
		SongbookID:      "sb-manual",
		OwnerID:         "host-manual",
		Name:            "Manual Staging Show",
		Status:          models.EventStatusActive,
		RotationMode:    models.RotationModeManual,
		AutoAccept:      true, // AutoAccept is true, but manual mode must override it to pending
		SubmissionDelay: 0,
	}
	_ = evStore.CreateEvent(context.Background(), event)

	body, _ := json.Marshal(models.RequestCreate{
		SingerName: "Charlie",
		SongTitle:  "Staged Request",
	})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-manual/requests", bytes.NewReader(body))
	r.SetPathValue("id", "ev-manual")
	w := httptest.NewRecorder()
	handler.CreateEventRequest(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("create failed: %d: %s", w.Code, w.Body.String())
	}
	var req models.SongRequest
	_ = json.Unmarshal(w.Body.Bytes(), &req)

	if req.Status != models.StatusPending {
		t.Fatalf("expected StatusPending in manual rotation mode, got %s", req.Status)
	}
}

func TestQueueHandler_ConditionalEventMutation(t *testing.T) {
	evStore := newTestEventStore()
	requests := &conditionalTestQueueStore{testQueueStore: newTestQueueStore(), currentVersion: 4}
	event := &models.Event{ID: "ev-versioned", Status: models.EventStatusActive, QueueVersion: 4}
	_ = evStore.CreateEvent(context.Background(), event)
	request := &models.SongRequest{ID: "req-versioned", EventID: event.ID, Status: models.StatusAccepted}
	_ = requests.CreateRequest(context.Background(), request)
	handler := &QueueHandler{Events: evStore, Requests: requests}

	status := models.StatusCompleted
	body, _ := json.Marshal(models.RequestUpdate{Status: &status, ExpectedQueueVersion: ptrInt64(4)})
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-versioned/requests/req-versioned", bytes.NewReader(body))
	r.SetPathValue("id", event.ID)
	r.SetPathValue("requestID", request.ID)
	w := httptest.NewRecorder()
	handler.UpdateEventRequest(w, r)
	if w.Code != http.StatusOK || w.Header().Get("X-Queue-Version") != "5" {
		t.Fatalf("conditional update = %d, header %q; want 200/5", w.Code, w.Header().Get("X-Queue-Version"))
	}
	if requests.currentVersion != 5 {
		t.Fatalf("conditional update version = %d; want 5", requests.currentVersion)
	}

	staleStatus := models.StatusAccepted
	body, _ = json.Marshal(models.RequestUpdate{Status: &staleStatus, ExpectedQueueVersion: ptrInt64(4)})
	r = httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-versioned/requests/req-versioned", bytes.NewReader(body))
	r.SetPathValue("id", event.ID)
	r.SetPathValue("requestID", request.ID)
	w = httptest.NewRecorder()
	handler.UpdateEventRequest(w, r)
	if w.Code != http.StatusConflict || w.Header().Get("X-Queue-Version") != "5" {
		t.Fatalf("stale conditional update = %d, header %q; want 409/5", w.Code, w.Header().Get("X-Queue-Version"))
	}
	var conflict map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &conflict)
	if conflict["expected_queue_version"] != float64(4) || conflict["current_queue_version"] != float64(5) {
		t.Fatalf("unexpected conflict body: %v", conflict)
	}
	stored, _ := requests.testQueueStore.GetRequest(context.Background(), request.ID)
	if stored.Status != models.StatusCompleted || requests.currentVersion != 5 {
		t.Fatalf("stale update changed stored state: status=%s version=%d", stored.Status, requests.currentVersion)
	}

	body, _ = json.Marshal(models.RequestUpdate{ExpectedQueueVersion: ptrInt64(-1)})
	r = httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-versioned/requests/req-versioned", bytes.NewReader(body))
	r.SetPathValue("id", event.ID)
	r.SetPathValue("requestID", request.ID)
	w = httptest.NewRecorder()
	handler.UpdateEventRequest(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("negative expected version = %d; want 400", w.Code)
	}
}

func TestQueueHandler_ConditionalReorderNoOpDoesNotBump(t *testing.T) {
	evStore := newTestEventStore()
	requests := &conditionalTestQueueStore{testQueueStore: newTestQueueStore(), currentVersion: 8}
	event := &models.Event{ID: "ev-reorder-versioned", Status: models.EventStatusActive, QueueVersion: 8}
	_ = evStore.CreateEvent(context.Background(), event)
	handler := &QueueHandler{Events: evStore, Requests: requests}
	body := []byte(`{"order":[],"expected_queue_version":8}`)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-reorder-versioned/requests/reorder", bytes.NewReader(body))
	r.SetPathValue("id", event.ID)
	w := httptest.NewRecorder()
	handler.ReorderEventRequests(w, r)
	if w.Code != http.StatusOK || w.Header().Get("X-Queue-Version") != "8" || requests.currentVersion != 8 {
		t.Fatalf("empty conditional reorder = %d, header %q, version %d; want 200/8", w.Code, w.Header().Get("X-Queue-Version"), requests.currentVersion)
	}
}

func TestQueueHandler_ConditionalReorderRejectsInvalidOrder(t *testing.T) {
	evStore := newTestEventStore()
	requests := &conditionalTestQueueStore{testQueueStore: newTestQueueStore(), currentVersion: 3}
	event := &models.Event{ID: "ev-reorder-invalid", Status: models.EventStatusActive, QueueVersion: 3}
	_ = evStore.CreateEvent(context.Background(), event)
	handler := &QueueHandler{Events: evStore, Requests: requests}

	r := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-reorder-invalid/requests/reorder", bytes.NewBufferString(`{"order":["same","same"],"expected_queue_version":3}`))
	r.SetPathValue("id", event.ID)
	w := httptest.NewRecorder()
	handler.ReorderEventRequests(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("duplicate conditional reorder = %d; want 400", w.Code)
	}

	order := make([]string, 100)
	for i := range order {
		order[i] = fmt.Sprintf("request-%d", i)
	}
	body, _ := json.Marshal(struct {
		Order                []string `json:"order"`
		ExpectedQueueVersion int64    `json:"expected_queue_version"`
	}{Order: order, ExpectedQueueVersion: 3})
	r = httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-reorder-invalid/requests/reorder", bytes.NewReader(body))
	r.SetPathValue("id", event.ID)
	w = httptest.NewRecorder()
	handler.ReorderEventRequests(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("oversized conditional reorder = %d; want 400", w.Code)
	}
	if requests.currentVersion != 3 {
		t.Fatalf("invalid conditional reorder changed version to %d", requests.currentVersion)
	}
}

func TestQueueHandler_IdempotentStatusReplayAndIsolation(t *testing.T) {
	evStore := newTestEventStore()
	requests := &conditionalTestQueueStore{testQueueStore: newTestQueueStore(), currentVersion: 4}
	event := &models.Event{ID: "ev-idempotent", OwnerID: "host-a", Status: models.EventStatusActive, QueueVersion: 4}
	_ = evStore.CreateEvent(context.Background(), event)
	request := &models.SongRequest{ID: "req-idempotent", EventID: event.ID, Status: models.StatusAccepted}
	_ = requests.CreateRequest(context.Background(), request)
	handler := &QueueHandler{Events: evStore, Requests: requests}

	completed := models.StatusCompleted
	body, _ := json.Marshal(models.RequestUpdate{Status: &completed, ExpectedQueueVersion: ptrInt64(4)})
	makeRequest := func(owner, statusBody string) *http.Request {
		r := httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-idempotent/requests/req-idempotent", bytes.NewBufferString(statusBody))
		r.Header.Set("Idempotency-Key", "11111111-1111-4111-8111-111111111111")
		r.SetPathValue("id", event.ID)
		r.SetPathValue("requestID", request.ID)
		return r.WithContext(context.WithValue(r.Context(), userContextKey, &models.User{ID: owner, IsHost: true}))
	}

	w := httptest.NewRecorder()
	handler.UpdateEventRequest(w, makeRequest("host-a", string(body)))
	firstResponse := w.Body.String()
	if w.Code != http.StatusOK || w.Header().Get("X-Queue-Version") != "5" || requests.currentVersion != 5 {
		t.Fatalf("first idempotent status = %d/header %q/version %d", w.Code, w.Header().Get("X-Queue-Version"), requests.currentVersion)
	}
	delete(requests.requests, request.ID)

	w = httptest.NewRecorder()
	handler.UpdateEventRequest(w, makeRequest("host-a", string(body)))
	if w.Code != http.StatusOK || w.Header().Get("X-Queue-Version") != "5" || w.Body.String() != firstResponse || requests.currentVersion != 5 {
		t.Fatalf("replayed idempotent status = %d/header %q/version %d/body equal=%v", w.Code, w.Header().Get("X-Queue-Version"), requests.currentVersion, w.Body.String() == firstResponse)
	}

	rejected := models.StatusRejected
	mismatch, _ := json.Marshal(models.RequestUpdate{Status: &rejected, ExpectedQueueVersion: ptrInt64(4)})
	w = httptest.NewRecorder()
	handler.UpdateEventRequest(w, makeRequest("host-a", string(mismatch)))
	if w.Code != http.StatusConflict || requests.currentVersion != 5 {
		t.Fatalf("mismatched idempotent status = %d/version %d; want 409/5", w.Code, requests.currentVersion)
	}

	w = httptest.NewRecorder()
	handler.UpdateEventRequest(w, makeRequest("host-b", string(body)))
	if w.Code != http.StatusConflict || requests.currentVersion != 5 {
		t.Fatalf("cross-tenant idempotent status = %d/version %d; want 409/5", w.Code, requests.currentVersion)
	}
}

func TestQueueHandler_IdempotentReorderReplay(t *testing.T) {
	evStore := newTestEventStore()
	requests := &conditionalTestQueueStore{testQueueStore: newTestQueueStore(), currentVersion: 7}
	event := &models.Event{ID: "ev-idempotent-reorder", OwnerID: "host-a", Status: models.EventStatusActive, QueueVersion: 7}
	_ = evStore.CreateEvent(context.Background(), event)
	first := &models.SongRequest{ID: "idempotent-first", EventID: event.ID, Status: models.StatusAccepted}
	second := &models.SongRequest{ID: "idempotent-second", EventID: event.ID, Status: models.StatusAccepted}
	_ = requests.CreateRequest(context.Background(), first)
	_ = requests.CreateRequest(context.Background(), second)
	handler := &QueueHandler{Events: evStore, Requests: requests}

	body := `{"order":["idempotent-second","idempotent-first"],"expected_queue_version":7}`
	makeRequest := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-idempotent-reorder/requests/reorder", bytes.NewBufferString(body))
		r.Header.Set("Idempotency-Key", "22222222-2222-4222-8222-222222222222")
		r.SetPathValue("id", event.ID)
		return r.WithContext(context.WithValue(r.Context(), userContextKey, &models.User{ID: "host-a", IsHost: true}))
	}
	w := httptest.NewRecorder()
	handler.ReorderEventRequests(w, makeRequest())
	if w.Code != http.StatusOK || w.Header().Get("X-Queue-Version") != "8" || requests.currentVersion != 8 {
		t.Fatalf("first idempotent reorder = %d/header %q/version %d", w.Code, w.Header().Get("X-Queue-Version"), requests.currentVersion)
	}
	w = httptest.NewRecorder()
	handler.ReorderEventRequests(w, makeRequest())
	if w.Code != http.StatusOK || w.Header().Get("X-Queue-Version") != "8" || requests.currentVersion != 8 || requests.order[0] != second.ID {
		t.Fatalf("replayed idempotent reorder = %d/header %q/version %d/order %v", w.Code, w.Header().Get("X-Queue-Version"), requests.currentVersion, requests.order)
	}
}

func TestQueueHandler_ConditionalReorderRejectsStaleWithoutChangingOrder(t *testing.T) {
	evStore := newTestEventStore()
	requests := &conditionalTestQueueStore{testQueueStore: newTestQueueStore(), currentVersion: 8}
	event := &models.Event{ID: "ev-reorder-stale", Status: models.EventStatusActive, QueueVersion: 8}
	_ = evStore.CreateEvent(context.Background(), event)
	first := &models.SongRequest{ID: "reorder-first", EventID: event.ID, Status: models.StatusAccepted}
	second := &models.SongRequest{ID: "reorder-second", EventID: event.ID, Status: models.StatusAccepted}
	_ = requests.CreateRequest(context.Background(), first)
	_ = requests.CreateRequest(context.Background(), second)
	handler := &QueueHandler{Events: evStore, Requests: requests}

	r := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-reorder-stale/requests/reorder", bytes.NewBufferString(`{"order":["reorder-second","reorder-first"],"expected_queue_version":8}`))
	r.SetPathValue("id", event.ID)
	w := httptest.NewRecorder()
	handler.ReorderEventRequests(w, r)
	if w.Code != http.StatusOK || w.Header().Get("X-Queue-Version") != "9" {
		t.Fatalf("conditional reorder = %d, header %q; want 200/9", w.Code, w.Header().Get("X-Queue-Version"))
	}

	r = httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-reorder-stale/requests/reorder", bytes.NewBufferString(`{"order":["reorder-first","reorder-second"],"expected_queue_version":8}`))
	r.SetPathValue("id", event.ID)
	w = httptest.NewRecorder()
	handler.ReorderEventRequests(w, r)
	if w.Code != http.StatusConflict || w.Header().Get("X-Queue-Version") != "9" {
		t.Fatalf("stale conditional reorder = %d, header %q; want 409/9", w.Code, w.Header().Get("X-Queue-Version"))
	}
	if requests.currentVersion != 9 || len(requests.order) != 2 || requests.order[0] != second.ID || requests.order[1] != first.ID {
		t.Fatalf("stale reorder changed state: order=%v version=%d", requests.order, requests.currentVersion)
	}
}

func TestQueueHandler_RoundRobinSlotting(t *testing.T) {
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	sbStore := newTestStore()
	handler := &QueueHandler{Events: evStore, Requests: qStore, Songbooks: sbStore}

	sb := &models.Songbook{ID: "sb-rr", PublicID: "pub-rr", Name: "RR Test", IsPublic: true}
	_ = sbStore.CreateSongbook(context.Background(), sb)

	event := &models.Event{
		ID:           "ev-rr-1",
		OwnerID:      "host-rr",
		SongbookID:   sb.ID,
		Status:       models.EventStatusActive,
		RotationMode: models.RotationModeRoundRobin,
		CurrentRound: 1,
		AutoAccept:   true,
	}
	_ = evStore.CreateEvent(context.Background(), event)

	submitReq := func(singer, songTitle string) *models.SongRequest {
		body := fmt.Sprintf(`{"singer_name":%q,"song_title":%q,"song_artist":"Artist"}`, singer, songTitle)
		r := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-rr-1/requests", bytes.NewBufferString(body))
		r.SetPathValue("id", event.ID)
		w := httptest.NewRecorder()
		handler.CreateEventRequest(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("CreateEventRequest failed: code %d, body: %s", w.Code, w.Body.String())
		}
		var created models.SongRequest
		_ = json.NewDecoder(w.Body).Decode(&created)
		return &created
	}

	// 1. Alice submits 1st song -> Round 1
	reqA1 := submitReq("Alice", "Song A1")
	if reqA1.Round != 1 {
		t.Fatalf("reqA1.Round = %d; want 1", reqA1.Round)
	}

	// 2. Alice submits 2nd song -> Round 2 (Alice already has active song in Round 1)
	reqA2 := submitReq("Alice", "Song A2")
	if reqA2.Round != 2 {
		t.Fatalf("reqA2.Round = %d; want 2", reqA2.Round)
	}

	// 3. Bob submits 1st song -> Round 1 (Bob has no active song in Round 1)
	reqB1 := submitReq("Bob", "Song B1")
	if reqB1.Round != 1 {
		t.Fatalf("reqB1.Round = %d; want 1", reqB1.Round)
	}

	// 4. Alice submits 3rd song -> Round 3
	reqA3 := submitReq("Alice", "Song A3")
	if reqA3.Round != 3 {
		t.Fatalf("reqA3.Round = %d; want 3", reqA3.Round)
	}

	// 5. Host updates reqA2 to Round 4 manually
	targetRound := 4
	updateBody, _ := json.Marshal(models.RequestUpdate{Round: &targetRound})
	updateReq := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/v1/events/ev-rr-1/requests/%s", reqA2.ID), bytes.NewBuffer(updateBody))
	updateReq.SetPathValue("id", event.ID)
	updateReq.SetPathValue("requestID", reqA2.ID)
	updateReq = updateReq.WithContext(context.WithValue(updateReq.Context(), userContextKey, &models.User{ID: "host-rr", IsHost: true}))
	w := httptest.NewRecorder()
	handler.UpdateEventRequest(w, updateReq)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateEventRequest failed: code %d, body: %s", w.Code, w.Body.String())
	}
	var updated models.SongRequest
	_ = json.NewDecoder(w.Body).Decode(&updated)
	if updated.Round != 4 {
		t.Fatalf("updated.Round = %d; want 4", updated.Round)
	}
	expectedRankBase := int64(4) * models.RoundRankMultiplier
	if updated.Rank < expectedRankBase {
		t.Fatalf("updated.Rank = %d; want >= %d", updated.Rank, expectedRankBase)
	}

	// 6. Advance event to Round 2
	event.CurrentRound = 2
	_ = evStore.UpdateEvent(context.Background(), event)

	// 7. Charlie submits 1st song -> Round 2 (since current round is 2)
	reqC1 := submitReq("Charlie", "Song C1")
	if reqC1.Round != 2 {
		t.Fatalf("reqC1.Round = %d; want 2", reqC1.Round)
	}
}

func TestQueueHandler_FairPriority(t *testing.T) {
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	sbStore := newTestStore()
	handler := &QueueHandler{Events: evStore, Requests: qStore, Songbooks: sbStore}

	sb := &models.Songbook{ID: "sb-fp", PublicID: "pub-fp", Name: "Fair Priority Test", IsPublic: true}
	_ = sbStore.CreateSongbook(context.Background(), sb)

	event := &models.Event{
		ID:           "ev-fp-1",
		OwnerID:      "host-fp",
		SongbookID:   sb.ID,
		Status:       models.EventStatusActive,
		RotationMode: models.RotationModeFairPriority,
		CurrentRound: 1,
		AutoAccept:   true,
	}
	_ = evStore.CreateEvent(context.Background(), event)

	submitReq := func(singer, songTitle string) *models.SongRequest {
		body := fmt.Sprintf(`{"singer_name":%q,"song_title":%q,"song_artist":"Artist"}`, singer, songTitle)
		r := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-fp-1/requests", bytes.NewBufferString(body))
		r.SetPathValue("id", event.ID)
		w := httptest.NewRecorder()
		handler.CreateEventRequest(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("CreateEventRequest failed: code %d, body: %s", w.Code, w.Body.String())
		}
		var created models.SongRequest
		_ = json.NewDecoder(w.Body).Decode(&created)
		return &created
	}

	// 1. Initial 3 singers submit -> fill stage lock buffer (On Stage, On Deck, In the Hole)
	reqA1 := submitReq("Alice", "Song A1")
	reqB1 := submitReq("Bob", "Song B1")
	reqC1 := submitReq("Charlie", "Song C1")

	if reqA1.SingerTurn != 1 || reqB1.SingerTurn != 1 || reqC1.SingerTurn != 1 {
		t.Fatalf("expected SingerTurn 1 for A1, B1, C1; got A1=%d, B1=%d, C1=%d", reqA1.SingerTurn, reqB1.SingerTurn, reqC1.SingerTurn)
	}
	if reqA1.Rank >= reqB1.Rank || reqB1.Rank >= reqC1.Rank {
		t.Fatalf("expected A1 < B1 < C1 in ranks; got A1=%d, B1=%d, C1=%d", reqA1.Rank, reqB1.Rank, reqC1.Rank)
	}

	// 2. Dave submits 1st song (Cycle 0, turn 1)
	reqD1 := submitReq("Dave", "Song D1")
	if reqD1.SingerTurn != 1 {
		t.Fatalf("expected SingerTurn 1 for D1; got %d", reqD1.SingerTurn)
	}
	if reqC1.Rank >= reqD1.Rank {
		t.Fatalf("expected C1 < D1; got C1=%d, D1=%d", reqC1.Rank, reqD1.Rank)
	}

	// 3. Dave submits 2nd song immediately (Cycle 1, turn 2) -> must append behind D1
	reqD2 := submitReq("Dave", "Song D2")
	if reqD2.SingerTurn != 2 {
		t.Fatalf("expected SingerTurn 2 for D2; got %d", reqD2.SingerTurn)
	}
	if reqD1.Rank >= reqD2.Rank {
		t.Fatalf("expected D1 < D2; got D1=%d, D2=%d", reqD1.Rank, reqD2.Rank)
	}

	// 4. Eve arrives (newcomer, 0 songs sung). Submits 1st song (Cycle 0, turn 1).
	// Eve has Cycle 0. Dave2 has Cycle 1.
	// Eve must weave in ahead of Dave2!
	reqE1 := submitReq("Eve", "Song E1")
	if reqE1.SingerTurn != 1 {
		t.Fatalf("expected SingerTurn 1 for E1; got %d", reqE1.SingerTurn)
	}
	if reqD1.Rank >= reqE1.Rank {
		t.Fatalf("expected D1 < E1 (FIFO between Cycle 0 newcomers); got D1=%d, E1=%d", reqD1.Rank, reqE1.Rank)
	}
	if reqE1.Rank >= reqD2.Rank {
		t.Fatalf("expected E1 (Cycle 0) < D2 (Cycle 1) in Smart Weave; got E1=%d, D2=%d", reqE1.Rank, reqD2.Rank)
	}

	// 5. Simulate Alice completing her performance
	reqA1.Status = models.StatusCompleted
	_ = qStore.UpdateRequest(context.Background(), reqA1)

	// Now active queue is: B1 (On Stage), C1 (On Deck), D1 (In the Hole), E1 (Cycle 0), D2 (Cycle 1)
	// Alice (1 completed song) submits her 2nd song.
	// Alice2 is Cycle 0 (her only active song in queue).
	// Since D2 is Cycle 1, Alice2 (Cycle 0) must weave in ahead of D2!
	reqA2 := submitReq("Alice", "Song A2")
	if reqA2.SingerTurn != 2 {
		t.Fatalf("expected SingerTurn 2 for A2; got %d", reqA2.SingerTurn)
	}
	if reqE1.Rank >= reqA2.Rank {
		t.Fatalf("expected E1 (0 completed) < A2 (1 completed); got E1=%d, A2=%d", reqE1.Rank, reqA2.Rank)
	}
	if reqA2.Rank >= reqD2.Rank {
		t.Fatalf("expected A2 (Cycle 0) < D2 (Cycle 1); got A2=%d, D2=%d", reqA2.Rank, reqD2.Rank)
	}
}

func TestQueueHandler_PendingToAccepted_PlacementAndStageLock(t *testing.T) {
	evStore := newTestEventStore()
	sbStore := newTestStore()
	qStore := newTestQueueStore()
	handler := &QueueHandler{Events: evStore, Songbooks: sbStore, Requests: qStore}

	sb := &models.Songbook{ID: "sb-pending", PublicID: "pub-pending", Name: "Pending Test", IsPublic: true}
	_ = sbStore.CreateSongbook(context.Background(), sb)

	event := &models.Event{
		ID:           "ev-pending-1",
		OwnerID:      "host-pending",
		SongbookID:   sb.ID,
		Status:       models.EventStatusActive,
		RotationMode: models.RotationModeFairPriority,
		CurrentRound: 1,
		AutoAccept:   false, // Staging inbox enabled
	}
	_ = evStore.CreateEvent(context.Background(), event)

	now := time.Now().UTC()
	rA := &models.SongRequest{ID: "rA", EventID: event.ID, SingerName: "Alice", Status: models.StatusAccepted, Rank: 1, CreatedAt: now.Add(-10 * time.Minute)}
	rB := &models.SongRequest{ID: "rB", EventID: event.ID, SingerName: "Dave", Status: models.StatusAccepted, Rank: 2, CreatedAt: now.Add(-9 * time.Minute)}
	rC := &models.SongRequest{ID: "rC", EventID: event.ID, SingerName: "Charlie", Status: models.StatusAccepted, Rank: 3, CreatedAt: now.Add(-8 * time.Minute)}
	// Dave also has a 2nd song in tail (rank 100), which is Cycle 1
	rD := &models.SongRequest{ID: "rD", EventID: event.ID, SingerName: "Dave", Status: models.StatusAccepted, Rank: 100, CreatedAt: now.Add(-7 * time.Minute)}
	_ = qStore.CreateRequest(context.Background(), rA)
	_ = qStore.CreateRequest(context.Background(), rB)
	_ = qStore.CreateRequest(context.Background(), rC)
	_ = qStore.CreateRequest(context.Background(), rD)

	// 1. Eve submits a song with AutoAccept=false
	createBody := `{"singer_name":"Eve","song_title":"Song Eve 1"}`
	rCreate := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-pending-1/requests", bytes.NewBufferString(createBody))
	rCreate.SetPathValue("id", event.ID)
	wCreate := httptest.NewRecorder()
	handler.CreateEventRequest(wCreate, rCreate)
	if wCreate.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for staged request, got %d", wCreate.Code)
	}
	var eveReq models.SongRequest
	_ = json.Unmarshal(wCreate.Body.Bytes(), &eveReq)
	if eveReq.Status != models.StatusPending {
		t.Fatalf("expected status pending, got %s", eveReq.Status)
	}

	// 2. Verify Eve does NOT appear in public live queue
	rPub := httptest.NewRequest(http.MethodGet, "/api/v1/events/ev-pending-1/queue", nil)
	rPub.SetPathValue("id", event.ID)
	wPub := httptest.NewRecorder()
	handler.GetEventPublicQueue(wPub, rPub)
	if wPub.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for public queue, got %d", wPub.Code)
	}
	var pubResp struct {
		Queue []models.PublicQueueItem `json:"queue"`
	}
	_ = json.Unmarshal(wPub.Body.Bytes(), &pubResp)
	if len(pubResp.Queue) != 4 {
		t.Fatalf("expected 4 active items in public queue, got %d", len(pubResp.Queue))
	}
	for _, it := range pubResp.Queue {
		if it.SingerName == "Eve" {
			t.Fatalf("pending request for Eve should NOT appear in public live queue")
		}
	}

	// 3. Host accepts Eve's staged request into active rotation
	updateBody := `{"status":"accepted"}`
	rUpdate := httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-pending-1/requests/"+eveReq.ID, bytes.NewBufferString(updateBody))
	rUpdate.SetPathValue("id", event.ID)
	rUpdate.SetPathValue("requestID", eveReq.ID)
	rUpdate = rUpdate.WithContext(context.WithValue(rUpdate.Context(), userContextKey, &models.User{ID: "host-pending", IsHost: true}))
	wUpdate := httptest.NewRecorder()
	handler.UpdateEventRequest(wUpdate, rUpdate)
	if wUpdate.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on host accept, got %d: %s", wUpdate.Code, wUpdate.Body.String())
	}
	var acceptedEve models.SongRequest
	_ = json.Unmarshal(wUpdate.Body.Bytes(), &acceptedEve)
	if acceptedEve.Status != models.StatusAccepted {
		t.Fatalf("expected accepted status, got %s", acceptedEve.Status)
	}

	// 4. Verify Eve was placed after the 3-song stage lock buffer, but weaves ahead of Dave (Cycle 1)!
	// Re-fetch Alice, Bob, Charlie
	upA, _ := qStore.GetRequest(context.Background(), "rA")
	upB, _ := qStore.GetRequest(context.Background(), "rB")
	upC, _ := qStore.GetRequest(context.Background(), "rC")
	upD, _ := qStore.GetRequest(context.Background(), "rD")
	upEve, _ := qStore.GetRequest(context.Background(), eveReq.ID)

	if upA.Rank >= upB.Rank || upB.Rank >= upC.Rank {
		t.Fatalf("stage lock buffer was corrupted! A=%d, B=%d, C=%d", upA.Rank, upB.Rank, upC.Rank)
	}
	if upC.Rank >= upEve.Rank {
		t.Fatalf("Eve violated stage lock buffer! C=%d, Eve=%d", upC.Rank, upEve.Rank)
	}
	if upEve.Rank >= upD.Rank {
		t.Fatalf("Eve (Cycle 0 newcomer) did not weave ahead of Dave (Cycle 1)! Eve=%d, Dave=%d", upEve.Rank, upD.Rank)
	}

	// 5. Test Round Robin acceptance from pending
	eventRR := &models.Event{
		ID:           "ev-pending-rr",
		OwnerID:      "host-pending",
		SongbookID:   sb.ID,
		Status:       models.EventStatusActive,
		RotationMode: models.RotationModeRoundRobin,
		CurrentRound: 3,
		AutoAccept:   false,
	}
	_ = evStore.CreateEvent(context.Background(), eventRR)

	rCreateRR := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-pending-rr/requests", bytes.NewBufferString(`{"singer_name":"Frank","song_title":"Frank Song"}`))
	rCreateRR.SetPathValue("id", eventRR.ID)
	wCreateRR := httptest.NewRecorder()
	handler.CreateEventRequest(wCreateRR, rCreateRR)
	var frankReq models.SongRequest
	_ = json.Unmarshal(wCreateRR.Body.Bytes(), &frankReq)

	rAcceptRR := httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-pending-rr/requests/"+frankReq.ID, bytes.NewBufferString(`{"status":"accepted"}`))
	rAcceptRR.SetPathValue("id", eventRR.ID)
	rAcceptRR.SetPathValue("requestID", frankReq.ID)
	rAcceptRR = rAcceptRR.WithContext(context.WithValue(rAcceptRR.Context(), userContextKey, &models.User{ID: "host-pending", IsHost: true}))
	wAcceptRR := httptest.NewRecorder()
	handler.UpdateEventRequest(wAcceptRR, rAcceptRR)
	if wAcceptRR.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on RR accept, got %d: %s", wAcceptRR.Code, wAcceptRR.Body.String())
	}
	var acceptedFrank models.SongRequest
	_ = json.Unmarshal(wAcceptRR.Body.Bytes(), &acceptedFrank)
	if acceptedFrank.Round != 3 {
		t.Fatalf("expected Frank to be assigned current round 3, got %d", acceptedFrank.Round)
	}
}

func TestQueueHandler_PendingAcceptance_PlacementLookupFailure(t *testing.T) {
	sbStore := newTestStore()
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	handler := &QueueHandler{Songbooks: sbStore, Events: evStore, Requests: qStore}

	req := &models.SongRequest{
		ID:         "req-no-ev",
		EventID:    "non-existent-event",
		SingerName: "Alice",
		Status:     models.StatusPending,
		Rank:       999999,
		CreatedAt:  time.Now().UTC(),
	}
	_ = qStore.CreateRequest(context.Background(), req)

	patchBody := `{"status":"accepted"}`
	rPatch := httptest.NewRequest(http.MethodPatch, "/api/v1/events/non-existent-event/requests/req-no-ev", bytes.NewBufferString(patchBody))
	rPatch.SetPathValue("id", "non-existent-event")
	rPatch.SetPathValue("requestID", "req-no-ev")
	rPatch = rPatch.WithContext(context.WithValue(rPatch.Context(), userContextKey, &models.User{ID: "host-1", IsHost: true}))
	wPatch := httptest.NewRecorder()
	handler.UpdateEventRequest(wPatch, rPatch)

	if wPatch.Code != http.StatusNotFound {
		t.Fatalf("expected 404 StatusNotFound when event not found during accept, got %d: %s", wPatch.Code, wPatch.Body.String())
	}

	saved, _ := qStore.GetRequest(context.Background(), "req-no-ev")
	if saved.Status != models.StatusPending {
		t.Fatalf("expected request status to remain pending, got %s", saved.Status)
	}
	if saved.Rank != 999999 {
		t.Fatalf("expected request rank to remain unchanged, got %d", saved.Rank)
	}
}

func TestQueueHandler_PendingAcceptance_AtomicReorder_Conditional(t *testing.T) {
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	condStore := &conditionalTestQueueStore{testQueueStore: qStore, currentVersion: 5}
	handler := &QueueHandler{Events: evStore, Requests: condStore}

	event := &models.Event{
		ID:           "ev-atomic-cond",
		OwnerID:      "host-1",
		Status:       models.EventStatusActive,
		RotationMode: models.RotationModeFairPriority,
		QueueVersion: 5,
	}
	_ = evStore.CreateEvent(context.Background(), event)

	now := time.Now().UTC()
	r1 := &models.SongRequest{ID: "r1", EventID: event.ID, SingerName: "Alice", Status: models.StatusAccepted, Rank: 1, CreatedAt: now.Add(-4 * time.Minute)}
	r2 := &models.SongRequest{ID: "r2", EventID: event.ID, SingerName: "Bob", Status: models.StatusAccepted, Rank: 2, CreatedAt: now.Add(-3 * time.Minute)}
	r3 := &models.SongRequest{ID: "r3", EventID: event.ID, SingerName: "Charlie", Status: models.StatusAccepted, Rank: 3, CreatedAt: now.Add(-2 * time.Minute)}
	r4 := &models.SongRequest{ID: "r4", EventID: event.ID, SingerName: "Dave", Status: models.StatusAccepted, Rank: 4, CreatedAt: now.Add(-1 * time.Minute)}
	r2.SingerName = "Dave"
	_ = qStore.CreateRequest(context.Background(), r1)
	_ = qStore.CreateRequest(context.Background(), r2)
	_ = qStore.CreateRequest(context.Background(), r3)
	_ = qStore.CreateRequest(context.Background(), r4)
	qStore.order = []string{"r1", "r2", "r3", "r4"}

	rEve := &models.SongRequest{ID: "rEve", EventID: event.ID, SingerName: "Eve", Status: models.StatusPending, Rank: 999999, CreatedAt: now}
	_ = qStore.CreateRequest(context.Background(), rEve)

	patchBody := `{"status":"accepted","expected_queue_version":5}`
	rPatch := httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-atomic-cond/requests/rEve", bytes.NewBufferString(patchBody))
	rPatch.SetPathValue("id", event.ID)
	rPatch.SetPathValue("requestID", "rEve")
	rPatch = rPatch.WithContext(context.WithValue(rPatch.Context(), userContextKey, &models.User{ID: "host-1", IsHost: true}))
	wPatch := httptest.NewRecorder()
	handler.UpdateEventRequest(wPatch, rPatch)

	if wPatch.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", wPatch.Code, wPatch.Body.String())
	}
	if wPatch.Header().Get("X-Queue-Version") != "6" {
		t.Fatalf("expected X-Queue-Version 6, got %s", wPatch.Header().Get("X-Queue-Version"))
	}

	savedEve, _ := qStore.GetRequest(context.Background(), "rEve")
	savedR4, _ := qStore.GetRequest(context.Background(), "r4")
	if savedEve.Rank != 4 {
		t.Fatalf("expected Eve to receive rank 4 in reordered queue, got %d", savedEve.Rank)
	}
	if savedR4.Rank != 5 {
		t.Fatalf("expected Dave r4 to receive rank 5 in reordered queue, got %d", savedR4.Rank)
	}
}

func TestQueueHandler_PendingAcceptance_AtomicReorder_Idempotent(t *testing.T) {
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	condStore := &conditionalTestQueueStore{testQueueStore: qStore, currentVersion: 2}
	handler := &QueueHandler{Events: evStore, Requests: condStore}

	event := &models.Event{
		ID:           "ev-atomic-idem",
		OwnerID:      "host-1",
		Status:       models.EventStatusActive,
		RotationMode: models.RotationModeFairPriority,
		QueueVersion: 2,
	}
	_ = evStore.CreateEvent(context.Background(), event)

	now := time.Now().UTC()
	r1 := &models.SongRequest{ID: "r1", EventID: event.ID, SingerName: "Alice", Status: models.StatusAccepted, Rank: 1, CreatedAt: now.Add(-3 * time.Minute)}
	r2 := &models.SongRequest{ID: "r2", EventID: event.ID, SingerName: "Dave", Status: models.StatusAccepted, Rank: 2, CreatedAt: now.Add(-2 * time.Minute)}
	r3 := &models.SongRequest{ID: "r3", EventID: event.ID, SingerName: "Charlie", Status: models.StatusAccepted, Rank: 3, CreatedAt: now.Add(-1 * time.Minute)}
	r4 := &models.SongRequest{ID: "r4", EventID: event.ID, SingerName: "Dave", Status: models.StatusAccepted, Rank: 4, CreatedAt: now}
	_ = qStore.CreateRequest(context.Background(), r1)
	_ = qStore.CreateRequest(context.Background(), r2)
	_ = qStore.CreateRequest(context.Background(), r3)
	_ = qStore.CreateRequest(context.Background(), r4)
	qStore.order = []string{"r1", "r2", "r3", "r4"}

	rFrank := &models.SongRequest{ID: "rFrank", EventID: event.ID, SingerName: "Frank", Status: models.StatusPending, Rank: 888888, CreatedAt: now}
	_ = qStore.CreateRequest(context.Background(), rFrank)

	patchBody := `{"status":"accepted","expected_queue_version":2}`
	rPatch1 := httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-atomic-idem/requests/rFrank", bytes.NewBufferString(patchBody))
	rPatch1.SetPathValue("id", event.ID)
	rPatch1.SetPathValue("requestID", "rFrank")
	rPatch1.Header.Set("Idempotency-Key", "33333333-3333-4333-8333-333333333333")
	rPatch1 = rPatch1.WithContext(context.WithValue(rPatch1.Context(), userContextKey, &models.User{ID: "host-1", IsHost: true}))
	wPatch1 := httptest.NewRecorder()
	handler.UpdateEventRequest(wPatch1, rPatch1)

	if wPatch1.Code != http.StatusOK {
		t.Fatalf("first idempotent accept failed: %d: %s", wPatch1.Code, wPatch1.Body.String())
	}
	var res1 models.SongRequest
	_ = json.Unmarshal(wPatch1.Body.Bytes(), &res1)
	if res1.Rank != 4 {
		t.Fatalf("expected Frank to receive rank 4, got %d", res1.Rank)
	}

	rPatch2 := httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-atomic-idem/requests/rFrank", bytes.NewBufferString(patchBody))
	rPatch2.SetPathValue("id", event.ID)
	rPatch2.SetPathValue("requestID", "rFrank")
	rPatch2.Header.Set("Idempotency-Key", "33333333-3333-4333-8333-333333333333")
	rPatch2 = rPatch2.WithContext(context.WithValue(rPatch2.Context(), userContextKey, &models.User{ID: "host-1", IsHost: true}))
	wPatch2 := httptest.NewRecorder()
	handler.UpdateEventRequest(wPatch2, rPatch2)

	if wPatch2.Code != http.StatusOK {
		t.Fatalf("replay failed: %d: %s", wPatch2.Code, wPatch2.Body.String())
	}
	var res2 models.SongRequest
	_ = json.Unmarshal(wPatch2.Body.Bytes(), &res2)
	if res2.Rank != 4 {
		t.Fatalf("expected replayed response to match rank 4, got %d", res2.Rank)
	}
}

func TestQueueHandler_PendingToUpNext_AtomicNoReorder(t *testing.T) {
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	condStore := &conditionalTestQueueStore{testQueueStore: qStore, currentVersion: 1}
	handler := &QueueHandler{Events: evStore, Requests: condStore}

	event := &models.Event{
		ID:           "ev-pending-upnext",
		OwnerID:      "host-1",
		Status:       models.EventStatusActive,
		RotationMode: models.RotationModeRoundRobin,
		QueueVersion: 1,
	}
	_ = evStore.CreateEvent(context.Background(), event)

	// Single existing active request, so pending -> up_next does not displace any requests
	r1 := &models.SongRequest{
		ID:         "r1",
		EventID:    event.ID,
		SingerName: "Alice",
		Status:     models.StatusAccepted,
		Rank:       1,
		CreatedAt:  time.Now().UTC().Add(-5 * time.Minute),
	}
	_ = qStore.CreateRequest(context.Background(), r1)
	qStore.order = []string{"r1"}

	rPending := &models.SongRequest{
		ID:         "rPending",
		EventID:    event.ID,
		SingerName: "Bob",
		Status:     models.StatusPending,
		CreatedAt:  time.Now().UTC(),
	}
	_ = qStore.CreateRequest(context.Background(), rPending)

	patchBody := `{"status":"up_next"}`
	rPatch := httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-pending-upnext/requests/rPending", bytes.NewBufferString(patchBody))
	rPatch.SetPathValue("id", event.ID)
	rPatch.SetPathValue("requestID", "rPending")
	rPatch = rPatch.WithContext(context.WithValue(rPatch.Context(), userContextKey, &models.User{ID: "host-1", IsHost: true}))
	wPatch := httptest.NewRecorder()
	handler.UpdateEventRequest(wPatch, rPatch)

	if wPatch.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", wPatch.Code, wPatch.Body.String())
	}

	savedPending, err := qStore.GetRequest(context.Background(), "rPending")
	if err != nil {
		t.Fatalf("failed to get request: %v", err)
	}
	if savedPending.Status != models.StatusUpNext {
		t.Fatalf("expected status up_next, got %s", savedPending.Status)
	}
	if savedPending.Rank != 2 {
		t.Fatalf("expected rank 2, got %d", savedPending.Rank)
	}
	// Verify that atomicStore.UpdateRequestAndReorder was invoked, bumping condStore.currentVersion
	if condStore.currentVersion != 2 {
		t.Fatalf("expected currentVersion 2 via atomicStore, got %d", condStore.currentVersion)
	}
}

func ptrInt64(value int64) *int64 { return &value }

func TestQueueHandler_UpdateEventRequest_EditSongDetails(t *testing.T) {
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	handler := &QueueHandler{Events: evStore, Requests: qStore}

	event := &models.Event{
		ID:           "ev-edit-details",
		OwnerID:      "host-1",
		Status:       models.EventStatusActive,
		RotationMode: models.RotationModeFairPriority,
		QueueVersion: 1,
	}
	_ = evStore.CreateEvent(context.Background(), event)

	origReq := &models.SongRequest{
		ID:         "req-edit-1",
		EventID:    event.ID,
		SingerName: "Sarah",
		SongTitle:  "Bohemian Rhapsody",
		SongArtist: "Queen",
		SongID:     "song-old-123",
		KeyChange:  0,
		Notes:      "",
		Status:     models.StatusAccepted,
		Rank:       10,
		Round:      1,
		CreatedAt:  time.Now().UTC(),
	}
	_ = qStore.CreateRequest(context.Background(), origReq)

	// 1. Host updates song details
	body := `{
		"singer_name": "Sara",
		"song_title": "Somebody to Love",
		"song_artist": "Queen",
		"song_id": "song-new-456",
		"key_change": 1,
		"notes": "Happy Birthday!"
	}`
	rPatch := httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-edit-details/requests/req-edit-1", bytes.NewBufferString(body))
	rPatch.SetPathValue("id", "ev-edit-details")
	rPatch.SetPathValue("requestID", "req-edit-1")
	rPatch = rPatch.WithContext(context.WithValue(rPatch.Context(), userContextKey, &models.User{ID: "host-1", IsHost: true}))
	wPatch := httptest.NewRecorder()
	handler.UpdateEventRequest(wPatch, rPatch)

	if wPatch.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", wPatch.Code, wPatch.Body.String())
	}

	saved, err := qStore.GetRequest(context.Background(), "req-edit-1")
	if err != nil {
		t.Fatalf("failed to load saved request: %v", err)
	}
	if saved.SingerName != "Sara" {
		t.Errorf("expected singer_name Sara, got %s", saved.SingerName)
	}
	if saved.SongTitle != "Somebody to Love" {
		t.Errorf("expected song_title 'Somebody to Love', got %s", saved.SongTitle)
	}
	if saved.SongArtist != "Queen" {
		t.Errorf("expected song_artist Queen, got %s", saved.SongArtist)
	}
	if saved.SongID != "song-new-456" {
		t.Errorf("expected song_id 'song-new-456', got %s", saved.SongID)
	}
	if saved.KeyChange != 1 {
		t.Errorf("expected key_change 1, got %d", saved.KeyChange)
	}
	if saved.Notes != "Happy Birthday!" {
		t.Errorf("expected notes 'Happy Birthday!', got %s", saved.Notes)
	}
	if saved.Status != models.StatusAccepted {
		t.Errorf("expected status to remain accepted, got %s", saved.Status)
	}
	if saved.Rank != 10 {
		t.Errorf("expected rank to remain 10, got %d", saved.Rank)
	}

	// 2. Empty song_title should be rejected with 400 Bad Request
	badBody := `{"song_title": "   "}`
	rBad := httptest.NewRequest(http.MethodPatch, "/api/v1/events/ev-edit-details/requests/req-edit-1", bytes.NewBufferString(badBody))
	rBad.SetPathValue("id", "ev-edit-details")
	rBad.SetPathValue("requestID", "req-edit-1")
	rBad = rBad.WithContext(context.WithValue(rBad.Context(), userContextKey, &models.User{ID: "host-1", IsHost: true}))
	wBad := httptest.NewRecorder()
	handler.UpdateEventRequest(wBad, rBad)

	if wBad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 BadRequest for empty song_title, got %d", wBad.Code)
	}
}

func TestQueueHandler_WriteInRequestPolicy(t *testing.T) {
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	handler := &QueueHandler{Events: evStore, Requests: qStore, Secret: []byte("test-secret-with-at-least-32-bytes")}

	_ = evStore.CreateEvent(context.Background(), &models.Event{
		ID:            "ev-nowi",
		OwnerID:       "host-1",
		SongbookID:    "sb-1",
		Status:        models.EventStatusActive,
		AllowWriteIns: false,
		QueueVersion:  1,
	})
	_ = evStore.CreateEvent(context.Background(), &models.Event{
		ID:            "ev-wi",
		OwnerID:       "host-1",
		SongbookID:    "sb-1",
		Status:        models.EventStatusActive,
		AllowWriteIns: true,
		AutoAccept:    true,
		QueueVersion:  1,
	})

	// 1. Write-in rejected when allow_write_ins=false
	reqDenied := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-nowi/requests", strings.NewReader(`{"is_write_in":true,"song_id":"ignored","song_title":"Pink Pony Club","song_artist":"Chappell Roan","singer_name":"Sam"}`))
	reqDenied.SetPathValue("id", "ev-nowi")
	wDenied := httptest.NewRecorder()
	handler.CreateEventRequest(wDenied, reqDenied)
	if wDenied.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden when write-ins disabled, got %d (%s)", wDenied.Code, wDenied.Body.String())
	}

	// 2. Write-in accepted when allow_write_ins=true (clears song_id and sets is_write_in=true)
	reqOK := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-wi/requests", strings.NewReader(`{"is_write_in":true,"song_id":"should-clear","song_title":"Pink Pony Club","song_artist":"Chappell Roan","singer_name":"Sam"}`))
	reqOK.SetPathValue("id", "ev-wi")
	wOK := httptest.NewRecorder()
	handler.CreateEventRequest(wOK, reqOK)
	if wOK.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created when write-ins enabled, got %d (%s)", wOK.Code, wOK.Body.String())
	}
	var created models.SongRequest
	if err := json.Unmarshal(wOK.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created: %v", err)
	}
	if !created.IsWriteIn || created.SongID != "" {
		t.Fatalf("expected is_write_in=true and empty song_id, got %+v", created)
	}

	// 3. Public queue includes is_write_in=true
	reqPub := httptest.NewRequest(http.MethodGet, "/api/v1/events/ev-wi/queue", nil)
	reqPub.SetPathValue("id", "ev-wi")
	wPub := httptest.NewRecorder()
	handler.GetEventPublicQueue(wPub, reqPub)
	if wPub.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for public queue, got %d", wPub.Code)
	}
	var pubResp struct {
		Queue []models.PublicQueueItem `json:"queue"`
	}
	if err := json.Unmarshal(wPub.Body.Bytes(), &pubResp); err != nil {
		t.Fatalf("unmarshal public queue: %v", err)
	}
	if len(pubResp.Queue) != 1 || !pubResp.Queue[0].IsWriteIn {
		t.Fatalf("expected public queue item with is_write_in=true, got %+v", pubResp.Queue)
	}
}
