package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"micup.live/server/internal/config"
	"micup.live/server/internal/handlers"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
	pgstore "micup.live/server/internal/store/postgres"
)

type contractRequestSpec struct {
	Method        string            `json:"method"`
	Path          string            `json:"path"`
	RequiredScope string            `json:"required_scope,omitempty"`
	Headers       map[string]string `json:"headers"`
	Body          json.RawMessage   `json:"body,omitempty"`
}

type contractResponseSpec struct {
	Status         int                    `json:"status"`
	Headers        map[string]string      `json:"headers"`
	Body           json.RawMessage        `json:"body,omitempty"`
	BodyBytes      *int                   `json:"body_bytes,omitempty"`
	ExpectedFields map[string]interface{} `json:"expected_fields,omitempty"`
	OmittedFields  []string               `json:"omitted_fields,omitempty"`
	ForbiddenItems []string               `json:"forbidden_item_fields,omitempty"`
	ExcludedStatus []string               `json:"excluded_statuses,omitempty"`
}

func loadContractFixture(t *testing.T, name string, target interface{}) {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "contracts", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read contract fixture %s: %v", path, err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("decode contract fixture %s: %v", path, err)
	}
}

type contractFixtureState struct {
	store.EventStore
	store.SongbookStore
	store.RequestStore
	users       map[string]*models.User
	keys        map[string]*models.DeveloperKey
	events      map[string]*models.Event
	songbooks   map[string]*models.Songbook
	requests    map[string]*models.SongRequest
	idempotency map[string]*contractIdempotencyRecord
}

type contractIdempotencyRecord struct {
	ownerID     string
	route       string
	resourceID  string
	requestHash string
	result      *store.QueueMutationResult
}

func newContractFixtureState() *contractFixtureState {
	return &contractFixtureState{
		users: map[string]*models.User{
			"usr_host_01": {
				ID:          "usr_host_01",
				Email:       "host@micup.live",
				DisplayName: "KJ Host",
				IsHost:      true,
			},
		},
		keys:   make(map[string]*models.DeveloperKey),
		events: make(map[string]*models.Event),
		songbooks: map[string]*models.Songbook{
			"sb_contract_01": {
				ID:       "sb_contract_01",
				PublicID: "pub_sb_contract_01",
				OwnerID:  "usr_host_01",
				Name:     "Main Host Songbook",
				IsPublic: true,
			},
		},
		requests:    make(map[string]*models.SongRequest),
		idempotency: make(map[string]*contractIdempotencyRecord),
	}
}

func (s *contractFixtureState) registerKey(rawToken string, scopes ...string) {
	s.keys[rawToken] = &models.DeveloperKey{
		ID:           "key_" + rawToken,
		OwnerID:      "usr_host_01",
		ScopeVersion: 1,
		Scopes:       scopes,
	}
}

func (s *contractFixtureState) GetEvent(_ context.Context, id string) (*models.Event, error) {
	ev, ok := s.events[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	cp := *ev
	return &cp, nil
}

func (s *contractFixtureState) ListEventsByOwner(_ context.Context, ownerID string) ([]*models.Event, error) {
	var out []*models.Event
	for _, ev := range s.events {
		if ev.OwnerID == ownerID {
			cp := *ev
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (s *contractFixtureState) CreateEvent(_ context.Context, ev *models.Event) error {
	cp := *ev
	s.events[ev.ID] = &cp
	return nil
}

func (s *contractFixtureState) GetSongbook(_ context.Context, id string) (*models.Songbook, error) {
	sb, ok := s.songbooks[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	cp := *sb
	return &cp, nil
}

func (s *contractFixtureState) GetRequest(_ context.Context, id string) (*models.SongRequest, error) {
	req, ok := s.requests[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	cp := *req
	return &cp, nil
}

func (s *contractFixtureState) ListRequestsByEvent(_ context.Context, eventID string) ([]*models.SongRequest, error) {
	var out []*models.SongRequest
	for _, req := range s.requests {
		if req.EventID == eventID {
			cp := *req
			out = append(out, &cp)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		rI, rJ := out[i].Round, out[j].Round
		if rI > 0 && rJ > 0 && rI != rJ {
			return rI < rJ
		}
		if out[i].Rank != out[j].Rank {
			return out[i].Rank < out[j].Rank
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (s *contractFixtureState) UpdateRequest(_ context.Context, req *models.SongRequest) error {
	cp := *req
	s.requests[req.ID] = &cp
	if ev, ok := s.events[req.EventID]; ok {
		ev.QueueVersion++
	}
	return nil
}

func (s *contractFixtureState) UpdateRequestWithQueueVersion(ctx context.Context, req *models.SongRequest, expectedVersion int64) error {
	return s.applyUpdateAndReorder(ctx, req, nil, &expectedVersion)
}

func (s *contractFixtureState) UpdateRequestAndReorder(ctx context.Context, req *models.SongRequest, orderedIDs []string) error {
	return s.applyUpdateAndReorder(ctx, req, orderedIDs, nil)
}

func (s *contractFixtureState) UpdateRequestAndReorderWithQueueVersion(ctx context.Context, req *models.SongRequest, orderedIDs []string, expectedVersion int64) error {
	return s.applyUpdateAndReorder(ctx, req, orderedIDs, &expectedVersion)
}

func (s *contractFixtureState) ReorderRequests(ctx context.Context, eventID string, orderedIDs []string) error {
	return s.applyReorder(ctx, eventID, orderedIDs, nil)
}

func (s *contractFixtureState) ReorderRequestsWithQueueVersion(ctx context.Context, eventID string, orderedIDs []string, expectedVersion int64) error {
	return s.applyReorder(ctx, eventID, orderedIDs, &expectedVersion)
}

func (s *contractFixtureState) GetIdempotencyResult(_ context.Context, key, ownerID, route, resourceID, requestHash string) (*store.QueueMutationResult, bool, error) {
	rec, ok := s.idempotency[key]
	if !ok {
		return nil, false, nil
	}
	if rec.ownerID != ownerID || rec.route != route || rec.resourceID != resourceID || rec.requestHash != requestHash {
		return nil, false, &store.IdempotencyConflictError{}
	}
	return &store.QueueMutationResult{
		QueueVersion: rec.result.QueueVersion,
		StatusCode:   rec.result.StatusCode,
		Body:         append([]byte(nil), rec.result.Body...),
		Replay:       true,
	}, true, nil
}

func (s *contractFixtureState) UpdateRequestWithIdempotency(ctx context.Context, req *models.SongRequest, expectedVersion *int64, key, ownerID, route, requestHash string) (*store.QueueMutationResult, error) {
	return s.UpdateRequestAndReorderWithIdempotency(ctx, req, nil, expectedVersion, key, ownerID, route, requestHash)
}

func (s *contractFixtureState) UpdateRequestAndReorderWithIdempotency(ctx context.Context, req *models.SongRequest, orderedIDs []string, expectedVersion *int64, key, ownerID, route, requestHash string) (*store.QueueMutationResult, error) {
	if res, found, err := s.GetIdempotencyResult(ctx, key, ownerID, route, req.ID, requestHash); err != nil || found {
		return res, err
	}
	if err := s.applyUpdateAndReorder(ctx, req, orderedIDs, expectedVersion); err != nil {
		return nil, err
	}
	updated := s.requests[req.ID]
	body, err := json.Marshal(updated)
	if err != nil {
		return nil, err
	}
	res := &store.QueueMutationResult{
		QueueVersion: s.events[req.EventID].QueueVersion,
		StatusCode:   http.StatusOK,
		Body:         body,
	}
	s.idempotency[key] = &contractIdempotencyRecord{
		ownerID:     ownerID,
		route:       route,
		resourceID:  req.ID,
		requestHash: requestHash,
		result:      res,
	}
	return res, nil
}

func (s *contractFixtureState) ReorderRequestsWithIdempotency(ctx context.Context, eventID string, orderedIDs []string, expectedVersion *int64, key, ownerID, route, requestHash string) (*store.QueueMutationResult, error) {
	if res, found, err := s.GetIdempotencyResult(ctx, key, ownerID, route, eventID, requestHash); err != nil || found {
		return res, err
	}
	if err := s.applyReorder(ctx, eventID, orderedIDs, expectedVersion); err != nil {
		return nil, err
	}
	res := &store.QueueMutationResult{
		QueueVersion: s.events[eventID].QueueVersion,
		StatusCode:   http.StatusOK,
		Body:         []byte(`{"status":"ok"}`),
	}
	s.idempotency[key] = &contractIdempotencyRecord{
		ownerID:     ownerID,
		route:       route,
		resourceID:  eventID,
		requestHash: requestHash,
		result:      res,
	}
	return res, nil
}

func (s *contractFixtureState) applyUpdateAndReorder(_ context.Context, req *models.SongRequest, orderedIDs []string, expectedVersion *int64) error {
	ev, ok := s.events[req.EventID]
	if !ok {
		return os.ErrNotExist
	}
	if expectedVersion != nil && ev.QueueVersion != *expectedVersion {
		return &store.QueueVersionConflictError{
			Expected: *expectedVersion,
			Current:  ev.QueueVersion,
		}
	}
	cp := *req
	s.requests[req.ID] = &cp
	for idx, id := range orderedIDs {
		if item, exists := s.requests[id]; exists && item.EventID == req.EventID {
			item.Rank = int64(idx + 1)
		}
	}
	ev.QueueVersion++
	return nil
}

func (s *contractFixtureState) applyReorder(_ context.Context, eventID string, orderedIDs []string, expectedVersion *int64) error {
	ev, ok := s.events[eventID]
	if !ok {
		return os.ErrNotExist
	}
	if expectedVersion != nil && ev.QueueVersion != *expectedVersion {
		return &store.QueueVersionConflictError{
			Expected: *expectedVersion,
			Current:  ev.QueueVersion,
		}
	}
	for idx, id := range orderedIDs {
		item, exists := s.requests[id]
		if !exists || item.EventID != eventID {
			return fmt.Errorf("request %s not in event %s: %w", id, eventID, os.ErrInvalid)
		}
		item.Rank = int64(idx + 1)
	}
	if len(orderedIDs) > 0 {
		ev.QueueVersion++
	}
	return nil
}

func newContractRouter(t *testing.T, state *contractFixtureState) http.Handler {
	t.Helper()
	const secret = "contract-test-signing-secret-at-least-32-bytes"
	router, err := NewRouter(
		config.Config{
			Environment:           config.Development,
			AuthSecret:            secret,
			AllowedOrigins:        []string{"http://localhost:5173"},
			DeveloperKeyScopeMode: handlers.DeveloperKeyScopeEnforce,
		},
		Dependencies{
			Auth:          &keyRouteAuthStore{users: state.users},
			DeveloperKeys: &keyRouteStore{keys: state.keys},
			Events:        state,
			Songbooks:     state,
			Requests:      state,
		},
	)
	if err != nil {
		t.Fatalf("create contract router: %v", err)
	}
	return router
}

func executeContractRequest(t *testing.T, router http.Handler, spec contractRequestSpec) *httptest.ResponseRecorder {
	t.Helper()
	var bodyReader *bytes.Reader
	if len(spec.Body) > 0 {
		bodyReader = bytes.NewReader(spec.Body)
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(spec.Method, spec.Path, bodyReader)
	for k, v := range spec.Headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func assertJSONEqual(t *testing.T, label string, gotRaw, wantRaw []byte) {
	t.Helper()
	decode := func(b []byte) (interface{}, error) {
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		var v interface{}
		err := dec.Decode(&v)
		return v, err
	}
	got, err := decode(gotRaw)
	if err != nil {
		t.Fatalf("%s: unmarshal actual JSON %q: %v", label, string(gotRaw), err)
	}
	want, err := decode(wantRaw)
	if err != nil {
		t.Fatalf("%s: unmarshal expected JSON %q: %v", label, string(wantRaw), err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: JSON mismatch\ngot:  %s\nwant: %s", label, string(gotRaw), string(wantRaw))
	}
}

func assertResponseHeaders(t *testing.T, label string, got http.Header, want map[string]string) {
	t.Helper()
	for k, expected := range want {
		actual := got.Get(k)
		if actual != expected {
			t.Fatalf("%s: header %s = %q; want %q", label, k, actual, expected)
		}
	}
}

func TestContractFixtures_EventsList200AndEmptyAndForwardCompat(t *testing.T) {
	var fixture struct {
		Request                 contractRequestSpec  `json:"request"`
		Response                contractResponseSpec `json:"response"`
		EmptyResponse           contractResponseSpec `json:"empty_response"`
		ForwardCompatibleSample json.RawMessage      `json:"forward_compatible_wire_sample"`
	}
	loadContractFixture(t, "events_list_200.json", &fixture)

	state := newContractFixtureState()
	state.registerKey("mk_live_contract_events_read", handlers.DeveloperScopeEventsRead)

	// 1. Verify empty host event list returns {"events": []} (never null).
	router := newContractRouter(t, state)
	emptyRec := executeContractRequest(t, router, fixture.Request)
	if emptyRec.Code != fixture.EmptyResponse.Status {
		t.Fatalf("empty events status = %d; want %d", emptyRec.Code, fixture.EmptyResponse.Status)
	}
	assertResponseHeaders(t, "empty events", emptyRec.Header(), fixture.EmptyResponse.Headers)
	assertJSONEqual(t, "empty events body", emptyRec.Body.Bytes(), fixture.EmptyResponse.Body)

	// 2. Seed events from the fixture and verify exact wire response, RFC3339 timestamps, and omitted optional fields.
	var seededEnvelope struct {
		Events []*models.Event `json:"events"`
	}
	if err := json.Unmarshal(fixture.Response.Body, &seededEnvelope); err != nil {
		t.Fatalf("unmarshal fixture events: %v", err)
	}
	for _, ev := range seededEnvelope.Events {
		ev.OwnerID = "usr_host_01"
		state.events[ev.ID] = ev
	}

	rec := executeContractRequest(t, router, fixture.Request)
	if rec.Code != fixture.Response.Status {
		t.Fatalf("events list status = %d, body = %s; want %d", rec.Code, rec.Body.String(), fixture.Response.Status)
	}
	assertResponseHeaders(t, "events list", rec.Header(), fixture.Response.Headers)
	assertJSONEqual(t, "events list body", rec.Body.Bytes(), fixture.Response.Body)

	// Verify RFC3339 timestamps, unexported owner_id exclusion, and omitted optional fields.
	var rawEnvelope struct {
		Events []map[string]interface{} `json:"events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rawEnvelope); err != nil {
		t.Fatalf("decode raw events envelope: %v", err)
	}
	if len(rawEnvelope.Events) != 2 {
		t.Fatalf("len(events) = %d; want 2", len(rawEnvelope.Events))
	}
	for _, item := range rawEnvelope.Events {
		if _, leakedOwner := item["owner_id"]; leakedOwner {
			t.Fatalf("event %v leaked private owner_id field in wire response", item["id"])
		}
		for _, tsField := range []string{"created_at", "updated_at"} {
			tsStr, _ := item[tsField].(string)
			if _, err := time.Parse(time.RFC3339Nano, tsStr); err != nil {
				t.Fatalf("event %v field %s = %q is not valid RFC3339: %v", item["id"], tsField, tsStr, err)
			}
		}
	}
	if _, hasEndedAt := rawEnvelope.Events[0]["ended_at"]; hasEndedAt {
		t.Fatalf("expected active event evt_contract_01 to omit ended_at")
	}
	secondEvent := rawEnvelope.Events[1]
	for _, omitted := range []string{"tip_url", "announcement"} {
		if _, exists := secondEvent[omitted]; exists {
			t.Fatalf("expected optional field %q to be omitted on evt_contract_02, got %v", omitted, secondEvent[omitted])
		}
	}
	if endedAt, _ := secondEvent["ended_at"].(string); endedAt == "" {
		t.Fatalf("expected ended_at RFC3339 timestamp on ended event evt_contract_02")
	}

	// 3. Verify forward-compatible wire sample with unknown future fields decodes cleanly.
	var forwardDecoded struct {
		Events []*models.Event `json:"events"`
	}
	if err := json.Unmarshal(fixture.ForwardCompatibleSample, &forwardDecoded); err != nil {
		t.Fatalf("forward-compatible sample failed to unmarshal: %v", err)
	}
	if len(forwardDecoded.Events) != 1 || forwardDecoded.Events[0].ID != "evt_contract_01" {
		t.Fatalf("unexpected forward-compatible decoded event: %+v", forwardDecoded.Events)
	}
}

func seedQueueContractFixtureState(t *testing.T) (*contractFixtureState, http.Handler) {
	t.Helper()
	var eventsFixture struct {
		Response struct {
			Body struct {
				Events []*models.Event `json:"events"`
			} `json:"body"`
		} `json:"response"`
	}
	loadContractFixture(t, "events_list_200.json", &eventsFixture)

	var hostQueueFixture struct {
		Response struct {
			Body struct {
				Requests []*models.SongRequest `json:"requests"`
			} `json:"body"`
		} `json:"response"`
	}
	loadContractFixture(t, "queue_host_requests_200.json", &hostQueueFixture)

	state := newContractFixtureState()
	state.registerKey("mk_live_contract_events_read", handlers.DeveloperScopeEventsRead)
	state.registerKey("mk_live_contract_requests_read", handlers.DeveloperScopeRequestsRead)
	state.registerKey("mk_live_contract_requests_write", handlers.DeveloperScopeRequestsWrite)

	for _, ev := range eventsFixture.Response.Body.Events {
		cp := *ev
		cp.OwnerID = "usr_host_01"
		state.events[ev.ID] = &cp
	}
	for _, req := range hostQueueFixture.Response.Body.Requests {
		cp := *req
		state.requests[req.ID] = &cp
	}

	return state, newContractRouter(t, state)
}

func TestContractFixtures_QueueHost200Public200AndPoll204(t *testing.T) {
	_, router := seedQueueContractFixtureState(t)

	// 1. Host queue 200 (GET /api/v1/events/{id}/requests): all 6 canonical statuses, omitted song_id, X-Queue-Version.
	var hostFixture struct {
		Request                 contractRequestSpec  `json:"request"`
		Response                contractResponseSpec `json:"response"`
		ForwardCompatibleSample json.RawMessage      `json:"forward_compatible_wire_sample"`
	}
	loadContractFixture(t, "queue_host_requests_200.json", &hostFixture)

	hostRec := executeContractRequest(t, router, hostFixture.Request)
	if hostRec.Code != hostFixture.Response.Status {
		t.Fatalf("host queue status = %d, body = %s; want %d", hostRec.Code, hostRec.Body.String(), hostFixture.Response.Status)
	}
	assertResponseHeaders(t, "host queue 200", hostRec.Header(), hostFixture.Response.Headers)
	assertJSONEqual(t, "host queue 200 body", hostRec.Body.Bytes(), hostFixture.Response.Body)

	// Verify all 6 canonical statuses and omitted optional song_id in raw JSON.
	var rawHostBody struct {
		Requests []map[string]interface{} `json:"requests"`
	}
	if err := json.Unmarshal(hostRec.Body.Bytes(), &rawHostBody); err != nil {
		t.Fatalf("decode raw host queue JSON: %v", err)
	}
	seenStatuses := make(map[string]bool)
	omittedSongIDCount := 0
	for _, item := range rawHostBody.Requests {
		status, _ := item["status"].(string)
		seenStatuses[status] = true
		if _, hasSongID := item["song_id"]; !hasSongID {
			omittedSongIDCount++
		}
		for _, tsField := range []string{"created_at", "updated_at"} {
			tsStr, _ := item[tsField].(string)
			if _, err := time.Parse(time.RFC3339Nano, tsStr); err != nil {
				t.Fatalf("request %v field %s = %q is not valid RFC3339: %v", item["id"], tsField, tsStr, err)
			}
		}
	}
	for _, canonical := range []models.RequestStatus{
		models.StatusPending,
		models.StatusAccepted,
		models.StatusUpNext,
		models.StatusCompleted,
		models.StatusRejected,
		models.StatusCancelled,
	} {
		if !seenStatuses[string(canonical)] {
			t.Fatalf("host queue fixture response missing canonical status %q; got %v", canonical, seenStatuses)
		}
	}
	if omittedSongIDCount == 0 {
		t.Fatalf("expected at least one request in host queue fixture to omit optional song_id")
	}

	// Verify forward-compatible wire sample with unknown future fields decodes cleanly.
	var forwardHost struct {
		Requests     []*models.SongRequest `json:"requests"`
		CurrentRound int                   `json:"current_round"`
		QueueVersion int64                 `json:"queue_version"`
	}
	if err := json.Unmarshal(hostFixture.ForwardCompatibleSample, &forwardHost); err != nil {
		t.Fatalf("unmarshal forward-compatible host queue sample: %v", err)
	}
	if len(forwardHost.Requests) != 1 || forwardHost.Requests[0].SongID != "" {
		t.Fatalf("unexpected forward-compatible host queue request: %+v", forwardHost.Requests)
	}

	// 2. Public queue 200 (GET /api/v1/events/{id}/queue): only up_next and accepted, private fields omitted.
	var publicFixture struct {
		Request  contractRequestSpec  `json:"request"`
		Response contractResponseSpec `json:"response"`
	}
	loadContractFixture(t, "queue_public_200.json", &publicFixture)

	pubRec := executeContractRequest(t, router, publicFixture.Request)
	if pubRec.Code != publicFixture.Response.Status {
		t.Fatalf("public queue status = %d, body = %s; want %d", pubRec.Code, pubRec.Body.String(), publicFixture.Response.Status)
	}
	assertResponseHeaders(t, "public queue 200", pubRec.Header(), publicFixture.Response.Headers)
	assertJSONEqual(t, "public queue 200 body", pubRec.Body.Bytes(), publicFixture.Response.Body)

	var rawPublicBody struct {
		Queue []map[string]interface{} `json:"queue"`
	}
	if err := json.Unmarshal(pubRec.Body.Bytes(), &rawPublicBody); err != nil {
		t.Fatalf("decode raw public queue JSON: %v", err)
	}
	excluded := make(map[string]bool)
	for _, st := range publicFixture.Response.ExcludedStatus {
		excluded[st] = true
	}
	for _, item := range rawPublicBody.Queue {
		st, _ := item["status"].(string)
		if excluded[st] {
			t.Fatalf("public queue leaked excluded status %q in item %v", st, item)
		}
		for _, forbidden := range publicFixture.Response.ForbiddenItems {
			if _, exists := item[forbidden]; exists {
				t.Fatalf("public queue leaked private field %q in item %v", forbidden, item)
			}
		}
	}

	// 3. Conditional polling 204 No Content and stale-since 200 OK.
	var pollFixture struct {
		Cases []struct {
			Name     string               `json:"name"`
			Request  contractRequestSpec  `json:"request"`
			Response contractResponseSpec `json:"response"`
		} `json:"cases"`
	}
	loadContractFixture(t, "queue_poll_204.json", &pollFixture)

	for _, tc := range pollFixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			rec := executeContractRequest(t, router, tc.Request)
			if rec.Code != tc.Response.Status {
				t.Fatalf("status = %d, body = %q; want %d", rec.Code, rec.Body.String(), tc.Response.Status)
			}
			assertResponseHeaders(t, tc.Name, rec.Header(), tc.Response.Headers)
			if tc.Response.BodyBytes != nil && rec.Body.Len() != *tc.Response.BodyBytes {
				t.Fatalf("body length = %d (%q); want %d bytes", rec.Body.Len(), rec.Body.String(), *tc.Response.BodyBytes)
			}
		})
	}

	// 4. Verify CORS preflight and exposed headers for queue polling and idempotent mutations.
	corsRec := executeContractRequest(t, router, contractRequestSpec{
		Method: http.MethodOptions,
		Path:   "/api/v1/events/evt_contract_01/requests",
		Headers: map[string]string{
			"Origin":                        "http://localhost:5173",
			"Access-Control-Request-Method": "PATCH",
		},
	})
	if corsRec.Code != http.StatusNoContent {
		t.Fatalf("CORS preflight status = %d; want 204", corsRec.Code)
	}
	const wantExposed = "X-Queue-Version, Content-Disposition, RateLimit-Limit, RateLimit-Remaining, Retry-After, X-Deletion-Token"
	if exposed := corsRec.Header().Get("Access-Control-Expose-Headers"); exposed != wantExposed {
		t.Fatalf("Access-Control-Expose-Headers = %q; want %q", exposed, wantExposed)
	}
	const wantAllowed = "Content-Type, Authorization, X-Session-ID, X-Friendly-Name, Idempotency-Key, X-Cancel-Token, X-Edit-Token, X-API-Key, X-Deletion-Token"
	if allowed := corsRec.Header().Get("Access-Control-Allow-Headers"); allowed != wantAllowed {
		t.Fatalf("Access-Control-Allow-Headers = %q; want %q", allowed, wantAllowed)
	}
}

func TestContractFixtures_StatusMutationsReorderConflictsAndIdempotentReplay(t *testing.T) {
	state, router := seedQueueContractFixtureState(t)

	// 1. Execute status mutations from request_status_mutations.json.
	var mutationsFixture struct {
		Mutations []struct {
			Name     string               `json:"name"`
			Request  contractRequestSpec  `json:"request"`
			Response contractResponseSpec `json:"response"`
		} `json:"mutations"`
	}
	loadContractFixture(t, "request_status_mutations.json", &mutationsFixture)

	for _, m := range mutationsFixture.Mutations {
		t.Run(m.Name, func(t *testing.T) {
			rec := executeContractRequest(t, router, m.Request)
			if rec.Code != m.Response.Status {
				t.Fatalf("status = %d, body = %s; want %d", rec.Code, rec.Body.String(), m.Response.Status)
			}
			assertResponseHeaders(t, m.Name, rec.Header(), m.Response.Headers)

			var gotMap map[string]interface{}
			if err := json.Unmarshal(rec.Body.Bytes(), &gotMap); err != nil {
				t.Fatalf("decode mutation response JSON: %v", err)
			}
			for k, wantVal := range m.Response.ExpectedFields {
				if !reflect.DeepEqual(gotMap[k], wantVal) {
					t.Fatalf("field %q = %v (%T); want %v (%T)", k, gotMap[k], gotMap[k], wantVal, wantVal)
				}
			}
			for _, omitted := range m.Response.OmittedFields {
				if _, exists := gotMap[omitted]; exists {
					t.Fatalf("expected optional field %q to be omitted, got %v", omitted, gotMap[omitted])
				}
			}
		})
	}

	// 2. Execute manual reorder from queue_reorder_200.json.
	var reorderFixture struct {
		Request                  contractRequestSpec  `json:"request"`
		Response                 contractResponseSpec `json:"response"`
		ExpectedActiveOrderAfter []string             `json:"expected_active_order_after"`
	}
	loadContractFixture(t, "queue_reorder_200.json", &reorderFixture)

	reorderRec := executeContractRequest(t, router, reorderFixture.Request)
	if reorderRec.Code != reorderFixture.Response.Status {
		t.Fatalf("reorder status = %d, body = %s; want %d", reorderRec.Code, reorderRec.Body.String(), reorderFixture.Response.Status)
	}
	assertResponseHeaders(t, "reorder 200", reorderRec.Header(), reorderFixture.Response.Headers)
	assertJSONEqual(t, "reorder 200 body", reorderRec.Body.Bytes(), reorderFixture.Response.Body)

	// Verify active queue order after reorder matches expected_active_order_after.
	pubRec := executeContractRequest(t, router, contractRequestSpec{
		Method: http.MethodGet,
		Path:   "/api/v1/events/evt_contract_01/queue",
	})
	var pubQueue struct {
		Queue        []models.PublicQueueItem `json:"queue"`
		QueueVersion int64                    `json:"queue_version"`
	}
	if err := json.Unmarshal(pubRec.Body.Bytes(), &pubQueue); err != nil {
		t.Fatalf("decode public queue after reorder: %v", err)
	}
	if len(pubQueue.Queue) != len(reorderFixture.ExpectedActiveOrderAfter) {
		t.Fatalf("active queue len = %d; want %d", len(pubQueue.Queue), len(reorderFixture.ExpectedActiveOrderAfter))
	}
	for i, wantID := range reorderFixture.ExpectedActiveOrderAfter {
		if pubQueue.Queue[i].ID != wantID {
			t.Fatalf("active queue[%d].ID = %s; want %s", i, pubQueue.Queue[i].ID, wantID)
		}
	}

	// 3. Execute version conflicts, idempotent replays, and idempotency payload conflicts from conflicts_and_idempotency.json.
	var conflictsFixture struct {
		VersionConflicts []struct {
			Name     string               `json:"name"`
			Request  contractRequestSpec  `json:"request"`
			Response contractResponseSpec `json:"response"`
		} `json:"version_conflicts"`
		IdempotentReplays []struct {
			Name                     string               `json:"name"`
			Request                  contractRequestSpec  `json:"request"`
			Response                 contractResponseSpec `json:"response"`
			EventQueueVersionRemains int64                `json:"event_queue_version_remains"`
		} `json:"idempotent_replays"`
		IdempotencyConflicts []struct {
			Name     string               `json:"name"`
			Request  contractRequestSpec  `json:"request"`
			Response contractResponseSpec `json:"response"`
		} `json:"idempotency_conflicts"`
	}
	loadContractFixture(t, "conflicts_and_idempotency.json", &conflictsFixture)

	for _, vc := range conflictsFixture.VersionConflicts {
		t.Run(vc.Name, func(t *testing.T) {
			rec := executeContractRequest(t, router, vc.Request)
			if rec.Code != vc.Response.Status {
				t.Fatalf("status = %d, body = %s; want %d", rec.Code, rec.Body.String(), vc.Response.Status)
			}
			assertResponseHeaders(t, vc.Name, rec.Header(), vc.Response.Headers)
			assertJSONEqual(t, vc.Name, rec.Body.Bytes(), vc.Response.Body)
		})
	}

	for _, replay := range conflictsFixture.IdempotentReplays {
		t.Run(replay.Name, func(t *testing.T) {
			rec := executeContractRequest(t, router, replay.Request)
			if rec.Code != replay.Response.Status {
				t.Fatalf("status = %d, body = %s; want %d", rec.Code, rec.Body.String(), replay.Response.Status)
			}
			assertResponseHeaders(t, replay.Name, rec.Header(), replay.Response.Headers)
			if len(replay.Response.Body) > 0 {
				assertJSONEqual(t, replay.Name, rec.Body.Bytes(), replay.Response.Body)
			}
			if len(replay.Response.ExpectedFields) > 0 {
				var gotMap map[string]interface{}
				if err := json.Unmarshal(rec.Body.Bytes(), &gotMap); err != nil {
					t.Fatalf("decode replay response: %v", err)
				}
				for k, wantVal := range replay.Response.ExpectedFields {
					if !reflect.DeepEqual(gotMap[k], wantVal) {
						t.Fatalf("replay field %q = %v; want %v", k, gotMap[k], wantVal)
					}
				}
			}
			if gotVer := state.events["evt_contract_01"].QueueVersion; gotVer != replay.EventQueueVersionRemains {
				t.Fatalf("event QueueVersion after replay = %d; want %d (must not increment twice)", gotVer, replay.EventQueueVersionRemains)
			}
		})
	}

	for _, ic := range conflictsFixture.IdempotencyConflicts {
		t.Run(ic.Name, func(t *testing.T) {
			beforeVer := state.events["evt_contract_01"].QueueVersion
			rec := executeContractRequest(t, router, ic.Request)
			if rec.Code != ic.Response.Status {
				t.Fatalf("status = %d, body = %s; want %d", rec.Code, rec.Body.String(), ic.Response.Status)
			}
			assertResponseHeaders(t, ic.Name, rec.Header(), ic.Response.Headers)
			assertJSONEqual(t, ic.Name, rec.Body.Bytes(), ic.Response.Body)
			if afterVer := state.events["evt_contract_01"].QueueVersion; afterVer != beforeVer {
				t.Fatalf("event QueueVersion changed on idempotency conflict: %d -> %d", beforeVer, afterVer)
			}
		})
	}
}

func TestContractFixtures_DeveloperScopeFailures403(t *testing.T) {
	state, _ := seedQueueContractFixtureState(t)

	var scopeFixture struct {
		ScopeMode string `json:"scope_mode"`
		Cases     []struct {
			Name          string               `json:"name"`
			GrantedScopes []string             `json:"granted_scopes"`
			Request       contractRequestSpec  `json:"request"`
			Response      contractResponseSpec `json:"response"`
		} `json:"cases"`
	}
	loadContractFixture(t, "developer_scope_failures_403.json", &scopeFixture)

	for _, tc := range scopeFixture.Cases {
		token := tc.Request.Headers["Authorization"]
		const prefix = "Bearer "
		if len(token) > len(prefix) {
			rawKey := token[len(prefix):]
			state.registerKey(rawKey, tc.GrantedScopes...)
		}
	}

	router := newContractRouter(t, state)
	beforeVer := state.events["evt_contract_01"].QueueVersion

	for _, tc := range scopeFixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			rec := executeContractRequest(t, router, tc.Request)
			if rec.Code != tc.Response.Status {
				t.Fatalf("status = %d, body = %s; want %d", rec.Code, rec.Body.String(), tc.Response.Status)
			}
			assertResponseHeaders(t, tc.Name, rec.Header(), tc.Response.Headers)
			assertJSONEqual(t, tc.Name, rec.Body.Bytes(), tc.Response.Body)
			if afterVer := state.events["evt_contract_01"].QueueVersion; afterVer != beforeVer {
				t.Fatalf("denied scope request mutated queue_version: %d -> %d ("+strconv.FormatInt(afterVer, 10)+")", beforeVer, afterVer)
			}
		})
	}
}

func assertContractResponseFields(t *testing.T, label string, body []byte, spec contractResponseSpec) {
	t.Helper()
	if len(spec.Body) > 0 {
		assertJSONEqual(t, label, body, spec.Body)
	}
	if len(spec.ExpectedFields) == 0 && len(spec.OmittedFields) == 0 {
		return
	}
	var gotMap map[string]interface{}
	if err := json.Unmarshal(body, &gotMap); err != nil {
		t.Fatalf("%s: decode response JSON: %v (body=%s)", label, err, string(body))
	}
	for k, wantVal := range spec.ExpectedFields {
		if !reflect.DeepEqual(gotMap[k], wantVal) {
			t.Fatalf("%s: field %q = %v (%T); want %v (%T)", label, k, gotMap[k], gotMap[k], wantVal, wantVal)
		}
	}
	for _, omitted := range spec.OmittedFields {
		if _, exists := gotMap[omitted]; exists {
			t.Fatalf("%s: expected optional field %q to be omitted, got %v", label, omitted, gotMap[omitted])
		}
	}
}

type schemaExecer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

func isValidFixtureSchema(schema string) bool {
	if !strings.HasPrefix(schema, "app_test_") || len(schema) != len("app_test_")+8 {
		return false
	}
	for _, r := range schema[len("app_test_"):] {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

func dsnWithSearchPath(dsn, schema string) (string, error) {
	if strings.Contains(dsn, "://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return "", err
		}
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		return u.String(), nil
	}
	return dsn + " search_path=" + schema, nil
}

func dropFixtureSchema(ctx context.Context, execer schemaExecer, schema string) error {
	if !isValidFixtureSchema(schema) {
		return fmt.Errorf("refusing to drop non-fixture schema %q", schema)
	}
	_, err := execer.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	if err != nil {
		return fmt.Errorf("cleanup fixture schema %s: %w", schema, err)
	}
	return nil
}

func openIsolatedContractPostgresStore(t *testing.T) (context.Context, *pgstore.Store) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	schema := "app_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	adminStore, err := pgstore.New(ctx, pgstore.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("create postgres admin store: %v", err)
	}
	if _, err := adminStore.Pool().Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		_ = adminStore.Close()
		t.Fatalf("create fixture schema %s: %v", schema, err)
	}
	_ = adminStore.Close()

	isolatedDSN, err := dsnWithSearchPath(dsn, schema)
	if err != nil {
		t.Fatalf("build isolated postgres DSN: %v", err)
	}
	pgStore, err := pgstore.New(ctx, pgstore.Config{DSN: isolatedDSN})
	if err != nil {
		if cleanupStore, cleanupErr := pgstore.New(context.Background(), pgstore.Config{DSN: dsn}); cleanupErr == nil {
			_ = dropFixtureSchema(context.Background(), cleanupStore.Pool(), schema)
			_ = cleanupStore.Close()
		}
		t.Fatalf("create isolated postgres store: %v", err)
	}
	t.Cleanup(func() {
		if err := pgStore.Close(); err != nil {
			t.Errorf("close postgres store: %v", err)
		}
	})
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := dropFixtureSchema(cleanupCtx, pgStore.Pool(), schema); err != nil {
			t.Errorf("%v", err)
		}
	})
	if err := pgStore.EnsureSchema(ctx); err != nil {
		t.Fatalf("ensure postgres schema %s: %v", schema, err)
	}
	return ctx, pgStore
}

type failingSchemaExecer struct {
	executed []string
	failErr  error
}

func (f *failingSchemaExecer) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	f.executed = append(f.executed, sql)
	if f.failErr != nil {
		return pgconn.CommandTag{}, f.failErr
	}
	return pgconn.CommandTag{}, nil
}

func TestContractFixtures_PostgresRouterEndToEnd(t *testing.T) {
	// Verify schema cleanup helper refuses non-fixture schemas and propagates execution errors.
	fakeExec := &failingSchemaExecer{failErr: errors.New("simulated drop schema failure")}
	if err := dropFixtureSchema(context.Background(), fakeExec, "public"); err == nil {
		t.Fatal("expected dropFixtureSchema to refuse non-fixture schema \"public\"")
	}
	if len(fakeExec.executed) != 0 {
		t.Fatalf("dropFixtureSchema executed SQL for refused schema: %v", fakeExec.executed)
	}
	if err := dropFixtureSchema(context.Background(), fakeExec, "app_test_1234abcd"); err == nil {
		t.Fatal("expected dropFixtureSchema to propagate SQL error")
	}

	ctx, pgStore := openIsolatedContractPostgresStore(t)

	host := &models.User{
		ID:          "usr_host_01",
		Email:       "host@micup.live",
		DisplayName: "KJ Host",
		IsHost:      true,
	}
	if err := pgStore.CreateUser(ctx, host); err != nil {
		t.Fatalf("create host user: %v", err)
	}
	if err := pgStore.CreateSongbook(ctx, &models.Songbook{
		ID:       "sb_contract_01",
		PublicID: "pub_sb_contract_01",
		OwnerID:  host.ID,
		Name:     "Main Host Songbook",
		IsPublic: true,
	}); err != nil {
		t.Fatalf("create songbook: %v", err)
	}

	for rawKey, scopes := range map[string][]string{
		"mk_live_contract_events_read":    {handlers.DeveloperScopeEventsRead},
		"mk_live_contract_requests_read":  {handlers.DeveloperScopeRequestsRead},
		"mk_live_contract_requests_write": {handlers.DeveloperScopeRequestsWrite},
	} {
		if err := pgStore.CreateDeveloperKey(ctx, rawKey, &models.DeveloperKey{
			Name:         rawKey,
			KeyPrefix:    rawKey[:12],
			OwnerID:      host.ID,
			ScopeVersion: 1,
			Scopes:       scopes,
			CreatedAt:    time.Now().UTC(),
		}); err != nil {
			t.Fatalf("create developer key %s: %v", rawKey, err)
		}
	}

	var eventsFixture struct {
		Response struct {
			Body struct {
				Events []*models.Event `json:"events"`
			} `json:"body"`
		} `json:"response"`
	}
	loadContractFixture(t, "events_list_200.json", &eventsFixture)
	for _, ev := range eventsFixture.Response.Body.Events {
		cp := *ev
		cp.OwnerID = host.ID
		if err := pgStore.CreateEvent(ctx, &cp); err != nil {
			t.Fatalf("create event %s: %v", cp.ID, err)
		}
	}

	var hostQueueFixture struct {
		Response struct {
			Body struct {
				Requests []*models.SongRequest `json:"requests"`
			} `json:"body"`
		} `json:"response"`
	}
	loadContractFixture(t, "queue_host_requests_200.json", &hostQueueFixture)
	for _, req := range hostQueueFixture.Response.Body.Requests {
		cp := *req
		if err := pgStore.CreateRequest(ctx, &cp); err != nil {
			t.Fatalf("create request %s: %v", cp.ID, err)
		}
	}

	const secret = "contract-test-signing-secret-at-least-32-bytes"
	router, err := NewRouter(
		config.Config{
			Environment:           config.Development,
			AuthSecret:            secret,
			AllowedOrigins:        []string{"http://localhost:5173"},
			DeveloperKeyScopeMode: handlers.DeveloperKeyScopeEnforce,
		},
		Dependencies{
			Auth:          pgStore,
			DeveloperKeys: pgStore,
			Events:        pgStore,
			Songbooks:     pgStore,
			Requests:      pgStore,
		},
	)
	if err != nil {
		t.Fatalf("create postgres contract router: %v", err)
	}

	// 1. Verify 204 No Content polling against PostgreSQL.
	var pollFixture struct {
		Cases []struct {
			Name     string               `json:"name"`
			Request  contractRequestSpec  `json:"request"`
			Response contractResponseSpec `json:"response"`
		} `json:"cases"`
	}
	loadContractFixture(t, "queue_poll_204.json", &pollFixture)
	for _, tc := range pollFixture.Cases {
		rec := executeContractRequest(t, router, tc.Request)
		if rec.Code != tc.Response.Status {
			t.Fatalf("postgres %s status = %d, body = %s; want %d", tc.Name, rec.Code, rec.Body.String(), tc.Response.Status)
		}
		assertResponseHeaders(t, "postgres "+tc.Name, rec.Header(), tc.Response.Headers)
		if tc.Response.BodyBytes != nil && rec.Body.Len() != *tc.Response.BodyBytes {
			t.Fatalf("postgres %s body len = %d; want %d", tc.Name, rec.Body.Len(), *tc.Response.BodyBytes)
		}
	}

	// 2. Verify status mutations, reorder, version conflicts, idempotent replays, and idempotency conflicts against PostgreSQL transactions.
	var mutationsFixture struct {
		Mutations []struct {
			Name     string               `json:"name"`
			Request  contractRequestSpec  `json:"request"`
			Response contractResponseSpec `json:"response"`
		} `json:"mutations"`
	}
	loadContractFixture(t, "request_status_mutations.json", &mutationsFixture)
	for _, m := range mutationsFixture.Mutations {
		rec := executeContractRequest(t, router, m.Request)
		if rec.Code != m.Response.Status {
			t.Fatalf("postgres %s status = %d, body = %s; want %d", m.Name, rec.Code, rec.Body.String(), m.Response.Status)
		}
		assertResponseHeaders(t, "postgres "+m.Name, rec.Header(), m.Response.Headers)
		assertContractResponseFields(t, "postgres "+m.Name, rec.Body.Bytes(), m.Response)
	}

	var reorderFixture struct {
		Request                  contractRequestSpec  `json:"request"`
		Response                 contractResponseSpec `json:"response"`
		ExpectedActiveOrderAfter []string             `json:"expected_active_order_after"`
	}
	loadContractFixture(t, "queue_reorder_200.json", &reorderFixture)
	reorderRec := executeContractRequest(t, router, reorderFixture.Request)
	if reorderRec.Code != reorderFixture.Response.Status {
		t.Fatalf("postgres reorder status = %d, body = %s; want %d", reorderRec.Code, reorderRec.Body.String(), reorderFixture.Response.Status)
	}
	assertResponseHeaders(t, "postgres reorder", reorderRec.Header(), reorderFixture.Response.Headers)
	assertContractResponseFields(t, "postgres reorder body", reorderRec.Body.Bytes(), reorderFixture.Response)

	pubRec := executeContractRequest(t, router, contractRequestSpec{
		Method: http.MethodGet,
		Path:   "/api/v1/events/evt_contract_01/queue",
	})
	var pubQueue struct {
		Queue []models.PublicQueueItem `json:"queue"`
	}
	if err := json.Unmarshal(pubRec.Body.Bytes(), &pubQueue); err != nil {
		t.Fatalf("decode postgres public queue after reorder: %v", err)
	}
	if len(pubQueue.Queue) != len(reorderFixture.ExpectedActiveOrderAfter) {
		t.Fatalf("postgres active queue len = %d; want %d", len(pubQueue.Queue), len(reorderFixture.ExpectedActiveOrderAfter))
	}
	for i, wantID := range reorderFixture.ExpectedActiveOrderAfter {
		if pubQueue.Queue[i].ID != wantID {
			t.Fatalf("postgres active queue[%d].ID = %s; want %s", i, pubQueue.Queue[i].ID, wantID)
		}
	}

	var conflictsFixture struct {
		VersionConflicts []struct {
			Name     string               `json:"name"`
			Request  contractRequestSpec  `json:"request"`
			Response contractResponseSpec `json:"response"`
		} `json:"version_conflicts"`
		IdempotentReplays []struct {
			Name                     string               `json:"name"`
			Request                  contractRequestSpec  `json:"request"`
			Response                 contractResponseSpec `json:"response"`
			EventQueueVersionRemains int64                `json:"event_queue_version_remains"`
		} `json:"idempotent_replays"`
		IdempotencyConflicts []struct {
			Name     string               `json:"name"`
			Request  contractRequestSpec  `json:"request"`
			Response contractResponseSpec `json:"response"`
		} `json:"idempotency_conflicts"`
	}
	loadContractFixture(t, "conflicts_and_idempotency.json", &conflictsFixture)

	for _, vc := range conflictsFixture.VersionConflicts {
		rec := executeContractRequest(t, router, vc.Request)
		if rec.Code != vc.Response.Status {
			t.Fatalf("postgres %s status = %d, body = %s; want %d", vc.Name, rec.Code, rec.Body.String(), vc.Response.Status)
		}
		assertResponseHeaders(t, "postgres "+vc.Name, rec.Header(), vc.Response.Headers)
		assertContractResponseFields(t, "postgres "+vc.Name, rec.Body.Bytes(), vc.Response)
	}

	for _, replay := range conflictsFixture.IdempotentReplays {
		rec := executeContractRequest(t, router, replay.Request)
		if rec.Code != replay.Response.Status {
			t.Fatalf("postgres %s status = %d, body = %s; want %d", replay.Name, rec.Code, rec.Body.String(), replay.Response.Status)
		}
		assertResponseHeaders(t, "postgres "+replay.Name, rec.Header(), replay.Response.Headers)
		assertContractResponseFields(t, "postgres "+replay.Name, rec.Body.Bytes(), replay.Response)
		ev, err := pgStore.GetEvent(ctx, "evt_contract_01")
		if err != nil {
			t.Fatalf("get event after postgres replay: %v", err)
		}
		if ev.QueueVersion != replay.EventQueueVersionRemains {
			t.Fatalf("postgres QueueVersion after replay = %d; want %d", ev.QueueVersion, replay.EventQueueVersionRemains)
		}
	}

	for _, ic := range conflictsFixture.IdempotencyConflicts {
		rec := executeContractRequest(t, router, ic.Request)
		if rec.Code != ic.Response.Status {
			t.Fatalf("postgres %s status = %d, body = %s; want %d", ic.Name, rec.Code, rec.Body.String(), ic.Response.Status)
		}
		assertResponseHeaders(t, "postgres "+ic.Name, rec.Header(), ic.Response.Headers)
		assertContractResponseFields(t, "postgres "+ic.Name, rec.Body.Bytes(), ic.Response)
	}
}

func TestContract_EventPatch_PostgresRouterEndToEnd(t *testing.T) {
	ctx, pgStore := openIsolatedContractPostgresStore(t)

	host := &models.User{
		ID:          "usr_host_sb_01",
		Email:       "host_sb@micup.live",
		DisplayName: "KJ Host SB",
		IsHost:      true,
	}
	if err := pgStore.CreateUser(ctx, host); err != nil {
		t.Fatalf("create host user: %v", err)
	}

	otherHost := &models.User{
		ID:          "usr_host_other_02",
		Email:       "other_host_sb@micup.live",
		DisplayName: "Other Host SB",
		IsHost:      true,
	}
	if err := pgStore.CreateUser(ctx, otherHost); err != nil {
		t.Fatalf("create other host user: %v", err)
	}

	if err := pgStore.CreateSongbook(ctx, &models.Songbook{
		ID:       "sb_patch_01",
		PublicID: "pub_sb_patch_01",
		OwnerID:  host.ID,
		Name:     "Songbook Patch Test",
		IsPublic: true,
	}); err != nil {
		t.Fatalf("create songbook: %v", err)
	}

	if err := pgStore.CreateDeveloperKey(ctx, "mk_live_sb_requests_read", &models.DeveloperKey{
		Name:         "mk_live_sb_requests_read",
		KeyPrefix:    "mk_live_sb_r",
		OwnerID:      host.ID,
		ScopeVersion: 1,
		Scopes:       []string{handlers.DeveloperScopeRequestsRead},
		CreatedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create read-only developer key: %v", err)
	}

	if err := pgStore.CreateDeveloperKey(ctx, "mk_live_sb_requests_write", &models.DeveloperKey{
		Name:         "mk_live_sb_requests_write",
		KeyPrefix:    "mk_live_sb_w",
		OwnerID:      host.ID,
		ScopeVersion: 1,
		Scopes:       []string{handlers.DeveloperScopeRequestsWrite},
		CreatedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create write developer key: %v", err)
	}

	if err := pgStore.CreateDeveloperKey(ctx, "mk_live_other_host_write", &models.DeveloperKey{
		Name:         "mk_live_other_host_write",
		KeyPrefix:    "mk_live_othe",
		OwnerID:      otherHost.ID,
		ScopeVersion: 1,
		Scopes:       []string{handlers.DeveloperScopeRequestsWrite},
		CreatedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create other host developer key: %v", err)
	}

	now := time.Now().UTC()
	event := &models.Event{
		ID:           "evt_patch_01",
		OwnerID:      host.ID,
		SongbookID:   "sb_patch_01",
		Name:         "Live Show Patch Test",
		Status:       models.EventStatusActive,
		JoinCode:     "PATCH1",
		QueueVersion: 1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := pgStore.CreateEvent(ctx, event); err != nil {
		t.Fatalf("create event: %v", err)
	}

	req := &models.SongRequest{
		ID:         "req_patch_01",
		SongbookID: "sb_patch_01",
		EventID:    "evt_patch_01",
		SongTitle:  "Dreams",
		SongArtist: "Fleetwood Mac",
		SingerName: "Sarah K.",
		Status:     models.StatusPending,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := pgStore.CreateRequest(ctx, req); err != nil {
		t.Fatalf("create request: %v", err)
	}

	const secret = "contract-test-signing-secret-at-least-32-bytes"
	router, err := NewRouter(
		config.Config{
			Environment:           config.Development,
			AuthSecret:            secret,
			AllowedOrigins:        []string{"http://localhost:5173"},
			DeveloperKeyScopeMode: handlers.DeveloperKeyScopeEnforce,
		},
		Dependencies{
			Auth:          pgStore,
			DeveloperKeys: pgStore,
			Events:        pgStore,
			Songbooks:     pgStore,
			Requests:      pgStore,
		},
	)
	if err != nil {
		t.Fatalf("create postgres router: %v", err)
	}

	// 1. Scope Denial: Developer key with only requests:read cannot PATCH /events/{id}/requests/{rid}
	{
		rec := executeContractRequest(t, router, contractRequestSpec{
			Method: http.MethodPatch,
			Path:   "/api/v1/events/evt_patch_01/requests/req_patch_01",
			Headers: map[string]string{
				"Authorization": "Bearer mk_live_sb_requests_read",
				"Content-Type":  "application/json",
			},
			Body: json.RawMessage(`{"status":"accepted"}`),
		})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("scope denial status = %d; want %d; body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	}

	// 2. Ownership Denial: Other host with requests:write cannot mutate sb_patch_01 requests (404 anti-enumeration)
	{
		rec := executeContractRequest(t, router, contractRequestSpec{
			Method: http.MethodPatch,
			Path:   "/api/v1/events/evt_patch_01/requests/req_patch_01",
			Headers: map[string]string{
				"Authorization": "Bearer mk_live_other_host_write",
				"Content-Type":  "application/json",
			},
			Body: json.RawMessage(`{"status":"accepted"}`),
		})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("ownership denial status = %d; want %d; body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
		}
	}

	// 3. Atomic queue-version advancement & legacy response shape:
	// Owner PATCH with expected_queue_version: 1 advances event version to 2 and returns legacy SongRequest envelope.
	{
		rec := executeContractRequest(t, router, contractRequestSpec{
			Method: http.MethodPatch,
			Path:   "/api/v1/events/evt_patch_01/requests/req_patch_01",
			Headers: map[string]string{
				"Authorization": "Bearer mk_live_sb_requests_write",
				"Content-Type":  "application/json",
			},
			Body: json.RawMessage(`{"status":"accepted","expected_queue_version":1}`),
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("patch status = %d; want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
		}
		if gotVer := rec.Header().Get("X-Queue-Version"); gotVer != "2" {
			t.Fatalf("patch X-Queue-Version = %q; want \"2\"", gotVer)
		}

		// Verify legacy response shape (unmarshals as models.SongRequest)
		var respReq models.SongRequest
		if err := json.Unmarshal(rec.Body.Bytes(), &respReq); err != nil {
			t.Fatalf("decode legacy SongRequest response: %v; body = %s", err, rec.Body.String())
		}
		if respReq.ID != "req_patch_01" || respReq.Status != models.StatusAccepted || respReq.SongbookID != "sb_patch_01" {
			t.Fatalf("unexpected legacy response fields: %+v", respReq)
		}

		// Verify atomic mutation in PostgreSQL
		var qv int64
		if err := pgStore.Pool().QueryRow(ctx, `SELECT queue_version FROM events WHERE id = $1`, "evt_patch_01").Scan(&qv); err != nil {
			t.Fatalf("query event queue_version from postgres: %v", err)
		}
		if qv != 2 {
			t.Fatalf("postgres event queue_version = %d; want 2", qv)
		}
		storedReq, err := pgStore.GetRequest(ctx, "req_patch_01")
		if err != nil {
			t.Fatalf("get request from postgres: %v", err)
		}
		if storedReq.Status != models.StatusAccepted {
			t.Fatalf("postgres request status = %q; want %q", storedReq.Status, models.StatusAccepted)
		}
	}

	// 4. Stale-version conflict:
	// Owner PATCH with expected_queue_version: 1 (current is 2) returns 409 Conflict with current version in header.
	{
		rec := executeContractRequest(t, router, contractRequestSpec{
			Method: http.MethodPatch,
			Path:   "/api/v1/events/evt_patch_01/requests/req_patch_01",
			Headers: map[string]string{
				"Authorization": "Bearer mk_live_sb_requests_write",
				"Content-Type":  "application/json",
			},
			Body: json.RawMessage(`{"status":"completed","expected_queue_version":1}`),
		})
		if rec.Code != http.StatusConflict {
			t.Fatalf("stale conflict status = %d; want %d; body = %s", rec.Code, http.StatusConflict, rec.Body.String())
		}
		if gotVer := rec.Header().Get("X-Queue-Version"); gotVer != "2" {
			t.Fatalf("stale conflict X-Queue-Version = %q; want \"2\"", gotVer)
		}
		var errResp map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
			t.Fatalf("decode conflict response: %v", err)
		}
		if fmt.Sprint(errResp["current_queue_version"]) != "2" {
			t.Fatalf("conflict current_queue_version = %v; want 2", errResp["current_queue_version"])
		}

		// Verify PostgreSQL state was untouched
		ev, err := pgStore.GetEvent(ctx, "evt_patch_01")
		if err != nil {
			t.Fatalf("get event: %v", err)
		}
		if ev.QueueVersion != 2 {
			t.Fatalf("postgres event QueueVersion after conflict = %d; want 2", ev.QueueVersion)
		}
		storedReq, err := pgStore.GetRequest(ctx, "req_patch_01")
		if err != nil {
			t.Fatalf("get request: %v", err)
		}
		if storedReq.Status != models.StatusAccepted {
			t.Fatalf("postgres request status after conflict = %q; want %q", storedReq.Status, models.StatusAccepted)
		}
	}

	// 5. Idempotent replay without a second mutation:
	// First call commits mutation, advances QueueVersion to 3.
	idempotencyKey := uuid.NewString()
	{
		rec := executeContractRequest(t, router, contractRequestSpec{
			Method: http.MethodPatch,
			Path:   "/api/v1/events/evt_patch_01/requests/req_patch_01",
			Headers: map[string]string{
				"Authorization":   "Bearer mk_live_sb_requests_write",
				"Content-Type":    "application/json",
				"Idempotency-Key": idempotencyKey,
			},
			Body: json.RawMessage(`{"status":"completed","expected_queue_version":2}`),
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("idempotent initial status = %d; want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
		}
		if gotVer := rec.Header().Get("X-Queue-Version"); gotVer != "3" {
			t.Fatalf("idempotent initial X-Queue-Version = %q; want \"3\"", gotVer)
		}

		var respReq models.SongRequest
		if err := json.Unmarshal(rec.Body.Bytes(), &respReq); err != nil {
			t.Fatalf("decode initial idempotent response: %v", err)
		}
		if respReq.Status != models.StatusCompleted {
			t.Fatalf("initial idempotent response status = %q; want %q", respReq.Status, models.StatusCompleted)
		}

		ev, err := pgStore.GetEvent(ctx, "evt_patch_01")
		if err != nil {
			t.Fatalf("get event: %v", err)
		}
		if ev.QueueVersion != 3 {
			t.Fatalf("postgres event QueueVersion after initial idempotency = %d; want 3", ev.QueueVersion)
		}
	}

	// Replay exact same request with same Idempotency-Key:
	// Returns cached 200 response with legacy shape, does NOT re-mutate or increment QueueVersion.
	{
		rec := executeContractRequest(t, router, contractRequestSpec{
			Method: http.MethodPatch,
			Path:   "/api/v1/events/evt_patch_01/requests/req_patch_01",
			Headers: map[string]string{
				"Authorization":   "Bearer mk_live_sb_requests_write",
				"Content-Type":    "application/json",
				"Idempotency-Key": idempotencyKey,
			},
			Body: json.RawMessage(`{"status":"completed","expected_queue_version":2}`),
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("idempotent replay status = %d; want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
		}
		if gotVer := rec.Header().Get("X-Queue-Version"); gotVer != "3" {
			t.Fatalf("idempotent replay X-Queue-Version = %q; want \"3\"", gotVer)
		}

		var respReq models.SongRequest
		if err := json.Unmarshal(rec.Body.Bytes(), &respReq); err != nil {
			t.Fatalf("decode replay response: %v", err)
		}
		if respReq.Status != models.StatusCompleted {
			t.Fatalf("replay response status = %q; want %q", respReq.Status, models.StatusCompleted)
		}

		// Critical invariant: QueueVersion in PostgreSQL remains 3 (no second mutation)
		ev, err := pgStore.GetEvent(ctx, "evt_patch_01")
		if err != nil {
			t.Fatalf("get event: %v", err)
		}
		if ev.QueueVersion != 3 {
			t.Fatalf("postgres event QueueVersion after replay = %d; want 3 (replayed without second mutation)", ev.QueueVersion)
		}
	}
}
