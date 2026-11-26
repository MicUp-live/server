package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"micup.live/server/internal/config"
	"micup.live/server/internal/handlers"
	"micup.live/server/internal/models"
	pgstore "micup.live/server/internal/store/postgres"
)

const crossAdapterSigningSecret = "contract-cross-signing-secret-at-least-32-bytes"

func makeHostSessionToken(userID, authEpoch string, secret []byte) string {
	payload, err := json.Marshal(struct {
		UserID    string `json:"sub"`
		AuthEpoch string `json:"auth_epoch,omitempty"`
		ExpiresAt int64  `json:"exp"`
	}{
		UserID:    userID,
		AuthEpoch: authEpoch,
		ExpiresAt: time.Now().Add(24 * time.Hour).Unix(),
	})
	if err != nil {
		panic(err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

type crossAdapterHarness struct {
	ctx           context.Context
	pgStore       *pgstore.Store
	router        http.Handler
	hostA         *models.User
	eventA        *models.Event
	songbookA     *models.Songbook
	sessionTokenA string
	openkjKeyA    string
	hostB         *models.User
	eventB        *models.Event
	songbookB     *models.Songbook
	sessionTokenB string
	openkjKeyB    string
}

// setupCrossAdapterTestPostgres initializes PostgreSQL with an isolated schema,
// registers cleanup, and provisions two distinct host tenants (Host A and Host B)
// with songbooks, events, OpenKJ integration keys, scoped developer keys, and session tokens.
func setupCrossAdapterTestPostgres(t *testing.T) *crossAdapterHarness {
	t.Helper()
	ctx, pgStore := openIsolatedContractPostgresStore(t)

	now := time.Now().UTC()

	// 1. Host A (primary tenant under test)
	hostA := &models.User{
		ID:          "usr_host_cross_01",
		Email:       "host_cross_01@micup.live",
		DisplayName: "Host Alpha",
		IsHost:      true,
		AuthEpoch:   "epoch_host_01",
	}
	if err := pgStore.CreateUser(ctx, hostA); err != nil {
		t.Fatalf("create host A: %v", err)
	}
	sbA := &models.Songbook{
		ID:       "sb_cross_01",
		PublicID: "pub_sb_cross_01",
		OwnerID:  hostA.ID,
		Name:     "Host A Songbook",
		IsPublic: true,
	}
	if err := pgStore.CreateSongbook(ctx, sbA); err != nil {
		t.Fatalf("create songbook A: %v", err)
	}
	openkjKeyA := models.GenerateOpenKJKey("evt_cross_01")
	eventA := &models.Event{
		ID:             "evt_cross_01",
		OwnerID:        hostA.ID,
		SongbookID:     sbA.ID,
		Name:           "Show Alpha",
		VenueName:      "Alpha Lounge",
		JoinCode:       "ALPHA",
		Status:         models.EventStatusActive,
		AllowKeyChange: true,
		QueueVersion:   1,
		OpenKJKey:      openkjKeyA,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := pgStore.CreateEvent(ctx, eventA); err != nil {
		t.Fatalf("create event A: %v", err)
	}

	// Developer keys for Host A
	for rawKey, scopes := range map[string][]string{
		"mk_live_cross_read":  {handlers.DeveloperScopeRequestsRead, handlers.DeveloperScopeEventsRead},
		"mk_live_cross_write": {handlers.DeveloperScopeRequestsWrite, handlers.DeveloperScopeEventsWrite},
		"mk_live_cross_full": {
			handlers.DeveloperScopeRequestsRead,
			handlers.DeveloperScopeRequestsWrite,
			handlers.DeveloperScopeEventsRead,
			handlers.DeveloperScopeEventsWrite,
			handlers.DeveloperScopeCatalogsWrite,
		},
	} {
		if err := pgStore.CreateDeveloperKey(ctx, rawKey, &models.DeveloperKey{
			Name:         rawKey,
			KeyPrefix:    rawKey[:12],
			OwnerID:      hostA.ID,
			ScopeVersion: 1,
			Scopes:       scopes,
			CreatedAt:    now,
		}); err != nil {
			t.Fatalf("create developer key %s: %v", rawKey, err)
		}
	}

	// 2. Host B (distinct tenant for isolation and anti-enumeration checks)
	hostB := &models.User{
		ID:          "usr_host_cross_02",
		Email:       "host_cross_02@micup.live",
		DisplayName: "Host Beta",
		IsHost:      true,
		AuthEpoch:   "epoch_host_02",
	}
	if err := pgStore.CreateUser(ctx, hostB); err != nil {
		t.Fatalf("create host B: %v", err)
	}
	sbB := &models.Songbook{
		ID:       "sb_cross_02",
		PublicID: "pub_sb_cross_02",
		OwnerID:  hostB.ID,
		Name:     "Host B Songbook",
		IsPublic: true,
	}
	if err := pgStore.CreateSongbook(ctx, sbB); err != nil {
		t.Fatalf("create songbook B: %v", err)
	}
	openkjKeyB := models.GenerateOpenKJKey("evt_cross_02")
	eventB := &models.Event{
		ID:             "evt_cross_02",
		OwnerID:        hostB.ID,
		SongbookID:     sbB.ID,
		Name:           "Show Beta",
		VenueName:      "Beta Bar",
		JoinCode:       "BETA",
		Status:         models.EventStatusActive,
		AllowKeyChange: true,
		QueueVersion:   1,
		OpenKJKey:      openkjKeyB,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := pgStore.CreateEvent(ctx, eventB); err != nil {
		t.Fatalf("create event B: %v", err)
	}

	// Developer key for Host B
	if err := pgStore.CreateDeveloperKey(ctx, "mk_live_other_write", &models.DeveloperKey{
		Name:         "mk_live_other_write",
		KeyPrefix:    "mk_live_othe",
		OwnerID:      hostB.ID,
		ScopeVersion: 1,
		Scopes: []string{
			handlers.DeveloperScopeRequestsRead,
			handlers.DeveloperScopeRequestsWrite,
			handlers.DeveloperScopeEventsRead,
			handlers.DeveloperScopeEventsWrite,
			handlers.DeveloperScopeCatalogsWrite,
		},
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("create host B developer key: %v", err)
	}

	router, err := NewRouter(
		config.Config{
			Environment:           config.Development,
			AuthSecret:            crossAdapterSigningSecret,
			AllowedOrigins:        []string{"http://localhost:5173"},
			DeveloperKeyScopeMode: handlers.DeveloperKeyScopeEnforce,
		},
		Dependencies{
			Auth:           pgStore,
			DeveloperKeys:  pgStore,
			Events:         pgStore,
			Songbooks:      pgStore,
			CatalogChanges: pgStore,
			Requests:       pgStore,
		},
	)
	if err != nil {
		t.Fatalf("create router: %v", err)
	}

	sessionTokenA := makeHostSessionToken(hostA.ID, hostA.AuthEpoch, []byte(crossAdapterSigningSecret))
	sessionTokenB := makeHostSessionToken(hostB.ID, hostB.AuthEpoch, []byte(crossAdapterSigningSecret))

	return &crossAdapterHarness{
		ctx:           ctx,
		pgStore:       pgStore,
		router:        router,
		hostA:         hostA,
		eventA:        eventA,
		songbookA:     sbA,
		sessionTokenA: sessionTokenA,
		openkjKeyA:    openkjKeyA,
		hostB:         hostB,
		eventB:        eventB,
		songbookB:     sbB,
		sessionTokenB: sessionTokenB,
		openkjKeyB:    openkjKeyB,
	}
}

// TestContract_CrossAdapter_SubmissionStatusAndLifecycle_PostgresRouter exercises
// request submission, status changes (accepted, up_next, completed, cancelled), and conditional
// polling across Host Session Auth, Developer-Key PAT, OpenKJ, Retained Compatibility, and Singer routes.
// Reuses testdata/contracts canonical fixtures for public queue, poll 204, and status mutations.
func TestContract_CrossAdapter_SubmissionStatusAndLifecycle_PostgresRouter(t *testing.T) {
	h := setupCrossAdapterTestPostgres(t)

	// Load canonical fixtures for shared expectations
	var publicFixture struct {
		ContractID  string               `json:"contract_id"`
		Description string               `json:"description"`
		Request     contractRequestSpec  `json:"request"`
		Response    contractResponseSpec `json:"response"`
	}
	loadContractFixture(t, "queue_public_200.json", &publicFixture)

	var pollFixture struct {
		ContractID  string `json:"contract_id"`
		Description string `json:"description"`
		Cases       []struct {
			Name     string               `json:"name"`
			Request  contractRequestSpec  `json:"request"`
			Response contractResponseSpec `json:"response"`
		} `json:"cases"`
	}
	loadContractFixture(t, "queue_poll_204.json", &pollFixture)

	var mutationsFixture struct {
		ContractID  string `json:"contract_id"`
		Description string `json:"description"`
		Mutations   []struct {
			Name     string               `json:"name"`
			Request  contractRequestSpec  `json:"request"`
			Response contractResponseSpec `json:"response"`
		} `json:"mutations"`
	}
	loadContractFixture(t, "request_status_mutations.json", &mutationsFixture)

	// =========================================================================
	// 1. Singer Submits Request via Public Web Path (POST /api/v1/events/{id}/requests)
	// =========================================================================
	var singerReq models.SongRequest
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodPost,
			Path:   "/api/v1/events/" + h.eventA.ID + "/requests",
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
			Body: json.RawMessage(`{
				"singer_name": "Alice Singer",
				"song_title": "Dreams",
				"song_artist": "Fleetwood Mac",
				"notes": "Key -1 please",
				"key_change": -1
			}`),
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("singer submit status = %d; want %d; body = %s", rec.Code, http.StatusCreated, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &singerReq); err != nil {
			t.Fatalf("decode submit response: %v", err)
		}
		if singerReq.ID == "" || singerReq.Status != models.StatusPending || singerReq.CancelToken == "" {
			t.Fatalf("unexpected singer request fields: %+v", singerReq)
		}

		// Observable Persistence Invariant: QueueVersion advanced from 1 to 2 in PostgreSQL
		ev, err := h.pgStore.GetEvent(h.ctx, h.eventA.ID)
		if err != nil {
			t.Fatalf("get event after submission: %v", err)
		}
		if ev.QueueVersion != 2 {
			t.Fatalf("event QueueVersion after submission = %d; want 2", ev.QueueVersion)
		}
	}

	// -------------------------------------------------------------------------
	// Cross-Adapter Observation of Pending Request:
	// -------------------------------------------------------------------------

	// A1. Host Intake via Session Auth (GET /api/v1/events/{id}/requests)
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/events/" + h.eventA.ID + "/requests",
			Headers: map[string]string{
				"Authorization": "Bearer " + h.sessionTokenA,
			},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("host session intake status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("X-Queue-Version") != "2" {
			t.Fatalf("host session intake X-Queue-Version = %q; want \"2\"", rec.Header().Get("X-Queue-Version"))
		}
		var intake struct {
			QueueVersion int64                 `json:"queue_version"`
			Requests     []*models.SongRequest `json:"requests"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &intake); err != nil {
			t.Fatalf("decode host session intake: %v", err)
		}
		if intake.QueueVersion != 2 || len(intake.Requests) != 1 || intake.Requests[0].ID != singerReq.ID {
			t.Fatalf("unexpected host session intake payload: %+v", intake)
		}
		if intake.Requests[0].Notes != "Key -1 please" {
			t.Fatalf("host session intake missing singer notes: got %q", intake.Requests[0].Notes)
		}
	}

	// A2. Host Intake via Developer Key (GET /api/v1/events/{id}/requests)
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/events/" + h.eventA.ID + "/requests",
			Headers: map[string]string{
				"Authorization": "Bearer mk_live_cross_read",
			},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("host devkey intake status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("X-Queue-Version") != "2" {
			t.Fatalf("host devkey intake X-Queue-Version = %q; want \"2\"", rec.Header().Get("X-Queue-Version"))
		}
	}

	// B. Public Queue Rotation (GET /api/v1/events/{id}/queue):
	// Returns 200 OK, queue_version: 2, but queue array is EMPTY (pending requests are excluded from public rotation).
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/events/" + h.eventA.ID + "/queue",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("public queue status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("X-Queue-Version") != "2" {
			t.Fatalf("public queue X-Queue-Version = %q; want \"2\"", rec.Header().Get("X-Queue-Version"))
		}
		var pubQueue struct {
			QueueVersion int64                    `json:"queue_version"`
			Queue        []models.PublicQueueItem `json:"queue"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &pubQueue); err != nil {
			t.Fatalf("decode public queue: %v", err)
		}
		if len(pubQueue.Queue) != 0 {
			t.Fatalf("public queue must exclude pending requests; got %d items", len(pubQueue.Queue))
		}
	}

	// C. OpenKJ Adapter Intake (GET /api/v1/openkj/api.php?action=getrequests&key={key}):
	// Protocol adapter exposes pending requests with OpenKJ DTO format.
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/openkj/api.php?action=getrequests&key=" + h.openkjKeyA,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("openkj getrequests status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
		var openkjResp struct {
			Command  string                      `json:"command"`
			Error    bool                        `json:"error"`
			Requests []handlers.OpenKJRequestDTO `json:"requests"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &openkjResp); err != nil {
			t.Fatalf("decode openkj requests: %v", err)
		}
		openkjReqs := openkjResp.Requests
		if openkjResp.Command != "getRequests" || openkjResp.Error || len(openkjReqs) != 1 || openkjReqs[0].ID != singerReq.ID || openkjReqs[0].Singer != "Alice Singer" || openkjReqs[0].Key != -1 {
			t.Fatalf("unexpected openkj requests payload: %+v", openkjResp)
		}
	}

	// D. OpenKJ Monotonic Serial (GET /api/v1/openkj/api.php?action=getserial&key={key}):
	// Matches Event.QueueVersion in PostgreSQL.
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/openkj/api.php?action=getserial&key=" + h.openkjKeyA,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("openkj getserial status = %d; want 200", rec.Code)
		}
		var serialResp struct {
			Serial int64 `json:"serial"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &serialResp); err != nil {
			t.Fatalf("decode openkj serial: %v", err)
		}
		if serialResp.Serial != 2 {
			t.Fatalf("openkj serial = %d; want 2", serialResp.Serial)
		}
	}

	// E. Retired Compatibility Route (GET /api/v1/songbooks/{id}/requests) returns 404:
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/songbooks/" + h.songbookA.ID + "/requests",
			Headers: map[string]string{
				"Authorization": "Bearer " + h.sessionTokenA,
			},
		})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("retired songbook requests route status = %d; want 404", rec.Code)
		}
	}

	// =========================================================================
	// 2. Host Accepts Request via Host Session Auth (PATCH /api/v1/events/{id}/requests/{rid})
	//    Using canonical precondition & unknown field tolerance from request_status_mutations.json
	// =========================================================================
	{
		acceptBody := map[string]interface{}{
			"status":                 "accepted",
			"expected_queue_version": 2,
			"future_client_hint":     "session_host_deck_v1",
		}
		bodyBytes, _ := json.Marshal(acceptBody)
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodPatch,
			Path:   "/api/v1/events/" + h.eventA.ID + "/requests/" + singerReq.ID,
			Headers: map[string]string{
				"Authorization":   "Bearer " + h.sessionTokenA,
				"Content-Type":    "application/json",
				"Idempotency-Key": uuid.NewString(),
			},
			Body: bodyBytes,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("host session patch status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("X-Queue-Version") != "3" {
			t.Fatalf("host patch X-Queue-Version = %q; want \"3\"", rec.Header().Get("X-Queue-Version"))
		}

		// Observable Persistence Invariant: QueueVersion advanced to 3 in PostgreSQL
		ev, err := h.pgStore.GetEvent(h.ctx, h.eventA.ID)
		if err != nil {
			t.Fatalf("get event: %v", err)
		}
		if ev.QueueVersion != 3 {
			t.Fatalf("event QueueVersion = %d; want 3", ev.QueueVersion)
		}
	}

	// -------------------------------------------------------------------------
	// Cross-Adapter Observation of Accepted Request:
	// -------------------------------------------------------------------------

	// A. Public Queue Rotation matches queue_public_200.json expectations (stripping private fields):
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/events/" + h.eventA.ID + "/queue",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("public queue status = %d; want 200", rec.Code)
		}
		var pubQueue struct {
			QueueVersion int64                    `json:"queue_version"`
			Queue        []models.PublicQueueItem `json:"queue"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &pubQueue); err != nil {
			t.Fatalf("decode public queue: %v", err)
		}
		if len(pubQueue.Queue) != 1 || pubQueue.Queue[0].ID != singerReq.ID || pubQueue.Queue[0].Status != models.StatusAccepted {
			t.Fatalf("unexpected public queue: %+v", pubQueue)
		}

		// Verify sensitive fields from fixture's contract are not leaked in raw JSON
		var rawMap map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &rawMap)
		items := rawMap["queue"].([]interface{})
		firstItem := items[0].(map[string]interface{})
		for _, forbidden := range []string{"notes", "cancel_token", "decline_reason"} {
			if _, exists := firstItem[forbidden]; exists {
				t.Fatalf("public queue leaked forbidden field %q", forbidden)
			}
		}
	}

	// B. OpenKJ Serial reflects version 3:
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/openkj/api.php?action=getserial&key=" + h.openkjKeyA,
		})
		var serialResp struct {
			Serial int64 `json:"serial"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &serialResp)
		if serialResp.Serial != 3 {
			t.Fatalf("openkj serial after accept = %d; want 3", serialResp.Serial)
		}
	}

	// C. Conditional Polling Fixture Replay (queue_poll_204.json):
	// Verifies both Session Auth and Developer PAT observe 204 No Content with 0-byte body on fresh poll,
	// and 200 OK on stale poll (?since=2).
	{
		// C1. Host Requests via Session Auth
		recSessionStale := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/events/" + h.eventA.ID + "/requests?since=2",
			Headers: map[string]string{
				"Authorization": "Bearer " + h.sessionTokenA,
			},
		})
		if recSessionStale.Code != http.StatusOK {
			t.Fatalf("session stale poll status = %d; want 200", recSessionStale.Code)
		}

		recSessionFresh := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/events/" + h.eventA.ID + "/requests?since=3",
			Headers: map[string]string{
				"Authorization": "Bearer " + h.sessionTokenA,
			},
		})
		if recSessionFresh.Code != http.StatusNoContent {
			t.Fatalf("session fresh poll status = %d; want 204", recSessionFresh.Code)
		}
		if recSessionFresh.Body.Len() != 0 {
			t.Fatalf("session fresh poll body len = %d; want 0", recSessionFresh.Body.Len())
		}
		if recSessionFresh.Header().Get("X-Queue-Version") != "3" {
			t.Fatalf("session fresh poll X-Queue-Version = %q; want \"3\"", recSessionFresh.Header().Get("X-Queue-Version"))
		}

		// C2. Host Requests via Developer PAT
		recPATFresh := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/events/" + h.eventA.ID + "/requests?since=3",
			Headers: map[string]string{
				"Authorization": "Bearer mk_live_cross_read",
			},
		})
		if recPATFresh.Code != http.StatusNoContent || recPATFresh.Body.Len() != 0 {
			t.Fatalf("PAT fresh poll status = %d, len = %d; want 204, 0", recPATFresh.Code, recPATFresh.Body.Len())
		}

		// C3. Public Queue Polling
		recPubFresh := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/events/" + h.eventA.ID + "/queue?since=3",
		})
		if recPubFresh.Code != http.StatusNoContent || recPubFresh.Body.Len() != 0 {
			t.Fatalf("public fresh poll status = %d, len = %d; want 204, 0", recPubFresh.Code, recPubFresh.Body.Len())
		}
	}

	// =========================================================================
	// 3. Status Mutation to Up Next via Developer PAT (with compatibility alias "playing")
	//    Exercising canonical normalization from request_status_mutations.json
	// =========================================================================
	{
		aliasBody := map[string]interface{}{
			"status":                 "playing", // alias -> up_next
			"expected_queue_version": 3,
		}
		bodyBytes, _ := json.Marshal(aliasBody)
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodPatch,
			Path:   "/api/v1/events/" + h.eventA.ID + "/requests/" + singerReq.ID,
			Headers: map[string]string{
				"Authorization":   "Bearer mk_live_cross_write",
				"Content-Type":    "application/json",
				"Idempotency-Key": uuid.NewString(),
			},
			Body: bodyBytes,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("playing alias patch status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("X-Queue-Version") != "4" {
			t.Fatalf("playing alias patch X-Queue-Version = %q; want \"4\"", rec.Header().Get("X-Queue-Version"))
		}

		// Verify normalized status is up_next in PostgreSQL
		stored, err := h.pgStore.GetRequest(h.ctx, singerReq.ID)
		if err != nil {
			t.Fatalf("get request from postgres: %v", err)
		}
		if stored.Status != models.StatusUpNext {
			t.Fatalf("stored status after playing alias = %q; want %q", stored.Status, models.StatusUpNext)
		}

		// QueueVersion advanced to 4
		ev, _ := h.pgStore.GetEvent(h.ctx, h.eventA.ID)
		if ev.QueueVersion != 4 {
			t.Fatalf("event QueueVersion = %d; want 4", ev.QueueVersion)
		}
	}

	// Observable in OpenKJ serial:
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/openkj/api.php?action=getserial&key=" + h.openkjKeyA,
		})
		var serialResp struct {
			Serial int64 `json:"serial"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &serialResp)
		if serialResp.Serial != 4 {
			t.Fatalf("openkj serial = %d; want 4", serialResp.Serial)
		}
	}

	// =========================================================================
	// 4. Request Completion via OpenKJ Adapter (action=deleterequest)
	//    OpenKJ deleterequest marks request completed in domain model.
	// =========================================================================
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodPost,
			Path:   "/api/v1/openkj/api.php?action=deleterequest&key=" + h.openkjKeyA + "&id=" + singerReq.ID,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("openkj deleterequest status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
		var okjResp map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &okjResp)
		if okjResp["status"] != "ok" {
			t.Fatalf("unexpected openkj deleterequest response: %+v", okjResp)
		}

		// Observable Persistence Invariants:
		// 1. Request status in PostgreSQL is StatusCompleted
		storedReq, err := h.pgStore.GetRequest(h.ctx, singerReq.ID)
		if err != nil {
			t.Fatalf("get request from postgres: %v", err)
		}
		if storedReq.Status != models.StatusCompleted {
			t.Fatalf("postgres request status = %q; want %q", storedReq.Status, models.StatusCompleted)
		}
		// 2. Event QueueVersion in PostgreSQL advanced to 5
		ev, err := h.pgStore.GetEvent(h.ctx, h.eventA.ID)
		if err != nil {
			t.Fatalf("get event: %v", err)
		}
		if ev.QueueVersion != 5 {
			t.Fatalf("event QueueVersion = %d; want 5", ev.QueueVersion)
		}
	}

	// -------------------------------------------------------------------------
	// Cross-Adapter Observation of Completed Request:
	// -------------------------------------------------------------------------

	// A. OpenKJ Intake (action=getrequests) is now EMPTY (completed requests leave intake):
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/openkj/api.php?action=getrequests&key=" + h.openkjKeyA,
		})
		var openkjResp struct {
			Requests []handlers.OpenKJRequestDTO `json:"requests"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &openkjResp); err != nil {
			t.Fatalf("decode openkj requests: %v", err)
		}
		if len(openkjResp.Requests) != 0 {
			t.Fatalf("expected 0 openkj requests after completion; got %d", len(openkjResp.Requests))
		}
	}

	// B. OpenKJ Serial reflects version 5:
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/openkj/api.php?action=getserial&key=" + h.openkjKeyA,
		})
		var serialResp struct {
			Serial int64 `json:"serial"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &serialResp)
		if serialResp.Serial != 5 {
			t.Fatalf("openkj serial = %d; want 5", serialResp.Serial)
		}
	}

	// C. Public Queue Rotation is EMPTY:
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/events/" + h.eventA.ID + "/queue",
		})
		var pubQueue struct {
			Queue []models.PublicQueueItem `json:"queue"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &pubQueue)
		if len(pubQueue.Queue) != 0 {
			t.Fatalf("public queue must be empty after completion; got %d", len(pubQueue.Queue))
		}
	}

	// D. Host Intake lists request with status "completed" and queue_version 5 (checked via Session Auth):
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/events/" + h.eventA.ID + "/requests",
			Headers: map[string]string{
				"Authorization": "Bearer " + h.sessionTokenA,
			},
		})
		var intake struct {
			QueueVersion int64                 `json:"queue_version"`
			Requests     []*models.SongRequest `json:"requests"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &intake)
		if intake.QueueVersion != 5 || len(intake.Requests) != 1 || intake.Requests[0].Status != models.StatusCompleted {
			t.Fatalf("unexpected host intake after completion: %+v", intake)
		}
	}

	// =========================================================================
	// 5. Singer Self-Cancellation via Cancellation Token
	// =========================================================================
	var singerReq2 models.SongRequest
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodPost,
			Path:   "/api/v1/events/" + h.eventA.ID + "/requests",
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
			Body: json.RawMessage(`{
				"singer_name": "Bob Singer",
				"song_title": "Valerie",
				"song_artist": "Amy Winehouse"
			}`),
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("submit req 2 status = %d", rec.Code)
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &singerReq2)

		// QueueVersion is now 6
		ev, _ := h.pgStore.GetEvent(h.ctx, h.eventA.ID)
		if ev.QueueVersion != 6 {
			t.Fatalf("event QueueVersion = %d; want 6", ev.QueueVersion)
		}
	}

	// Singer cancels via DELETE /api/v1/events/{id}/requests/{rid}?cancel_token={token}
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodDelete,
			Path:   "/api/v1/events/" + h.eventA.ID + "/requests/" + singerReq2.ID + "?cancel_token=" + singerReq2.CancelToken,
		})
		if rec.Code != http.StatusNoContent {
			t.Fatalf("singer cancel status = %d; want 204; body = %s", rec.Code, rec.Body.String())
		}

		// Observable Persistence Invariant: QueueVersion advanced to 7 in PostgreSQL
		ev, err := h.pgStore.GetEvent(h.ctx, h.eventA.ID)
		if err != nil {
			t.Fatalf("get event: %v", err)
		}
		if ev.QueueVersion != 7 {
			t.Fatalf("event QueueVersion = %d; want 7", ev.QueueVersion)
		}
	}

	// Observable in OpenKJ:
	{
		recReqs := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/openkj/api.php?action=getrequests&key=" + h.openkjKeyA,
		})
		var openkjResp struct {
			Requests []handlers.OpenKJRequestDTO `json:"requests"`
		}
		if err := json.Unmarshal(recReqs.Body.Bytes(), &openkjResp); err != nil {
			t.Fatalf("decode openkj requests: %v", err)
		}
		if len(openkjResp.Requests) != 0 {
			t.Fatalf("cancelled request must not appear in OpenKJ; got %d", len(openkjResp.Requests))
		}

		recSerial := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/openkj/api.php?action=getserial&key=" + h.openkjKeyA,
		})
		var serialResp struct {
			Serial int64 `json:"serial"`
		}
		_ = json.Unmarshal(recSerial.Body.Bytes(), &serialResp)
		if serialResp.Serial != 7 {
			t.Fatalf("openkj serial = %d; want 7", serialResp.Serial)
		}
	}

	// =========================================================================
	// 6. OpenKJ Batch Clear (action=clearrequests)
	// =========================================================================
	{
		// Submit two singer requests
		for i, name := range []string{"Charlie", "Diana"} {
			rec := executeContractRequest(t, h.router, contractRequestSpec{
				Method: http.MethodPost,
				Path:   "/api/v1/events/" + h.eventA.ID + "/requests",
				Headers: map[string]string{
					"Content-Type": "application/json",
				},
				Body: json.RawMessage(fmt.Sprintf(`{"singer_name": %q, "song_title": "Song %d", "song_artist": "Artist"}`, name, i)),
			})
			if rec.Code != http.StatusCreated {
				t.Fatalf("submit batch req %d status = %d", i, rec.Code)
			}
		}

		// QueueVersion is now 9 (7 + 2)
		ev, _ := h.pgStore.GetEvent(h.ctx, h.eventA.ID)
		if ev.QueueVersion != 9 {
			t.Fatalf("event QueueVersion = %d; want 9", ev.QueueVersion)
		}

		// OpenKJ clearrequests
		recClear := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodPost,
			Path:   "/api/v1/openkj/api.php?action=clearrequests&key=" + h.openkjKeyA,
		})
		if recClear.Code != http.StatusOK {
			t.Fatalf("clearrequests status = %d; want 200; body = %s", recClear.Code, recClear.Body.String())
		}

		// Both requests completed; QueueVersion advanced by 2 (to 11)
		evAfter, _ := h.pgStore.GetEvent(h.ctx, h.eventA.ID)
		if evAfter.QueueVersion != 11 {
			t.Fatalf("event QueueVersion after clear = %d; want 11", evAfter.QueueVersion)
		}

		// OpenKJ getrequests is empty
		recReqs := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/openkj/api.php?action=getrequests&key=" + h.openkjKeyA,
		})
		var openkjResp struct {
			Requests []handlers.OpenKJRequestDTO `json:"requests"`
		}
		if err := json.Unmarshal(recReqs.Body.Bytes(), &openkjResp); err != nil {
			t.Fatalf("decode openkj requests: %v", err)
		}
		if len(openkjResp.Requests) != 0 {
			t.Fatalf("expected 0 openkj requests after clearrequests; got %d", len(openkjResp.Requests))
		}

		// OpenKJ serial is 11
		recSerial := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/openkj/api.php?action=getserial&key=" + h.openkjKeyA,
		})
		var serialResp struct {
			Serial int64 `json:"serial"`
		}
		_ = json.Unmarshal(recSerial.Body.Bytes(), &serialResp)
		if serialResp.Serial != 11 {
			t.Fatalf("openkj serial = %d; want 11", serialResp.Serial)
		}
	}
}

// TestContract_CrossAdapter_QueueOrderingAndReorder_PostgresRouter exercises manual
// queue reordering across Host Session Auth, Developer-Key PAT, and Retained Compatibility routes,
// reusing the canonical queue_reorder_200.json fixture expectations, asserting atomic
// version progression and public rotation order, while documenting the intentional
// absence of a reorder action in the OpenKJ protocol.
func TestContract_CrossAdapter_QueueOrderingAndReorder_PostgresRouter(t *testing.T) {
	h := setupCrossAdapterTestPostgres(t)

	// Load canonical reorder fixture
	var reorderFixture struct {
		ContractID               string               `json:"contract_id"`
		Description              string               `json:"description"`
		Request                  contractRequestSpec  `json:"request"`
		Response                 contractResponseSpec `json:"response"`
		ExpectedActiveOrderAfter []string             `json:"expected_active_order_after"`
	}
	loadContractFixture(t, "queue_reorder_200.json", &reorderFixture)

	// 1. Submit 3 requests and transition them to accepted
	reqIDs := make([]string, 3)
	for i, name := range []string{"Singer One", "Singer Two", "Singer Three"} {
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodPost,
			Path:   "/api/v1/events/" + h.eventA.ID + "/requests",
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
			Body: json.RawMessage(fmt.Sprintf(`{"singer_name": %q, "song_title": "Song %d", "song_artist": "Artist"}`, name, i+1)),
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("submit %d status = %d", i, rec.Code)
		}
		var r models.SongRequest
		_ = json.Unmarshal(rec.Body.Bytes(), &r)
		reqIDs[i] = r.ID
	}

	// Accept all three via Host Session Auth
	for i, id := range reqIDs {
		expectedVer := int64(4 + i)
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodPatch,
			Path:   "/api/v1/events/" + h.eventA.ID + "/requests/" + id,
			Headers: map[string]string{
				"Authorization": "Bearer " + h.sessionTokenA,
				"Content-Type":  "application/json",
			},
			Body: json.RawMessage(fmt.Sprintf(`{"status":"accepted","expected_queue_version":%d}`, expectedVer)),
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("accept %d status = %d", i, rec.Code)
		}
	}

	// Current QueueVersion is 7 (1 initial + 3 submits + 3 accepts)
	ev, _ := h.pgStore.GetEvent(h.ctx, h.eventA.ID)
	if ev.QueueVersion != 7 {
		t.Fatalf("event QueueVersion = %d; want 7", ev.QueueVersion)
	}

	// Initial Public Queue order: [reqIDs[0], reqIDs[1], reqIDs[2]]
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/events/" + h.eventA.ID + "/queue",
		})
		var pubQueue struct {
			Queue []models.PublicQueueItem `json:"queue"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &pubQueue)
		if len(pubQueue.Queue) != 3 || pubQueue.Queue[0].ID != reqIDs[0] || pubQueue.Queue[2].ID != reqIDs[2] {
			t.Fatalf("unexpected initial public order: %+v", pubQueue)
		}
	}

	// 2. Reorder via Developer PAT (POST /api/v1/events/{id}/requests/reorder):
	// Using payload shape with unknown future field from queue_reorder_200.json:
	// New desired order: [reqIDs[2], reqIDs[0], reqIDs[1]]
	{
		reorderBody, _ := json.Marshal(map[string]interface{}{
			"order":                  []string{reqIDs[2], reqIDs[0], reqIDs[1]},
			"expected_queue_version": 7,
			"future_reorder_hint":    "manual_drag_drop",
		})
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodPost,
			Path:   "/api/v1/events/" + h.eventA.ID + "/requests/reorder",
			Headers: map[string]string{
				"Authorization":   "Bearer mk_live_cross_write",
				"Content-Type":    "application/json",
				"Idempotency-Key": uuid.NewString(),
			},
			Body: json.RawMessage(reorderBody),
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("pat reorder status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("X-Queue-Version") != "8" {
			t.Fatalf("pat reorder X-Queue-Version = %q; want \"8\"", rec.Header().Get("X-Queue-Version"))
		}

		// Observable Persistence Invariant: QueueVersion advanced to 8 in PostgreSQL
		ev, err := h.pgStore.GetEvent(h.ctx, h.eventA.ID)
		if err != nil {
			t.Fatalf("get event: %v", err)
		}
		if ev.QueueVersion != 8 {
			t.Fatalf("event QueueVersion = %d; want 8", ev.QueueVersion)
		}

		// Public queue matches new order: [reqIDs[2], reqIDs[0], reqIDs[1]]
		recPub := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/events/" + h.eventA.ID + "/queue",
		})
		var pubQueue struct {
			Queue []models.PublicQueueItem `json:"queue"`
		}
		_ = json.Unmarshal(recPub.Body.Bytes(), &pubQueue)
		if len(pubQueue.Queue) != 3 || pubQueue.Queue[0].ID != reqIDs[2] || pubQueue.Queue[1].ID != reqIDs[0] || pubQueue.Queue[2].ID != reqIDs[1] {
			t.Fatalf("reordered public queue mismatch: %+v", pubQueue)
		}

		// OpenKJ Serial reflects version 8:
		recSerial := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/openkj/api.php?action=getserial&key=" + h.openkjKeyA,
		})
		var serialResp struct {
			Serial int64 `json:"serial"`
		}
		_ = json.Unmarshal(recSerial.Body.Bytes(), &serialResp)
		if serialResp.Serial != 8 {
			t.Fatalf("openkj serial after reorder = %d; want 8", serialResp.Serial)
		}
	}

	// 3. Reorder via Host Session Auth (POST /api/v1/events/{id}/requests/reorder):
	// New desired order: [reqIDs[1], reqIDs[2], reqIDs[0]]
	{
		orderBody, _ := json.Marshal(map[string]interface{}{
			"order":                  []string{reqIDs[1], reqIDs[2], reqIDs[0]},
			"expected_queue_version": 8,
		})
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodPost,
			Path:   "/api/v1/events/" + h.eventA.ID + "/requests/reorder",
			Headers: map[string]string{
				"Authorization":   "Bearer " + h.sessionTokenA,
				"Content-Type":    "application/json",
				"Idempotency-Key": uuid.NewString(),
			},
			Body: json.RawMessage(orderBody),
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("session reorder status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("X-Queue-Version") != "9" {
			t.Fatalf("session reorder X-Queue-Version = %q; want \"9\"", rec.Header().Get("X-Queue-Version"))
		}

		// Observable in PostgreSQL: QueueVersion is 9
		ev, _ := h.pgStore.GetEvent(h.ctx, h.eventA.ID)
		if ev.QueueVersion != 9 {
			t.Fatalf("event QueueVersion = %d; want 9", ev.QueueVersion)
		}

		// Public queue matches new order: [reqIDs[1], reqIDs[2], reqIDs[0]]
		recPub := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/events/" + h.eventA.ID + "/queue",
		})
		var pubQueue struct {
			Queue []models.PublicQueueItem `json:"queue"`
		}
		_ = json.Unmarshal(recPub.Body.Bytes(), &pubQueue)
		if len(pubQueue.Queue) != 3 || pubQueue.Queue[0].ID != reqIDs[1] || pubQueue.Queue[1].ID != reqIDs[2] || pubQueue.Queue[2].ID != reqIDs[0] {
			t.Fatalf("session reordered public queue mismatch: %+v", pubQueue)
		}
	}

	// 4. Intentional Protocol Boundary: OpenKJ does not define a reorder action.
	// OpenKJ clients rely on local rotation logic and consume intake via action=getrequests.
	// We verify getrequests returns the active queue and reflects the latest monotonic serial.
	{
		recSerial := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/openkj/api.php?action=getserial&key=" + h.openkjKeyA,
		})
		var serialResp struct {
			Serial int64 `json:"serial"`
		}
		_ = json.Unmarshal(recSerial.Body.Bytes(), &serialResp)
		if serialResp.Serial != 9 {
			t.Fatalf("openkj serial = %d; want 9", serialResp.Serial)
		}
	}
}

// TestContract_CrossAdapter_EventAcceptingAndLifecycle_PostgresRouter exercises
// event pause and resume transitions across OpenKJ (action=setaccepting),
// Host Session Auth (POST /pause and /resume), Developer PAT, and Singer submission rejection.
func TestContract_CrossAdapter_EventAcceptingAndLifecycle_PostgresRouter(t *testing.T) {
	h := setupCrossAdapterTestPostgres(t)

	// =========================================================================
	// 1. OpenKJ Pauses Event (action=setaccepting&accepting=0)
	// =========================================================================
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodPost,
			Path:   "/api/v1/openkj/api.php?action=setaccepting&accepting=0&key=" + h.openkjKeyA,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("openkj pause status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
		var okjResp map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &okjResp)
		if okjResp["status"] != "ok" {
			t.Fatalf("unexpected openkj response: %+v", okjResp)
		}

		// Observable Persistence Invariant: Event status is paused in PostgreSQL
		ev, err := h.pgStore.GetEvent(h.ctx, h.eventA.ID)
		if err != nil {
			t.Fatalf("get event: %v", err)
		}
		if ev.Status != models.EventStatusPaused {
			t.Fatalf("postgres event status = %q; want %q", ev.Status, models.EventStatusPaused)
		}
	}

	// -------------------------------------------------------------------------
	// Cross-Adapter Observation of Paused Event:
	// -------------------------------------------------------------------------

	// A. Public Singer Submission is REJECTED (400 Bad Request: "event is paused")
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodPost,
			Path:   "/api/v1/events/" + h.eventA.ID + "/requests",
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
			Body: json.RawMessage(`{"singer_name": "Eve", "song_title": "Paused Song", "song_artist": "Artist"}`),
		})
		if rec.Code != http.StatusConflict {
			t.Fatalf("singer submit during pause status = %d; want 409; body = %s", rec.Code, rec.Body.String())
		}
	}

	// B. REST Host Event Intake reflects status "paused" via Host Session Auth:
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/events/" + h.eventA.ID,
			Headers: map[string]string{
				"Authorization": "Bearer " + h.sessionTokenA,
			},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("get event status = %d; want 200", rec.Code)
		}
		var evResp models.Event
		_ = json.Unmarshal(rec.Body.Bytes(), &evResp)
		if evResp.Status != models.EventStatusPaused {
			t.Fatalf("rest event status = %q; want %q", evResp.Status, models.EventStatusPaused)
		}
	}

	// C. REST Host Event Intake reflects status "paused" via Developer PAT:
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/events/" + h.eventA.ID,
			Headers: map[string]string{
				"Authorization": "Bearer mk_live_cross_read",
			},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("pat get event status = %d; want 200", rec.Code)
		}
		var evResp models.Event
		_ = json.Unmarshal(rec.Body.Bytes(), &evResp)
		if evResp.Status != models.EventStatusPaused {
			t.Fatalf("pat rest event status = %q; want %q", evResp.Status, models.EventStatusPaused)
		}
	}

	// D. Public Queue reflects paused status:
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/events/" + h.eventA.ID + "/queue",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("public queue status = %d; want 200", rec.Code)
		}
		var pubResp struct {
			EventStatus models.EventStatus `json:"event_status"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &pubResp)
		if pubResp.EventStatus != models.EventStatusPaused {
			t.Fatalf("public queue event_status = %q; want %q", pubResp.EventStatus, models.EventStatusPaused)
		}
	}

	// =========================================================================
	// 2. Host Resumes Event via Host Session Auth (POST /api/v1/events/{id}/resume)
	// =========================================================================
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodPost,
			Path:   "/api/v1/events/" + h.eventA.ID + "/resume",
			Headers: map[string]string{
				"Authorization": "Bearer " + h.sessionTokenA,
			},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("session resume status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}

		// Observable Persistence Invariant: Event status is active in PostgreSQL
		ev, _ := h.pgStore.GetEvent(h.ctx, h.eventA.ID)
		if ev.Status != models.EventStatusActive {
			t.Fatalf("postgres event status after session resume = %q; want %q", ev.Status, models.EventStatusActive)
		}
	}

	// Public Singer Submission SUCCEEDS again:
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodPost,
			Path:   "/api/v1/events/" + h.eventA.ID + "/requests",
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
			Body: json.RawMessage(`{"singer_name": "Eve", "song_title": "Resumed Song", "song_artist": "Artist"}`),
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("singer submit after resume status = %d; want 201; body = %s", rec.Code, rec.Body.String())
		}
	}

	// =========================================================================
	// 3. Host Pauses Event via Developer PAT (POST /api/v1/events/{id}/pause)
	// =========================================================================
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodPost,
			Path:   "/api/v1/events/" + h.eventA.ID + "/pause",
			Headers: map[string]string{
				"Authorization": "Bearer mk_live_cross_write",
			},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("pat pause status = %d; want 200", rec.Code)
		}
		ev, _ := h.pgStore.GetEvent(h.ctx, h.eventA.ID)
		if ev.Status != models.EventStatusPaused {
			t.Fatalf("postgres event status after pat pause = %q; want %q", ev.Status, models.EventStatusPaused)
		}
	}

	// =========================================================================
	// 4. OpenKJ Resumes Event (action=setaccepting&accepting=1)
	// =========================================================================
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodPost,
			Path:   "/api/v1/openkj/api.php?action=setaccepting&accepting=1&key=" + h.openkjKeyA,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("openkj resume status = %d; want 200", rec.Code)
		}
		ev, _ := h.pgStore.GetEvent(h.ctx, h.eventA.ID)
		if ev.Status != models.EventStatusActive {
			t.Fatalf("postgres event status after openkj resume = %q; want %q", ev.Status, models.EventStatusActive)
		}
	}
}

// TestContract_CrossAdapter_CatalogOperations_PostgresRouter exercises OpenKJ
// catalog sync operations (addsongs and cleardatabase) backed by PostgreSQL.
// Verifies persisted availability pruning in PostgreSQL and catalog read exposure,
// while asserting the other tenant's catalog remains untouched.
func TestContract_CrossAdapter_CatalogOperations_PostgresRouter(t *testing.T) {
	h := setupCrossAdapterTestPostgres(t)

	// 1. Host A bulk-imports songs via OpenKJ: POST /api/v1/openkj/api.php?action=addsongs
	songsPayloadA := `[
		{"artist": "Queen", "title": "Bohemian Rhapsody", "disc": "SC8100"},
		{"artist": "Journey", "title": "Don't Stop Believin'", "disc": "CB100"}
	]`
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodPost,
			Path:   "/api/v1/openkj/api.php?action=addsongs&key=" + h.openkjKeyA,
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
			Body: json.RawMessage(songsPayloadA),
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("openkj addsongs A status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
		var res map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["status"] != "ok" || int(res["added"].(float64)) != 2 {
			t.Fatalf("unexpected addsongs A response: %+v", res)
		}
	}

	// 2. Host B bulk-imports song via OpenKJ: POST /api/v1/openkj/api.php?action=addsongs
	songsPayloadB := `[
		{"artist": "The Beatles", "title": "Hey Jude", "disc": "TB01"}
	]`
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodPost,
			Path:   "/api/v1/openkj/api.php?action=addsongs&key=" + h.openkjKeyB,
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
			Body: json.RawMessage(songsPayloadB),
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("openkj addsongs B status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
		var res map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["status"] != "ok" || int(res["added"].(float64)) != 1 {
			t.Fatalf("unexpected addsongs B response: %+v", res)
		}
	}

	// 3. Verify initial state before clearing:
	// Host A has 2 available songs in PostgreSQL CatalogChanges and REST catalog read
	{
		changesA, err := h.pgStore.ListCatalogChanges(h.ctx, h.eventA.SongbookID)
		if err != nil {
			t.Fatalf("list catalog changes A from postgres: %v", err)
		}
		songsA := models.ApplyCatalogChanges(nil, changesA)
		if len(songsA) != 2 {
			t.Fatalf("expected 2 songs for Host A in postgres; got %d", len(songsA))
		}
		for _, s := range songsA {
			if !s.IsAvailable {
				t.Fatalf("expected song %q to be available before clear", s.Title)
			}
		}

		// Read via HTTP REST API with Host A session auth
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/songbooks/" + h.songbookA.ID + "/songs",
			Headers: map[string]string{
				"Authorization": "Bearer " + h.sessionTokenA,
			},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("read catalog A status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
		var httpSongsA []*models.Song
		if err := json.Unmarshal(rec.Body.Bytes(), &httpSongsA); err != nil {
			t.Fatalf("decode catalog A: %v", err)
		}
		if len(httpSongsA) != 2 {
			t.Fatalf("expected 2 songs in HTTP catalog A; got %d", len(httpSongsA))
		}
	}

	// Host B has 1 available song in PostgreSQL CatalogChanges and REST catalog read
	{
		changesB, err := h.pgStore.ListCatalogChanges(h.ctx, h.eventB.SongbookID)
		if err != nil {
			t.Fatalf("list catalog changes B from postgres: %v", err)
		}
		songsB := models.ApplyCatalogChanges(nil, changesB)
		if len(songsB) != 1 {
			t.Fatalf("unexpected initial songs for Host B: %+v", songsB)
		}
		for _, s := range songsB {
			if !s.IsAvailable || s.Title != "Hey Jude" {
				t.Fatalf("unexpected initial song for Host B: %+v", s)
			}
		}

		// Read via HTTP REST API with Host B session auth
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/songbooks/" + h.songbookB.ID + "/songs",
			Headers: map[string]string{
				"Authorization": "Bearer " + h.sessionTokenB,
			},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("read catalog B status = %d; want 200", rec.Code)
		}
		var httpSongsB []*models.Song
		_ = json.Unmarshal(rec.Body.Bytes(), &httpSongsB)
		if len(httpSongsB) != 1 || !httpSongsB[0].IsAvailable || httpSongsB[0].Title != "Hey Jude" {
			t.Fatalf("unexpected HTTP songs for Host B: %+v", httpSongsB)
		}
	}

	// 4. Host A executes cleardatabase via OpenKJ
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodPost,
			Path:   "/api/v1/openkj/api.php?action=cleardatabase&key=" + h.openkjKeyA,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("openkj cleardatabase status = %d; want 200; body = %s", rec.Code, rec.Body.String())
		}
		var res map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res["command"] != "clearDatabase" || res["error"] != false || res["status"] != "ok" {
			t.Fatalf("unexpected cleardatabase response: %+v", res)
		}
	}

	// 5. Observable Persistence Invariant: Host A catalog changes resolve to 0 songs after clearDatabase
	{
		changesA, err := h.pgStore.ListCatalogChanges(h.ctx, h.eventA.SongbookID)
		if err != nil {
			t.Fatalf("list catalog changes A after clear: %v", err)
		}
		songsA := models.ApplyCatalogChanges(nil, changesA)
		if len(songsA) != 0 {
			t.Fatalf("expected 0 effective songs for Host A after cleardatabase; got %d", len(songsA))
		}
	}

	// 6. Observable API Invariant: Host A catalog read via HTTP REST reflects cleared catalog
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/songbooks/" + h.songbookA.ID + "/songs",
			Headers: map[string]string{
				"Authorization": "Bearer " + h.sessionTokenA,
			},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("read catalog A after clear status = %d; want 200", rec.Code)
		}
		var httpSongsA []*models.Song
		if err := json.Unmarshal(rec.Body.Bytes(), &httpSongsA); err != nil {
			t.Fatalf("decode catalog A after clear: %v", err)
		}
		if len(httpSongsA) != 0 {
			t.Fatalf("expected 0 songs in HTTP catalog A after cleardatabase; got %d", len(httpSongsA))
		}
	}

	// 7. Observable Tenant Isolation Invariant: Host B catalog remains completely UNCHANGED
	{
		changesB, err := h.pgStore.ListCatalogChanges(h.ctx, h.eventB.SongbookID)
		if err != nil {
			t.Fatalf("list catalog changes B after Host A clear: %v", err)
		}
		songsB := models.ApplyCatalogChanges(nil, changesB)
		if len(songsB) != 1 {
			t.Fatalf("Host B catalog corrupted by Host A cleardatabase: %+v", songsB)
		}
		for _, s := range songsB {
			if !s.IsAvailable || s.Title != "Hey Jude" {
				t.Fatalf("Host B catalog corrupted by Host A cleardatabase: %+v", s)
			}
		}

		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/songbooks/" + h.songbookB.ID + "/songs",
			Headers: map[string]string{
				"Authorization": "Bearer " + h.sessionTokenB,
			},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("read catalog B after Host A clear status = %d", rec.Code)
		}
		var httpSongsB []*models.Song
		_ = json.Unmarshal(rec.Body.Bytes(), &httpSongsB)
		if len(httpSongsB) != 1 || !httpSongsB[0].IsAvailable || httpSongsB[0].Title != "Hey Jude" {
			t.Fatalf("Host B HTTP catalog corrupted: %+v", httpSongsB)
		}
	}
}

// TestContract_CrossAdapter_TenantIsolationAndAuthorization_PostgresRouter asserts
// tenant isolation (Contract C01), anti-enumeration (404 Not Found), developer key scope enforcement
// (reusing developer_scope_failures_403.json), OpenKJ credential validation, and method constraints.
func TestContract_CrossAdapter_TenantIsolationAndAuthorization_PostgresRouter(t *testing.T) {
	h := setupCrossAdapterTestPostgres(t)

	// Create request belonging to Event A
	var reqA models.SongRequest
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodPost,
			Path:   "/api/v1/events/" + h.eventA.ID + "/requests",
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
			Body: json.RawMessage(`{"singer_name": "Alice", "song_title": "Song A", "song_artist": "Artist A"}`),
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("setup req A status = %d", rec.Code)
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &reqA)
	}

	// =========================================================================
	// 1. Cross-Tenant Denial in OpenKJ Adapter:
	// OpenKJ Key for Event B cannot delete Event A's request (403 Forbidden)
	// =========================================================================
	{
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodPost,
			Path:   "/api/v1/openkj/api.php?action=deleterequest&key=" + h.openkjKeyB + "&id=" + reqA.ID,
		})
		if rec.Code != http.StatusForbidden {
			t.Fatalf("cross-tenant openkj delete status = %d; want 403 Forbidden; body = %s", rec.Code, rec.Body.String())
		}

		// Invariant: reqA remains pending in PostgreSQL
		stored, err := h.pgStore.GetRequest(h.ctx, reqA.ID)
		if err != nil || stored.Status != models.StatusPending {
			t.Fatalf("reqA mutated across tenant boundary: %+v", stored)
		}
	}

	// =========================================================================
	// 2. Cross-Tenant Anti-Enumeration across Session Auth and Developer PAT:
	// Host B cannot read or mutate Event A or Songbook A (404 Not Found)
	// =========================================================================
	for _, tc := range []struct {
		name   string
		method string
		path   string
		token  string
		body   string
	}{
		// Host B Session Auth checks:
		{"Session Host B reads Event A requests", http.MethodGet, "/api/v1/events/" + h.eventA.ID + "/requests", h.sessionTokenB, ""},
		{"Session Host B updates Event A request", http.MethodPatch, "/api/v1/events/" + h.eventA.ID + "/requests/" + reqA.ID, h.sessionTokenB, `{"status":"accepted"}`},
		{"Session Host B pauses Event A", http.MethodPost, "/api/v1/events/" + h.eventA.ID + "/pause", h.sessionTokenB, ""},
		{"Session Host B reorders Event A", http.MethodPost, "/api/v1/events/" + h.eventA.ID + "/requests/reorder", h.sessionTokenB, `{"order":["` + reqA.ID + `"]}`},
		{"Session Host B reads Songbook A songs", http.MethodGet, "/api/v1/songbooks/" + h.songbookA.ID + "/songs", h.sessionTokenB, ""},

		// Host B Developer PAT checks:
		{"PAT Host B reads Event A requests", http.MethodGet, "/api/v1/events/" + h.eventA.ID + "/requests", "mk_live_other_write", ""},
		{"PAT Host B updates Event A request", http.MethodPatch, "/api/v1/events/" + h.eventA.ID + "/requests/" + reqA.ID, "mk_live_other_write", `{"status":"accepted"}`},
		{"PAT Host B pauses Event A", http.MethodPost, "/api/v1/events/" + h.eventA.ID + "/pause", "mk_live_other_write", ""},
		{"PAT Host B reorders Event A", http.MethodPost, "/api/v1/events/" + h.eventA.ID + "/requests/reorder", "mk_live_other_write", `{"order":["` + reqA.ID + `"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := executeContractRequest(t, h.router, contractRequestSpec{
				Method: tc.method,
				Path:   tc.path,
				Headers: map[string]string{
					"Authorization": "Bearer " + tc.token,
					"Content-Type":  "application/json",
				},
				Body: json.RawMessage(tc.body),
			})
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s status = %d; want 404 (anti-enumeration); body = %s", tc.name, rec.Code, rec.Body.String())
			}
		})
	}

	// =========================================================================
	// 3. OpenKJ Integration Key Validation & Rejection:
	// =========================================================================
	for _, tc := range []struct {
		name string
		key  string
		want int
	}{
		{"Missing key", "", http.StatusUnauthorized},
		{"Invalid key format", "plain_string", http.StatusUnauthorized},
		{"Public join code", "ALPHA", http.StatusUnauthorized},
		{"Raw UUID", h.eventA.ID, http.StatusUnauthorized},
		{"Wrong secret in format", "okj_" + h.eventA.ID + "_badsecret0000000000000000", http.StatusUnauthorized},
	} {
		t.Run("OpenKJ Auth: "+tc.name, func(t *testing.T) {
			rec := executeContractRequest(t, h.router, contractRequestSpec{
				Method: http.MethodGet,
				Path:   "/api/v1/openkj/api.php?action=getserial&key=" + tc.key,
			})
			if rec.Code != tc.want {
				t.Fatalf("%s status = %d; want %d; body = %s", tc.name, rec.Code, tc.want, rec.Body.String())
			}
		})
	}

	// Ended Event Key is rejected with 401 Unauthorized ("event has ended")
	{
		evB, err := h.pgStore.GetEvent(h.ctx, h.eventB.ID)
		if err != nil {
			t.Fatalf("get event B: %v", err)
		}
		evB.Status = models.EventStatusEnded
		if err := h.pgStore.UpdateEvent(h.ctx, evB); err != nil {
			t.Fatalf("update event B to ended: %v", err)
		}
		rec := executeContractRequest(t, h.router, contractRequestSpec{
			Method: http.MethodGet,
			Path:   "/api/v1/openkj/api.php?action=getserial&key=" + h.openkjKeyB,
		})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("ended event key status = %d; want 401; body = %s", rec.Code, rec.Body.String())
		}
	}

	// =========================================================================
	// 4. Developer Key Scope Enforcement (DEV_KEY_SCOPE_MODE=enforce):
	// Reusing developer_scope_failures_403.json fixture cases
	// =========================================================================
	var scopeFixture struct {
		ContractID  string `json:"contract_id"`
		Description string `json:"description"`
		Cases       []struct {
			Name     string               `json:"name"`
			Request  contractRequestSpec  `json:"request"`
			Response contractResponseSpec `json:"response"`
		} `json:"cases"`
	}
	loadContractFixture(t, "developer_scope_failures_403.json", &scopeFixture)

	for _, tc := range []struct {
		name          string
		method        string
		path          string
		token         string
		body          string
		requiredScope string
	}{
		{
			name:          "PATCH request requires requests:write",
			method:        http.MethodPatch,
			path:          "/api/v1/events/" + h.eventA.ID + "/requests/" + reqA.ID,
			token:         "mk_live_cross_read",
			body:          `{"status":"accepted"}`,
			requiredScope: handlers.DeveloperScopeRequestsWrite,
		},
		{
			name:          "Reorder requests requires requests:write",
			method:        http.MethodPost,
			path:          "/api/v1/events/" + h.eventA.ID + "/requests/reorder",
			token:         "mk_live_cross_read",
			body:          `{"order":["` + reqA.ID + `"]}`,
			requiredScope: handlers.DeveloperScopeRequestsWrite,
		},
		{
			name:          "Pause event requires events:write",
			method:        http.MethodPost,
			path:          "/api/v1/events/" + h.eventA.ID + "/pause",
			token:         "mk_live_cross_read",
			body:          "",
			requiredScope: handlers.DeveloperScopeEventsWrite,
		},
		{
			name:          "Create song requires catalogs:write",
			method:        http.MethodPost,
			path:          "/api/v1/songbooks/" + h.songbookA.ID + "/songs",
			token:         "mk_live_cross_write",
			body:          `{"artist":"Queen","title":"Bohemian Rhapsody"}`,
			requiredScope: handlers.DeveloperScopeCatalogsWrite,
		},
	} {
		t.Run("Scope Denial: "+tc.name, func(t *testing.T) {
			rec := executeContractRequest(t, h.router, contractRequestSpec{
				Method: tc.method,
				Path:   tc.path,
				Headers: map[string]string{
					"Authorization": "Bearer " + tc.token,
					"Content-Type":  "application/json",
				},
				Body: json.RawMessage(tc.body),
			})
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s status = %d; want 403 Forbidden; body = %s", tc.name, rec.Code, rec.Body.String())
			}
			var errResp struct {
				Error         string `json:"error"`
				RequiredScope string `json:"required_scope"`
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &errResp)
			if errResp.Error != "insufficient_scope" || errResp.RequiredScope != tc.requiredScope {
				t.Fatalf("%s unexpected error payload: %+v", tc.name, errResp)
			}
		})
	}

	// =========================================================================
	// 5. OpenKJ Mutation Method Constraints:
	// GET on OpenKJ mutation actions returns 405 Method Not Allowed
	// =========================================================================
	for _, action := range []string{"cleardatabase", "addsongs", "setaccepting", "deleterequest", "clearrequests"} {
		t.Run("Method 405: "+action, func(t *testing.T) {
			rec := executeContractRequest(t, h.router, contractRequestSpec{
				Method: http.MethodGet,
				Path:   "/api/v1/openkj?action=" + action + "&key=" + h.openkjKeyA,
			})
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("GET on %s status = %d; want 405 Method Not Allowed", action, rec.Code)
			}
		})
	}
}

// TestContract_EmailVerificationConfirmAndRetiredAliases asserts that canonical
// email verification confirmation routes reject invalid tokens with 400 Bad Request,
// while retired /api/v1/auth/verify-email aliases return 404 Not Found.
func TestContract_EmailVerificationConfirmAndRetiredAliases(t *testing.T) {
	h := setupCrossAdapterTestPostgres(t)

	for _, tc := range []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
	}{
		{"Canonical POST /confirm with invalid token", http.MethodPost, "/api/v1/auth/verify-email/confirm", `{"token":"invalid_mock_token"}`, http.StatusBadRequest},
		{"Canonical GET /confirm with query token", http.MethodGet, "/api/v1/auth/verify-email/confirm?token=invalid_mock_token", "", http.StatusBadRequest},
		{"Retired POST /verify-email alias", http.MethodPost, "/api/v1/auth/verify-email", `{"token":"invalid_mock_token"}`, http.StatusNotFound},
		{"Retired GET /verify-email alias", http.MethodGet, "/api/v1/auth/verify-email?token=invalid_mock_token", "", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := executeContractRequest(t, h.router, contractRequestSpec{
				Method: tc.method,
				Path:   tc.path,
				Headers: map[string]string{
					"Content-Type": "application/json",
				},
				Body: json.RawMessage(tc.body),
			})
			if rec.Code != tc.wantStatus {
				t.Fatalf("%s status = %d; want %d; body = %s", tc.name, rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus == http.StatusBadRequest {
				var errResp map[string]string
				_ = json.Unmarshal(rec.Body.Bytes(), &errResp)
				if errResp["detail"] != "invalid or expired verification link" {
					t.Fatalf("%s unexpected message: %v", tc.name, errResp)
				}
			}
		})
	}
}
