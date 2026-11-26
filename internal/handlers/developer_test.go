package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"micup.live/server/internal/models"
)

type developerKeyTestStore struct {
	mu         sync.Mutex
	keysByHash map[string]*models.DeveloperKey
	ownerIndex map[string][]*models.DeveloperKey
	lastUsed   map[string]time.Time
}

func newDeveloperKeyTestStore() *developerKeyTestStore {
	return &developerKeyTestStore{
		keysByHash: make(map[string]*models.DeveloperKey),
		ownerIndex: make(map[string][]*models.DeveloperKey),
		lastUsed:   make(map[string]time.Time),
	}
}

func hashRawKey(rawKey string) string {
	digest := sha256.Sum256([]byte(rawKey))
	return hex.EncodeToString(digest[:])
}

func (s *developerKeyTestStore) CreateDeveloperKey(ctx context.Context, rawKey string, key *models.DeveloperKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key.CreatedAt.IsZero() {
		key.CreatedAt = time.Now().UTC()
	}
	stored := *key
	hash := hashRawKey(rawKey)
	stored.ID = hash
	s.keysByHash[hash] = &stored
	s.ownerIndex[key.OwnerID] = append(s.ownerIndex[key.OwnerID], &stored)
	return nil
}

func (s *developerKeyTestStore) GetDeveloperKey(_ context.Context, rawKey string) (*models.DeveloperKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.keysByHash[hashRawKey(rawKey)]
	if !ok {
		return nil, os.ErrNotExist
	}
	return key, nil
}

func (s *developerKeyTestStore) ListDeveloperKeys(_ context.Context, ownerID string) ([]*models.DeveloperKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*models.DeveloperKey(nil), s.ownerIndex[ownerID]...), nil
}

func (s *developerKeyTestStore) RevokeDeveloperKey(_ context.Context, id, ownerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.keysByHash[id]
	if !ok || key.OwnerID != ownerID {
		return os.ErrNotExist
	}
	delete(s.keysByHash, id)
	for i, k := range s.ownerIndex[ownerID] {
		if k.ID == id {
			s.ownerIndex[ownerID] = append(s.ownerIndex[ownerID][:i], s.ownerIndex[ownerID][i+1:]...)
			break
		}
	}
	return nil
}

func (s *developerKeyTestStore) TouchDeveloperKeyLastUsed(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUsed[id] = time.Now().UTC()
	return nil
}

func newDeveloperTestServer(t *testing.T) (*http.ServeMux, *developerKeyTestStore) {
	t.Helper()
	store := newDeveloperKeyTestStore()
	authStore := newAuthTestStore()
	handler := NewDeveloperHandler(store, authStore)
	handler.ScopeMode = DeveloperKeyScopeEnforce
	auth, err := NewAuthHandler(authStore, "test-signing-secret-with-at-least-32-bytes")
	if err != nil {
		t.Fatalf("create auth handler: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/register", auth.Register)

	hostAuth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			token := strings.TrimSpace(r.Header.Get("Authorization"))
			token = strings.TrimPrefix(token, "Bearer ")
			if strings.HasPrefix(token, "mk_live_") {
				handler.RequireDeveloperKey(next).ServeHTTP(w, r)
				return
			}
			auth.RequireAuth(auth.RequireHost(next)).ServeHTTP(w, r)
		}
	}
	hostOnly := hostAuth

	mux.HandleFunc("POST /api/v1/api-keys", hostOnly(handler.CreateKey))
	mux.HandleFunc("GET /api/v1/api-keys", hostOnly(handler.ListKeys))
	mux.HandleFunc("DELETE /api/v1/api-keys/{id}", hostOnly(handler.RevokeKey))
	mux.HandleFunc("GET /test/host-route", hostOnly(func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		if user == nil {
			http.Error(w, "missing user", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(user.Email))
	}))
	mux.HandleFunc("GET /test/requests-write-route", hostOnly(handler.RequireScope(DeveloperScopeRequestsWrite, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	return mux, store
}

func developerRequestFor(t *testing.T, mux *http.ServeMux, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	return response
}

func TestCreateDeveloperKeyReturnsRawOnce(t *testing.T) {
	mux, store := newDeveloperTestServer(t)
	tokens := authRegisterHost(t, mux, "dev@example.com")
	hostToken := decodeTokens(t, tokens).AccessToken

	resp := developerRequestFor(t, mux, http.MethodPost, "/api/v1/api-keys",
		`{"name":"Hazz Desktop","scopes":["events:read","requests:write"]}`, hostToken)
	if resp.Code != http.StatusCreated {
		t.Fatalf("create status = %d; want %d; body = %s", resp.Code, http.StatusCreated, resp.Body.String())
	}
	var created models.KeyResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode created key: %v", err)
	}
	if created.RawKey == "" {
		t.Fatalf("expected a raw key in the response")
	}
	if len(store.keysByHash) != 1 {
		t.Fatalf("expected one stored key, got %d", len(store.keysByHash))
	}
	if stored := store.keysByHash[created.ID]; stored.ScopeVersion != 1 {
		t.Fatalf("scope version = %d; want 1", stored.ScopeVersion)
	}
}

func TestReadOnlyDeveloperKeyCannotMutateScopedRoute(t *testing.T) {
	mux, _ := newDeveloperTestServer(t)
	hostToken := decodeTokens(t, authRegisterHost(t, mux, "readonly@example.com")).AccessToken
	resp := developerRequestFor(t, mux, http.MethodPost, "/api/v1/api-keys",
		`{"name":"Read-only","scopes":["events:read"]}`, hostToken)
	if resp.Code != http.StatusCreated {
		t.Fatalf("create status = %d; want %d", resp.Code, http.StatusCreated)
	}
	var key models.KeyResponse
	if err := json.NewDecoder(resp.Body).Decode(&key); err != nil {
		t.Fatalf("decode key: %v", err)
	}
	denied := developerRequestFor(t, mux, http.MethodGet, "/test/requests-write-route", "", key.RawKey)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("read-only mutation status = %d; want %d", denied.Code, http.StatusForbidden)
	}
	var body map[string]string
	if err := json.NewDecoder(denied.Body).Decode(&body); err != nil {
		t.Fatalf("decode denial: %v", err)
	}
	if body["required_scope"] != DeveloperScopeRequestsWrite {
		t.Fatalf("required scope = %q; want %q", body["required_scope"], DeveloperScopeRequestsWrite)
	}
}

func TestListAndRevokeDeveloperKeys(t *testing.T) {
	mux, _ := newDeveloperTestServer(t)
	hostToken := decodeTokens(t, authRegisterHost(t, mux, "keylist@example.com")).AccessToken

	resp := developerRequestFor(t, mux, http.MethodPost, "/api/v1/api-keys",
		`{"name":"First"}`, hostToken)
	var created models.KeyResponse
	_ = json.NewDecoder(resp.Body).Decode(&created)

	resp = developerRequestFor(t, mux, http.MethodGet, "/api/v1/api-keys", "", hostToken)
	if resp.Code != http.StatusOK {
		t.Fatalf("list status = %d; want %d", resp.Code, http.StatusOK)
	}
	var keys []*models.DeveloperKey
	if err := json.NewDecoder(resp.Body).Decode(&keys); err != nil {
		t.Fatalf("decode key list: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}

	resp = developerRequestFor(t, mux, http.MethodDelete, "/api/v1/api-keys/"+keys[0].ID, "", hostToken)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d; want %d", resp.Code, http.StatusNoContent)
	}

	resp = developerRequestFor(t, mux, http.MethodGet, "/api/v1/api-keys", "", hostToken)
	_ = json.NewDecoder(resp.Body).Decode(&keys)
	if len(keys) != 0 {
		t.Fatalf("expected 0 keys after revoke, got %d", len(keys))
	}
}

func TestDeveloperKeyRequiresHost(t *testing.T) {
	mux, _ := newDeveloperTestServer(t)
	singerToken := decodeTokens(t, authRegister(t, mux, "singer@example.com", false)).AccessToken
	resp := developerRequestFor(t, mux, http.MethodGet, "/api/v1/api-keys", "", singerToken)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("sinker list status = %d; want %d", resp.Code, http.StatusForbidden)
	}
}

func TestRequireDeveloperKeyMiddleware(t *testing.T) {
	mux, store := newDeveloperTestServer(t)
	hostTokens := decodeTokens(t, authRegisterHost(t, mux, "developer-pat@example.com")).AccessToken

	// Create a new developer key
	resp := developerRequestFor(t, mux, http.MethodPost, "/api/v1/api-keys",
		`{"name":"OBS Karaoke Dock"}`, hostTokens)
	if resp.Code != http.StatusCreated {
		t.Fatalf("create status = %d; want %d", resp.Code, http.StatusCreated)
	}
	var keyResp models.KeyResponse
	if err := json.NewDecoder(resp.Body).Decode(&keyResp); err != nil {
		t.Fatalf("decode key response: %v", err)
	}

	// 1. Authenticate with raw Bearer mk_live_... key
	routeResp := developerRequestFor(t, mux, http.MethodGet, "/test/host-route", "", keyResp.RawKey)
	if routeResp.Code != http.StatusOK {
		t.Fatalf("host route with key status = %d; want %d; body = %s", routeResp.Code, http.StatusOK, routeResp.Body.String())
	}
	if routeResp.Body.String() != "developer-pat@example.com" {
		t.Fatalf("got body %q; want %q", routeResp.Body.String(), "developer-pat@example.com")
	}

	// 2. Verify last used was updated
	if store.lastUsed[keyResp.ID].IsZero() {
		t.Fatalf("expected last_used_at to be touched")
	}

	// Expired keys are rejected before route handling, including when a route
	// also requires a developer scope.
	expiredRaw, err := newRawKey()
	if err != nil {
		t.Fatalf("generate expired key: %v", err)
	}
	if err := store.CreateDeveloperKey(context.Background(), expiredRaw, &models.DeveloperKey{
		OwnerID:   keyResp.OwnerID,
		Name:      "expired",
		ExpiresAt: time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("store expired key: %v", err)
	}
	expiredResp := developerRequestFor(t, mux, http.MethodGet, "/test/host-route", "", expiredRaw)
	if expiredResp.Code != http.StatusForbidden {
		t.Fatalf("expired key status = %d; want %d", expiredResp.Code, http.StatusForbidden)
	}

	// 3. Revoke key and verify access is rejected with 403
	revokeResp := developerRequestFor(t, mux, http.MethodDelete, "/api/v1/api-keys/"+keyResp.ID, "", hostTokens)
	if revokeResp.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d; want %d", revokeResp.Code, http.StatusNoContent)
	}

	rejectedResp := developerRequestFor(t, mux, http.MethodGet, "/test/host-route", "", keyResp.RawKey)
	if rejectedResp.Code != http.StatusUnauthorized {
		t.Fatalf("revoked key status = %d; want %d", rejectedResp.Code, http.StatusUnauthorized)
	}

	// 4. Invalid key prefix / bad key gives 401
	badResp := developerRequestFor(t, mux, http.MethodGet, "/test/host-route", "", "mk_live_invalidkeyhere0000000000000000000")
	if badResp.Code != http.StatusUnauthorized {
		t.Fatalf("invalid key status = %d; want %d", badResp.Code, http.StatusUnauthorized)
	}
}

func TestDeveloperKeyScopeModesAndLegacyAccess(t *testing.T) {
	handler := NewDeveloperHandler(nil, nil)
	next := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}

	key := &models.DeveloperKey{OwnerID: "host-1", Scopes: []string{DeveloperScopeEventsRead}, ScopeVersion: 1}
	request := httptest.NewRequest(http.MethodGet, "/events", nil)
	request = request.WithContext(context.WithValue(request.Context(), DeveloperKeyContextKey{}, key))
	handler.ScopeMode = DeveloperKeyScopeEnforce
	denied := httptest.NewRecorder()
	handler.RequireScope(DeveloperScopeRequestsWrite, next).ServeHTTP(denied, request)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("missing scope status = %d; want %d", denied.Code, http.StatusForbidden)
	}
	var body map[string]string
	if err := json.NewDecoder(denied.Body).Decode(&body); err != nil {
		t.Fatalf("decode scope denial: %v", err)
	}
	if body["error"] != "insufficient_scope" || body["required_scope"] != DeveloperScopeRequestsWrite {
		t.Fatalf("unexpected scope denial: %+v", body)
	}

	handler.ScopeMode = DeveloperKeyScopeReport
	reported := httptest.NewRecorder()
	handler.RequireScope(DeveloperScopeRequestsWrite, next).ServeHTTP(reported, request)
	if reported.Code != http.StatusNoContent {
		t.Fatalf("report-only status = %d; want %d", reported.Code, http.StatusNoContent)
	}

	legacy := httptest.NewRequest(http.MethodGet, "/events", nil)
	legacy = legacy.WithContext(context.WithValue(legacy.Context(), DeveloperKeyContextKey{}, &models.DeveloperKey{OwnerID: "legacy-host"}))
	handler.ScopeMode = DeveloperKeyScopeEnforce
	grandfathered := httptest.NewRecorder()
	handler.RequireScope(DeveloperScopeCatalogsWrite, next).ServeHTTP(grandfathered, legacy)
	if grandfathered.Code != http.StatusNoContent {
		t.Fatalf("legacy empty-scope status = %d; want %d", grandfathered.Code, http.StatusNoContent)
	}
}

func TestNormalizeScopesDefaultsAllRouteScopes(t *testing.T) {
	want := map[string]bool{
		DeveloperScopeEventsRead:    true,
		DeveloperScopeEventsWrite:   true,
		DeveloperScopeRequestsRead:  true,
		DeveloperScopeRequestsWrite: true,
		DeveloperScopeCatalogsWrite: true,
	}
	got := normalizeScopes(nil)
	if len(got) != len(want) {
		t.Fatalf("default scopes = %v; want %d scopes", got, len(want))
	}
	for _, scope := range got {
		if !want[scope] {
			t.Errorf("unexpected default scope %q", scope)
		}
	}
}

func TestDeveloperKey_EventAndQueueLifecycle(t *testing.T) {
	keyStore := newDeveloperKeyTestStore()
	authStore := newAuthTestStore()
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	qStore.events = evStore

	devHandler := NewDeveloperHandler(keyStore, authStore)
	evHandler := &EventHandler{Events: evStore, Requests: qStore}
	queueHandler := &QueueHandler{Events: evStore, Requests: qStore}
	auth, err := NewAuthHandler(authStore, "test-signing-secret-with-at-least-32-bytes")
	if err != nil {
		t.Fatalf("auth handler: %v", err)
	}

	hostAuth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			token := strings.TrimSpace(r.Header.Get("Authorization"))
			token = strings.TrimPrefix(token, "Bearer ")
			if strings.HasPrefix(token, "mk_live_") {
				devHandler.RequireDeveloperKey(next).ServeHTTP(w, r)
				return
			}
			auth.RequireAuth(auth.RequireHost(next)).ServeHTTP(w, r)
		}
	}
	ownedEventOnly := func(next http.HandlerFunc) http.HandlerFunc {
		return hostAuth(evHandler.RequireOwnership(next))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/register", auth.Register)
	mux.HandleFunc("POST /api/v1/api-keys", hostAuth(devHandler.CreateKey))
	mux.HandleFunc("GET /api/v1/events", hostAuth(evHandler.ListEvents))
	mux.HandleFunc("GET /api/v1/events/{id}", ownedEventOnly(evHandler.GetEvent))
	mux.HandleFunc("GET /api/v1/events/{id}/requests", ownedEventOnly(queueHandler.ListEventRequests))
	mux.HandleFunc("PATCH /api/v1/events/{id}/requests/{requestID}", ownedEventOnly(queueHandler.UpdateEventRequest))
	mux.HandleFunc("POST /api/v1/events/{id}/pause", ownedEventOnly(evHandler.PauseEvent))
	mux.HandleFunc("POST /api/v1/events/{id}/resume", ownedEventOnly(evHandler.ResumeEvent))

	// Register host and issue a developer key
	hostTokens := decodeTokens(t, authRegisterHost(t, mux, "live-host@example.com")).AccessToken
	createKeyResp := developerRequestFor(t, mux, http.MethodPost, "/api/v1/api-keys",
		`{"name":"Hazz Live Sync"}`, hostTokens)
	var keyResp models.KeyResponse
	_ = json.NewDecoder(createKeyResp.Body).Decode(&keyResp)
	apiKey := keyResp.RawKey

	// Setup event owned by this host
	event := &models.Event{
		ID:           "ev-hazz-1",
		OwnerID:      keyResp.OwnerID,
		Name:         "Friday Night Live",
		VenueName:    "The Karaoke Club",
		Status:       models.EventStatusActive,
		QueueVersion: 1,
	}
	_ = evStore.CreateEvent(context.Background(), event)

	// Add a pending singer request
	req := &models.SongRequest{
		ID:         "req-hazz-1",
		EventID:    "ev-hazz-1",
		SongTitle:  "Dreams",
		SongArtist: "Fleetwood Mac",
		SingerName: "Sarah K.",
		Status:     models.StatusPending,
	}
	_ = qStore.CreateRequest(context.Background(), req)

	// 1. List events using API Key
	eventsResp := developerRequestFor(t, mux, http.MethodGet, "/api/v1/events", "", apiKey)
	if eventsResp.Code != http.StatusOK {
		t.Fatalf("list events status = %d; want %d", eventsResp.Code, http.StatusOK)
	}
	var evList struct{ Events []*models.Event }
	_ = json.NewDecoder(eventsResp.Body).Decode(&evList)
	if len(evList.Events) != 1 || evList.Events[0].ID != "ev-hazz-1" {
		t.Fatalf("unexpected events list: %+v", evList.Events)
	}

	// 2. Poll requests using API Key without ?since
	listReqResp := developerRequestFor(t, mux, http.MethodGet, "/api/v1/events/ev-hazz-1/requests", "", apiKey)
	if listReqResp.Code != http.StatusOK {
		t.Fatalf("poll requests status = %d; want %d", listReqResp.Code, http.StatusOK)
	}
	var reqList struct {
		QueueVersion int64                 `json:"queue_version"`
		Requests     []*models.SongRequest `json:"requests"`
	}
	_ = json.NewDecoder(listReqResp.Body).Decode(&reqList)
	if reqList.QueueVersion != 1 || len(reqList.Requests) != 1 {
		t.Fatalf("unexpected request poll result: %+v", reqList)
	}

	// 3. Conditional poll with ?since=1 -> expect 204 No Content
	unchangedResp := developerRequestFor(t, mux, http.MethodGet, "/api/v1/events/ev-hazz-1/requests?since=1", "", apiKey)
	if unchangedResp.Code != http.StatusNoContent {
		t.Fatalf("conditional poll status = %d; want %d", unchangedResp.Code, http.StatusNoContent)
	}

	// 4. Update request status to "playing" using API Key -> maps to up_next and bumps queue version
	patchResp := developerRequestFor(t, mux, http.MethodPatch, "/api/v1/events/ev-hazz-1/requests/req-hazz-1",
		`{"status":"playing"}`, apiKey)
	if patchResp.Code != http.StatusOK {
		t.Fatalf("patch request status = %d; want %d; body = %s", patchResp.Code, http.StatusOK, patchResp.Body.String())
	}
	var patched models.SongRequest
	_ = json.NewDecoder(patchResp.Body).Decode(&patched)
	if patched.Status != models.StatusUpNext {
		t.Fatalf("status = %q; want %q", patched.Status, models.StatusUpNext)
	}

	// 5. Poll with ?since=1 now returns 200 OK because version was bumped
	changedResp := developerRequestFor(t, mux, http.MethodGet, "/api/v1/events/ev-hazz-1/requests?since=1", "", apiKey)
	if changedResp.Code != http.StatusOK {
		t.Fatalf("poll after bump status = %d; want %d", changedResp.Code, http.StatusOK)
	}

	// 6. Pause and Resume endpoints using API Key
	pauseResp := developerRequestFor(t, mux, http.MethodPost, "/api/v1/events/ev-hazz-1/pause", "", apiKey)
	if pauseResp.Code != http.StatusOK {
		t.Fatalf("pause status = %d; want %d", pauseResp.Code, http.StatusOK)
	}
	var pausedEvent models.Event
	_ = json.NewDecoder(pauseResp.Body).Decode(&pausedEvent)
	if pausedEvent.Status != models.EventStatusPaused {
		t.Fatalf("event status = %q; want %q", pausedEvent.Status, models.EventStatusPaused)
	}

	resumeResp := developerRequestFor(t, mux, http.MethodPost, "/api/v1/events/ev-hazz-1/resume", "", apiKey)
	if resumeResp.Code != http.StatusOK {
		t.Fatalf("resume status = %d; want %d", resumeResp.Code, http.StatusOK)
	}
	var resumedEvent models.Event
	_ = json.NewDecoder(resumeResp.Body).Decode(&resumedEvent)
	if resumedEvent.Status != models.EventStatusActive {
		t.Fatalf("event status = %q; want %q", resumedEvent.Status, models.EventStatusActive)
	}
}

func authRegister(t *testing.T, mux *http.ServeMux, email string, isHost bool) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{"email":%q,"password":"secure-pass","is_host":%t}`, email, isHost)
	return authRequestFor(t, mux, http.MethodPost, "/auth/register", body, "")
}

func authRegisterHost(t *testing.T, mux *http.ServeMux, email string) *httptest.ResponseRecorder {
	return authRegister(t, mux, email, true)
}

var _ = context.Background
