package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"micup.live/server/internal/models"
)

func TestRouterShowPolicies_SongReplacementAndAbsentDeferral(t *testing.T) {
	h := setupCrossAdapterTestPostgres(t)

	// 1. CORS preflight must allow X-Edit-Token for pre-call song replacement.
	preflight := httptest.NewRequest(http.MethodOptions, "/api/v1/events/"+h.eventA.ID+"/requests/req-1/song", nil)
	preflight.Header.Set("Origin", "https://micup.live")
	preflight.Header.Set("Access-Control-Request-Method", "PATCH")
	preflight.Header.Set("Access-Control-Request-Headers", "X-Edit-Token, Content-Type")
	preflightRec := httptest.NewRecorder()
	newRouterForTest(t).ServeHTTP(preflightRec, preflight)
	if preflightRec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d; want 204", preflightRec.Code)
	}
	if !strings.Contains(preflightRec.Header().Get("Access-Control-Allow-Headers"), "X-Edit-Token") {
		t.Fatalf("CORS Allow-Headers missing X-Edit-Token: %q", preflightRec.Header().Get("Access-Control-Allow-Headers"))
	}

	// 2. Host creates a new show without specifying allow_song_replacement -> defaults to true.
	createEventBody, _ := json.Marshal(map[string]any{
		"songbook_id":           h.songbookA.ID,
		"name":                  "HP1 Live Show",
		"venue_name":            "The Taproom",
		"rotation_mode":         "fair_priority",
		"auto_accept":           true,
		"allow_key_change":      true,
		"max_active_per_singer": 2,
	})
	createEvReq := httptest.NewRequest(http.MethodPost, "/api/v1/events", bytes.NewReader(createEventBody))
	createEvReq.Header.Set("Authorization", "Bearer "+h.sessionTokenA)
	createEvReq.Header.Set("Content-Type", "application/json")
	createEvRec := httptest.NewRecorder()
	h.router.ServeHTTP(createEvRec, createEvReq)
	if createEvRec.Code != http.StatusCreated {
		t.Fatalf("create event status = %d body = %s", createEvRec.Code, createEvRec.Body.String())
	}
	var ev models.Event
	if err := json.Unmarshal(createEvRec.Body.Bytes(), &ev); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if !ev.AllowSongReplacement {
		t.Fatal("expected new show to default allow_song_replacement=true")
	}

	// 3. Submit 5 requests so we can exercise both pre-call song replacement and 3-spot deferral.
	submit := func(singer, title, artist string) models.SongRequest {
		t.Helper()
		body, _ := json.Marshal(map[string]any{
			"singer_name": singer,
			"song_title":  title,
			"song_artist": artist,
			"key_change":  0,
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/events/"+ev.ID+"/requests", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.router.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("submit %s status = %d body = %s", singer, rec.Code, rec.Body.String())
		}
		var created models.SongRequest
		if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
			t.Fatalf("decode created request: %v", err)
		}
		if created.CancelToken == "" || created.EditToken == "" {
			t.Fatalf("expected both cancel_token and edit_token on created request, got %+v", created)
		}
		return created
	}

	r1 := submit("Alice", "Original Song", "Orig Artist")
	r2 := submit("Bob", "Song Two", "Artist Two")
	r3 := submit("Cara", "Song Three", "Artist Three")
	r4 := submit("Dan", "Song Four", "Artist Four")
	r5 := submit("Eve", "Song Five", "Artist Five")

	// 4. Capability separation: cancel_token MUST NOT authorize song replacement,
	//    and edit_token MUST NOT authorize cancellation.
	swapBody, _ := json.Marshal(map[string]any{
		"song_id":     "song-bohemian",
		"song_title":  "Bohemian Rhapsody",
		"song_artist": "Queen",
		"key_change":  -1,
		"notes":       "Swapped before call",
	})
	badCancelSwap := httptest.NewRequest(http.MethodPatch, "/api/v1/events/"+ev.ID+"/requests/"+r1.ID+"/song", bytes.NewReader(swapBody))
	badCancelSwap.Header.Set("Content-Type", "application/json")
	badCancelSwap.Header.Set("X-Edit-Token", r1.CancelToken)
	badCancelSwapRec := httptest.NewRecorder()
	h.router.ServeHTTP(badCancelSwapRec, badCancelSwap)
	if badCancelSwapRec.Code != http.StatusForbidden {
		t.Fatalf("cancel token used for song swap status = %d; want 403", badCancelSwapRec.Code)
	}

	badOtherEdit := httptest.NewRequest(http.MethodPatch, "/api/v1/events/"+ev.ID+"/requests/"+r1.ID+"/song", bytes.NewReader(swapBody))
	badOtherEdit.Header.Set("Content-Type", "application/json")
	badOtherEdit.Header.Set("X-Edit-Token", r2.EditToken)
	badOtherEditRec := httptest.NewRecorder()
	h.router.ServeHTTP(badOtherEditRec, badOtherEdit)
	if badOtherEditRec.Code != http.StatusForbidden {
		t.Fatalf("other request edit token status = %d; want 403", badOtherEditRec.Code)
	}

	badEditCancel := httptest.NewRequest(http.MethodDelete, "/api/v1/events/"+ev.ID+"/requests/"+r1.ID, nil)
	badEditCancel.Header.Set("X-Cancel-Token", r1.EditToken)
	badEditCancelRec := httptest.NewRecorder()
	h.router.ServeHTTP(badEditCancelRec, badEditCancel)
	if badEditCancelRec.Code != http.StatusForbidden {
		t.Fatalf("edit token used for delete status = %d; want 403", badEditCancelRec.Code)
	}

	// 5. Valid pre-call song replacement preserves rank, round, status, and bumps X-Queue-Version.
	goodSwap := httptest.NewRequest(http.MethodPatch, "/api/v1/events/"+ev.ID+"/requests/"+r1.ID+"/song", bytes.NewReader(swapBody))
	goodSwap.Header.Set("Content-Type", "application/json")
	goodSwap.Header.Set("X-Edit-Token", r1.EditToken)
	goodSwapRec := httptest.NewRecorder()
	h.router.ServeHTTP(goodSwapRec, goodSwap)
	if goodSwapRec.Code != http.StatusOK {
		t.Fatalf("valid song swap status = %d body = %s", goodSwapRec.Code, goodSwapRec.Body.String())
	}
	var swapped models.SongRequest
	if err := json.Unmarshal(goodSwapRec.Body.Bytes(), &swapped); err != nil {
		t.Fatalf("decode swapped request: %v", err)
	}
	if swapped.SongTitle != "Bohemian Rhapsody" || swapped.KeyChange != -1 {
		t.Fatalf("unexpected swapped fields: %+v", swapped)
	}
	if swapped.Rank != r1.Rank || swapped.Round != r1.Round || swapped.Status != r1.Status {
		t.Fatalf("song swap changed placement/status: got rank=%d round=%d status=%s; want rank=%d round=%d status=%s",
			swapped.Rank, swapped.Round, swapped.Status, r1.Rank, r1.Round, r1.Status)
	}
	if goodSwapRec.Header().Get("X-Queue-Version") == "" {
		t.Fatal("expected X-Queue-Version header on song replacement response")
	}

	// 6. Host calls Alice to stage (up_next) -> singer self-replacement is now locked (409).
	callBody, _ := json.Marshal(map[string]any{"status": "up_next"})
	callReq := httptest.NewRequest(http.MethodPatch, "/api/v1/events/"+ev.ID+"/requests/"+r1.ID, bytes.NewReader(callBody))
	callReq.Header.Set("Authorization", "Bearer "+h.sessionTokenA)
	callReq.Header.Set("Content-Type", "application/json")
	callRec := httptest.NewRecorder()
	h.router.ServeHTTP(callRec, callReq)
	if callRec.Code != http.StatusOK {
		t.Fatalf("call to stage status = %d body = %s", callRec.Code, callRec.Body.String())
	}

	lateSwap := httptest.NewRequest(http.MethodPatch, "/api/v1/events/"+ev.ID+"/requests/"+r1.ID+"/song", bytes.NewReader(swapBody))
	lateSwap.Header.Set("Content-Type", "application/json")
	lateSwap.Header.Set("X-Edit-Token", r1.EditToken)
	lateSwapRec := httptest.NewRecorder()
	h.router.ServeHTTP(lateSwapRec, lateSwap)
	if lateSwapRec.Code != http.StatusConflict {
		t.Fatalf("swap after up_next status = %d; want 409 (body=%s)", lateSwapRec.Code, lateSwapRec.Body.String())
	}

	// 7. Cross-tenant host cannot defer Host A's request.
	crossDefer := httptest.NewRequest(http.MethodPost, "/api/v1/events/"+ev.ID+"/requests/"+r1.ID+"/defer", bytes.NewReader([]byte(`{}`)))
	crossDefer.Header.Set("Authorization", "Bearer "+h.sessionTokenB)
	crossDefer.Header.Set("Content-Type", "application/json")
	crossDeferRec := httptest.NewRecorder()
	h.router.ServeHTTP(crossDeferRec, crossDefer)
	if crossDeferRec.Code != http.StatusNotFound && crossDeferRec.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant defer status = %d; want 404 or 403", crossDeferRec.Code)
	}

	// 8. Host A defers absent Alice (up_next at index 0) with Idempotency-Key.
	//    Alice should move down 3 spots (index 3, between Dan and Eve), return to accepted,
	//    and have CompletedAt == nil. Replaying the same Idempotency-Key must NOT defer twice.
	idemKey := uuid.NewString()
	doDefer := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/events/"+ev.ID+"/requests/"+r1.ID+"/defer", bytes.NewReader([]byte(`{}`)))
		req.Header.Set("Authorization", "Bearer "+h.sessionTokenA)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", idemKey)
		rec := httptest.NewRecorder()
		h.router.ServeHTTP(rec, req)
		return rec
	}

	deferRec1 := doDefer()
	if deferRec1.Code != http.StatusOK {
		t.Fatalf("defer status = %d body = %s", deferRec1.Code, deferRec1.Body.String())
	}
	var deferred models.SongRequest
	if err := json.Unmarshal(deferRec1.Body.Bytes(), &deferred); err != nil {
		t.Fatalf("decode deferred request: %v", err)
	}
	if deferred.Status != models.StatusAccepted {
		t.Fatalf("expected deferred status=accepted, got %s", deferred.Status)
	}
	if deferred.CompletedAt != nil {
		t.Fatal("expected deferred request CompletedAt=nil")
	}

	// Replay with same Idempotency-Key must return identical result without moving Alice again.
	deferRec2 := doDefer()
	if deferRec2.Code != http.StatusOK {
		t.Fatalf("defer replay status = %d body = %s", deferRec2.Code, deferRec2.Body.String())
	}

	// Verify live queue order is [r2, r3, r4, r1, r5].
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/events/"+ev.ID+"/requests", nil)
	listReq.Header.Set("Authorization", "Bearer "+h.sessionTokenA)
	listRec := httptest.NewRecorder()
	h.router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list requests status = %d", listRec.Code)
	}
	var listResp struct {
		Requests []models.SongRequest `json:"requests"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	wantIDs := []string{r2.ID, r3.ID, r4.ID, r1.ID, r5.ID}
	if len(listResp.Requests) != len(wantIDs) {
		t.Fatalf("got %d requests, want %d", len(listResp.Requests), len(wantIDs))
	}
	for i, wantID := range wantIDs {
		if listResp.Requests[i].ID != wantID {
			t.Fatalf("queue index %d: got %s, want %s", i, listResp.Requests[i].ID, wantID)
		}
	}
}
