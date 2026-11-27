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

func createPolicyTestShow(t *testing.T, h *crossAdapterHarness) models.Event {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"songbook_id":           h.songbookA.ID,
		"name":                  "HP1 Live Show",
		"venue_name":            "The Taproom",
		"rotation_mode":         "fair_priority",
		"auto_accept":           true,
		"allow_key_change":      true,
		"max_active_per_singer": 2,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+h.sessionTokenA)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create event status = %d body = %s", rec.Code, rec.Body.String())
	}
	var ev models.Event
	if err := json.Unmarshal(rec.Body.Bytes(), &ev); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if !ev.AllowSongReplacement {
		t.Fatal("expected new show to default allow_song_replacement=true")
	}
	return ev
}

func submitPolicyTestRequest(t *testing.T, h *crossAdapterHarness, eventID, singer, title, artist string) models.SongRequest {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"singer_name": singer,
		"song_title":  title,
		"song_artist": artist,
		"key_change":  0,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events/"+eventID+"/requests", bytes.NewReader(body))
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
		t.Fatalf("expected both cancel_token and edit_token, got %+v", created)
	}
	return created
}

func sendSongSwapRequest(router http.Handler, eventID, reqID, editToken string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/events/"+eventID+"/requests/"+reqID+"/song", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Edit-Token", editToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func assertHostQueueOrder(t *testing.T, h *crossAdapterHarness, eventID string, wantIDs []string) {
	t.Helper()
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/events/"+eventID+"/requests", nil)
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
	if len(listResp.Requests) != len(wantIDs) {
		t.Fatalf("got %d requests, want %d", len(listResp.Requests), len(wantIDs))
	}
	for i, wantID := range wantIDs {
		if listResp.Requests[i].ID != wantID {
			t.Fatalf("queue index %d: got %s, want %s", i, listResp.Requests[i].ID, wantID)
		}
	}
}

func TestRouterShowPolicies_SongReplacementAndAbsentDeferral(t *testing.T) {
	h := setupCrossAdapterTestPostgres(t)

	t.Run("CORS preflight allows X-Edit-Token", func(t *testing.T) {
		preflight := httptest.NewRequest(http.MethodOptions, "/api/v1/events/"+h.eventA.ID+"/requests/req-1/song", nil)
		preflight.Header.Set("Origin", "https://micup.live")
		preflight.Header.Set("Access-Control-Request-Method", "PATCH")
		preflight.Header.Set("Access-Control-Request-Headers", "X-Edit-Token, Content-Type")
		rec := httptest.NewRecorder()
		newRouterForTest(t).ServeHTTP(rec, preflight)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("preflight status = %d; want 204", rec.Code)
		}
		if !strings.Contains(rec.Header().Get("Access-Control-Allow-Headers"), "X-Edit-Token") {
			t.Fatalf("CORS Allow-Headers missing X-Edit-Token: %q", rec.Header().Get("Access-Control-Allow-Headers"))
		}
	})

	ev := createPolicyTestShow(t, h)
	r1 := submitPolicyTestRequest(t, h, ev.ID, "Alice", "Original Song", "Orig Artist")
	r2 := submitPolicyTestRequest(t, h, ev.ID, "Bob", "Song Two", "Artist Two")
	r3 := submitPolicyTestRequest(t, h, ev.ID, "Cara", "Song Three", "Artist Three")
	r4 := submitPolicyTestRequest(t, h, ev.ID, "Dan", "Song Four", "Artist Four")
	r5 := submitPolicyTestRequest(t, h, ev.ID, "Eve", "Song Five", "Artist Five")

	swapBody, _ := json.Marshal(map[string]any{
		"song_id":     "song-bohemian",
		"song_title":  "Bohemian Rhapsody",
		"song_artist": "Queen",
		"key_change":  -1,
		"notes":       "Swapped before call",
	})

	t.Run("enforces edit and cancel token capability separation", func(t *testing.T) {
		if rec := sendSongSwapRequest(h.router, ev.ID, r1.ID, r1.CancelToken, swapBody); rec.Code != http.StatusForbidden {
			t.Fatalf("cancel token used for song swap status = %d; want 403", rec.Code)
		}
		if rec := sendSongSwapRequest(h.router, ev.ID, r1.ID, r2.EditToken, swapBody); rec.Code != http.StatusForbidden {
			t.Fatalf("other request edit token status = %d; want 403", rec.Code)
		}
		badEditCancel := httptest.NewRequest(http.MethodDelete, "/api/v1/events/"+ev.ID+"/requests/"+r1.ID, nil)
		badEditCancel.Header.Set("X-Cancel-Token", r1.EditToken)
		rec := httptest.NewRecorder()
		h.router.ServeHTTP(rec, badEditCancel)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("edit token used for delete status = %d; want 403", rec.Code)
		}
	})

	t.Run("pre-call swap preserves placement and locks after up_next", func(t *testing.T) {
		rec := sendSongSwapRequest(h.router, ev.ID, r1.ID, r1.EditToken, swapBody)
		if rec.Code != http.StatusOK || rec.Header().Get("X-Queue-Version") == "" {
			t.Fatalf("valid song swap status = %d version = %q body = %s", rec.Code, rec.Header().Get("X-Queue-Version"), rec.Body.String())
		}
		var swapped models.SongRequest
		if err := json.Unmarshal(rec.Body.Bytes(), &swapped); err != nil {
			t.Fatalf("decode swapped request: %v", err)
		}
		if swapped.SongTitle != "Bohemian Rhapsody" || swapped.KeyChange != -1 || swapped.Rank != r1.Rank || swapped.Status != r1.Status {
			t.Fatalf("unexpected swapped request: %+v", swapped)
		}

		callBody, _ := json.Marshal(map[string]any{"status": "up_next"})
		callReq := httptest.NewRequest(http.MethodPatch, "/api/v1/events/"+ev.ID+"/requests/"+r1.ID, bytes.NewReader(callBody))
		callReq.Header.Set("Authorization", "Bearer "+h.sessionTokenA)
		callReq.Header.Set("Content-Type", "application/json")
		callRec := httptest.NewRecorder()
		h.router.ServeHTTP(callRec, callReq)
		if callRec.Code != http.StatusOK {
			t.Fatalf("call to stage status = %d body = %s", callRec.Code, callRec.Body.String())
		}
		if late := sendSongSwapRequest(h.router, ev.ID, r1.ID, r1.EditToken, swapBody); late.Code != http.StatusConflict {
			t.Fatalf("swap after up_next status = %d; want 409", late.Code)
		}
	})

	t.Run("defers absent singer down 3 spots idempotently and denies cross-tenant host", func(t *testing.T) {
		crossDefer := httptest.NewRequest(http.MethodPost, "/api/v1/events/"+ev.ID+"/requests/"+r1.ID+"/defer", bytes.NewReader([]byte(`{}`)))
		crossDefer.Header.Set("Authorization", "Bearer "+h.sessionTokenB)
		crossDefer.Header.Set("Content-Type", "application/json")
		crossRec := httptest.NewRecorder()
		h.router.ServeHTTP(crossRec, crossDefer)
		if crossRec.Code != http.StatusNotFound && crossRec.Code != http.StatusForbidden {
			t.Fatalf("cross-tenant defer status = %d; want 404 or 403", crossRec.Code)
		}

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

		first := doDefer()
		if first.Code != http.StatusOK {
			t.Fatalf("defer status = %d body = %s", first.Code, first.Body.String())
		}
		var deferred models.SongRequest
		if err := json.Unmarshal(first.Body.Bytes(), &deferred); err != nil {
			t.Fatalf("decode deferred: %v", err)
		}
		if deferred.Status != models.StatusAccepted || deferred.CompletedAt != nil {
			t.Fatalf("expected accepted with nil CompletedAt, got %+v", deferred)
		}

		if replay := doDefer(); replay.Code != http.StatusOK {
			t.Fatalf("defer replay status = %d body = %s", replay.Code, replay.Body.String())
		}
		assertHostQueueOrder(t, h, ev.ID, []string{r2.ID, r3.ID, r4.ID, r1.ID, r5.ID})
	})
}
