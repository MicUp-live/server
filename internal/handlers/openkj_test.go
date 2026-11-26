package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

func TestOpenKJHandler_ClearRequestsReportsWriteFailure(t *testing.T) {
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	qStore.events = evStore
	key := models.GenerateOpenKJKey("event-1")
	event := &models.Event{ID: "event-1", OwnerID: "host-1", Status: models.EventStatusActive, OpenKJKey: key, QueueVersion: 1}
	if err := evStore.CreateEvent(context.Background(), event); err != nil {
		t.Fatalf("create event: %v", err)
	}
	req := &models.SongRequest{ID: "request-1", EventID: event.ID, Status: models.StatusAccepted}
	if err := qStore.CreateRequest(context.Background(), req); err != nil {
		t.Fatalf("create request: %v", err)
	}
	qStore.failUpdate = fmt.Errorf("injected write failure")
	h := &OpenKJHandler{Events: evStore, Requests: qStore}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/openkj/api.php?action=clearrequests&key="+key, nil)
	w := httptest.NewRecorder()
	h.HandleAction(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("failed clear = %d: %s; want 500", w.Code, w.Body.String())
	}
	stored, err := qStore.GetRequest(context.Background(), req.ID)
	if err != nil || stored.Status != models.StatusAccepted {
		t.Fatalf("request after failed clear = %v, error = %v; want accepted", stored, err)
	}
	updated, err := evStore.GetEvent(context.Background(), event.ID)
	if err != nil || updated.QueueVersion != 1 {
		t.Fatalf("event after failed clear = %v, error = %v; want version 1", updated, err)
	}
}

func TestOpenKJHandler_CompleteProtocol(t *testing.T) {
	sbStore := newTestStore()
	evStore := newTestEventStore()
	qStore := newTestQueueStore()

	openkj := &OpenKJHandler{
		Events:         evStore,
		Songbooks:      sbStore,
		CatalogChanges: sbStore,
		Requests:       qStore,
	}

	// 1. Setup mock songbook and active event with an explicit OpenKJ integration key
	sb := &models.Songbook{ID: "sb-1", Name: "Host Catalog", OwnerID: "host-1"}
	_ = sbStore.CreateSongbook(context.Background(), sb)

	key := models.GenerateOpenKJKey("ev-openkj-1")
	ev := &models.Event{
		ID:           "ev-openkj-1",
		OwnerID:      "host-1",
		SongbookID:   "sb-1",
		VenueName:    "OpenKJ Lounge",
		Name:         "Live OpenKJ Show",
		JoinCode:     "OPENKJ",
		Status:       models.EventStatusActive,
		OpenKJKey:    key,
		QueueVersion: 1,
	}
	_ = evStore.CreateEvent(context.Background(), ev)

	// Test action=getVenues (pkircher29/OpenKJ envelope)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/openkj/api.php?action=getVenues&key="+key, nil)
	w := httptest.NewRecorder()
	openkj.HandleAction(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("getVenues failed: %d", w.Code)
	}
	var venuesResp struct {
		Command string                   `json:"command"`
		Error   bool                     `json:"error"`
		Venues  []map[string]interface{} `json:"venues"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &venuesResp); err != nil {
		t.Fatalf("decode getVenues: %v: %s", err, w.Body.String())
	}
	if venuesResp.Command != "getVenues" || venuesResp.Error || len(venuesResp.Venues) != 1 || venuesResp.Venues[0]["name"] != "OpenKJ Lounge" || venuesResp.Venues[0]["accepting"] != true {
		t.Fatalf("unexpected venues response: %+v", venuesResp)
	}

	// Test action=getSerial
	req = httptest.NewRequest(http.MethodGet, "/api/v1/openkj/api.php?action=getSerial&key="+key, nil)
	w = httptest.NewRecorder()
	openkj.HandleAction(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("getSerial failed: %d", w.Code)
	}
	var serialRes map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &serialRes)
	if serialRes["command"] != "getSerial" || serialRes["error"] != false || serialRes["serial"] == nil {
		t.Fatalf("unexpected getSerial response: %+v", serialRes)
	}

	// Test action=getAlert and getEntitledSystemCount (pkircher29/OpenKJ commands)
	for _, tc := range []struct {
		cmd   string
		field string
	}{
		{"getAlert", "alert"},
		{"getEntitledSystemCount", "count"},
	} {
		req = httptest.NewRequest(http.MethodGet, "/api/v1/openkj/api.php?action="+tc.cmd+"&key="+key, nil)
		w = httptest.NewRecorder()
		openkj.HandleAction(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s failed: %d", tc.cmd, w.Code)
		}
		var res map[string]interface{}
		_ = json.Unmarshal(w.Body.Bytes(), &res)
		if res["command"] != tc.cmd || res["error"] != false || res[tc.field] == nil {
			t.Fatalf("unexpected %s response: %+v", tc.cmd, res)
		}
	}

	// Test action=addSongs (must be POST, writes to CatalogChanges)
	songsPayload, _ := json.Marshal([]OpenKJSong{
		{Artist: "Queen", Title: "Bohemian Rhapsody", SongID: "SC8100"},
		{Artist: "Journey", Title: "Don't Stop Believin'", SongID: "CB100"},
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/openkj/api.php?action=addSongs&key="+key, bytes.NewReader(songsPayload))
	w = httptest.NewRecorder()
	openkj.HandleAction(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("addSongs failed: %d: %s", w.Code, w.Body.String())
	}
	var addRes map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &addRes)
	if addRes["command"] != "addSongs" || addRes["error"] != false || int(addRes["added"].(float64)) != 2 {
		t.Fatalf("unexpected addSongs response: %+v", addRes)
	}

	// Verify songs are present in CatalogChanges
	catalogChanges, _ := sbStore.ListCatalogChanges(context.Background(), "sb-1")
	if len(catalogChanges) != 2 {
		t.Fatalf("expected 2 songs in catalog changes, found %d", len(catalogChanges))
	}

	// Test action=setAccepting (pause requests)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/openkj/api.php?action=setAccepting&key="+key+"&accepting=0", nil)
	w = httptest.NewRecorder()
	openkj.HandleAction(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("setAccepting failed: %d", w.Code)
	}
	updatedEv, _ := evStore.GetEvent(context.Background(), "ev-openkj-1")
	if updatedEv.Status != models.EventStatusPaused || updatedEv.QueueVersion != 2 {
		t.Fatalf("expected event to be paused with queue version 2, got %s, version %d", updatedEv.Status, updatedEv.QueueVersion)
	}

	// Test action=setAccepting (resume requests)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/openkj/api.php?action=setAccepting&key="+key+"&accepting=1", nil)
	w = httptest.NewRecorder()
	openkj.HandleAction(w, req)
	updatedEv, _ = evStore.GetEvent(context.Background(), "ev-openkj-1")
	if updatedEv.Status != models.EventStatusActive || updatedEv.QueueVersion != 3 {
		t.Fatalf("expected event to be active with queue version 3, got %s, version %d", updatedEv.Status, updatedEv.QueueVersion)
	}

	// 2. Add sample singer request to event queue
	songReq := &models.SongRequest{
		ID:         "req-openkj-1",
		EventID:    "ev-openkj-1",
		SongbookID: "sb-1",
		SingerName: "Sam",
		SongTitle:  "Bohemian Rhapsody",
		SongArtist: "Queen",
		KeyChange:  -1,
		Notes:      "Duet with Alex",
		Status:     models.StatusPending,
		Rank:       1,
	}
	_ = qStore.CreateRequest(context.Background(), songReq)

	// Test action=getRequests
	req = httptest.NewRequest(http.MethodGet, "/api/v1/openkj/api.php?action=getRequests&key="+key, nil)
	w = httptest.NewRecorder()
	openkj.HandleAction(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("getRequests failed: %d: %s", w.Code, w.Body.String())
	}
	var reqsResp struct {
		Command  string             `json:"command"`
		Error    bool               `json:"error"`
		Requests []OpenKJRequestDTO `json:"requests"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &reqsResp); err != nil {
		t.Fatalf("decode getRequests: %v: %s", err, w.Body.String())
	}
	if reqsResp.Command != "getRequests" || reqsResp.Error || len(reqsResp.Requests) != 1 || reqsResp.Requests[0].Singer != "Sam" || reqsResp.Requests[0].Key != -1 || reqsResp.Requests[0].RequestID <= 0 {
		t.Fatalf("unexpected getRequests output: %+v", reqsResp)
	}

	// Test action=deleteRequest using OpenKJ's numeric request_id
	deleteBody := fmt.Sprintf(`{"command":"deleteRequest","api_key":%q,"request_id":%d}`, key, reqsResp.Requests[0].RequestID)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/openkj/api.php", bytes.NewBufferString(deleteBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	openkj.HandleAction(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("deleteRequest failed: %d: %s", w.Code, w.Body.String())
	}

	// Verify request is completed and no longer in pending getRequests
	req = httptest.NewRequest(http.MethodGet, "/api/v1/openkj/api.php?action=getRequests&key="+key, nil)
	w = httptest.NewRecorder()
	openkj.HandleAction(w, req)
	reqsResp.Requests = nil
	_ = json.Unmarshal(w.Body.Bytes(), &reqsResp)
	if len(reqsResp.Requests) != 0 {
		t.Fatalf("expected 0 pending requests after delete, found %d", len(reqsResp.Requests))
	}

	// Test action=clearDatabase (writes Clear: true tombstone to CatalogChanges)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/openkj/api.php?action=clearDatabase&key="+key, nil)
	w = httptest.NewRecorder()
	openkj.HandleAction(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("clearDatabase failed: %d: %s", w.Code, w.Body.String())
	}
	catalogChanges, _ = sbStore.ListCatalogChanges(context.Background(), "sb-1")
	if len(models.ApplyCatalogChanges(nil, catalogChanges)) != 0 {
		t.Fatalf("expected 0 songs after clearDatabase, got %d", len(models.ApplyCatalogChanges(nil, catalogChanges)))
	}
}

func TestOpenKJHandler_RejectsPublicIdentifiersAndInvalidKeys(t *testing.T) {
	sbStore := newTestStore()
	evStore := newTestEventStore()
	qStore := newTestQueueStore()

	openkj := &OpenKJHandler{
		Events:         evStore,
		Songbooks:      sbStore,
		CatalogChanges: sbStore,
		Requests:       qStore,
	}

	realKey := models.GenerateOpenKJKey("ev-secret-uuid-12345")
	ev := &models.Event{
		ID:         "ev-secret-uuid-12345",
		OwnerID:    "host-1",
		SongbookID: "sb-1",
		VenueName:  "Test Bar",
		Name:       "Public Show",
		JoinCode:   "PUBLIC123",
		Status:     models.EventStatusActive,
		OpenKJKey:  realKey,
	}
	_ = evStore.CreateEvent(context.Background(), ev)

	tests := []struct {
		name string
		key  string
	}{
		{"Public Join Code", "PUBLIC123"},
		{"Raw Event UUID", "ev-secret-uuid-12345"},
		{"Empty Key", ""},
		{"Wrong Secret in Format", "okj_ev-secret-uuid-12345_badsecret0000000000000000"},
		{"Arbitrary String", "random-attempt"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/openkj/api.php?action=clearDatabase&key="+tc.key, nil)
			w := httptest.NewRecorder()
			openkj.HandleAction(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401 Unauthorized for key %q, got %d", tc.key, w.Code)
			}
		})
	}
}

func TestOpenKJHandler_EnforcesPOSTForMutations(t *testing.T) {
	sbStore := newTestStore()
	evStore := newTestEventStore()
	qStore := newTestQueueStore()

	openkj := &OpenKJHandler{
		Events:         evStore,
		Songbooks:      sbStore,
		CatalogChanges: sbStore,
		Requests:       qStore,
	}

	key := models.GenerateOpenKJKey("ev-post-test")
	ev := &models.Event{
		ID:         "ev-post-test",
		OwnerID:    "host-1",
		SongbookID: "sb-1",
		Name:       "Show",
		Status:     models.EventStatusActive,
		OpenKJKey:  key,
	}
	_ = evStore.CreateEvent(context.Background(), ev)

	mutations := []string{"cleardatabase", "addsongs", "setaccepting", "deleterequest", "clearrequests"}
	for _, action := range mutations {
		t.Run(action+" via GET", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/openkj?action="+action+"&key="+key, nil)
			w := httptest.NewRecorder()
			openkj.HandleAction(w, req)
			if w.Code != http.StatusMethodNotAllowed {
				t.Fatalf("expected 405 Method Not Allowed for GET %s, got %d", action, w.Code)
			}
		})
	}
}

func TestOpenKJHandler_DeleteRequestScoping(t *testing.T) {
	sbStore := newTestStore()
	evStore := newTestEventStore()
	qStore := newTestQueueStore()

	openkj := &OpenKJHandler{
		Events:         evStore,
		Songbooks:      sbStore,
		CatalogChanges: sbStore,
		Requests:       qStore,
	}

	keyA := models.GenerateOpenKJKey("event-A")
	evA := &models.Event{
		ID:        "event-A",
		OwnerID:   "host-A",
		Status:    models.EventStatusActive,
		OpenKJKey: keyA,
	}
	_ = evStore.CreateEvent(context.Background(), evA)

	keyB := models.GenerateOpenKJKey("event-B")
	evB := &models.Event{
		ID:        "event-B",
		OwnerID:   "host-B",
		Status:    models.EventStatusActive,
		OpenKJKey: keyB,
	}
	_ = evStore.CreateEvent(context.Background(), evB)

	// Create request for event B
	reqB := &models.SongRequest{
		ID:         "req-belonging-to-B",
		EventID:    "event-B",
		SingerName: "Bob",
		SongTitle:  "Yesterday",
		Status:     models.StatusPending,
	}
	_ = qStore.CreateRequest(context.Background(), reqB)

	// 1. Event A attempts to delete Event B's request -> must be rejected with 403 Forbidden
	req := httptest.NewRequest(http.MethodPost, "/api/v1/openkj?action=deleterequest&key="+keyA+"&id=req-belonging-to-B", nil)
	w := httptest.NewRecorder()
	openkj.HandleAction(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden when deleting cross-event request, got %d", w.Code)
	}

	// 2. Request B should still be pending
	storedReq, _ := qStore.GetRequest(context.Background(), "req-belonging-to-B")
	if storedReq.Status != models.StatusPending {
		t.Fatalf("expected request status to remain pending, got %s", storedReq.Status)
	}

	// 3. Event B deletes its own request -> succeeds
	req = httptest.NewRequest(http.MethodPost, "/api/v1/openkj?action=deleterequest&key="+keyB+"&id=req-belonging-to-B", nil)
	w = httptest.NewRecorder()
	openkj.HandleAction(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK when deleting own request, got %d", w.Code)
	}

	storedReq, _ = qStore.GetRequest(context.Background(), "req-belonging-to-B")
	if storedReq.Status != models.StatusCompleted {
		t.Fatalf("expected request status to be completed, got %s", storedReq.Status)
	}
}

func TestEventHandler_RotateOpenKJKey(t *testing.T) {
	evStore := newTestEventStore()
	handler := &EventHandler{
		Events: evStore,
	}
	openkj := &OpenKJHandler{
		Events: evStore,
	}

	origKey := models.GenerateOpenKJKey("ev-rot-1")
	ev := &models.Event{
		ID:        "ev-rot-1",
		OwnerID:   "host-1",
		Name:      "Rotating Show",
		Status:    models.EventStatusActive,
		OpenKJKey: origKey,
	}
	_ = evStore.CreateEvent(context.Background(), ev)

	// Verify original key authenticates
	req := httptest.NewRequest(http.MethodGet, "/api/v1/openkj?action=getvenues&key="+origKey, nil)
	w := httptest.NewRecorder()
	openkj.HandleAction(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected original key to authenticate, got %d", w.Code)
	}

	// Rotate key
	rotateReq := httptest.NewRequest(http.MethodPost, "/api/v1/events/ev-rot-1/openkj-key/rotate", nil)
	rotateReq.SetPathValue("id", "ev-rot-1")
	rotateW := httptest.NewRecorder()
	handler.RotateOpenKJKey(rotateW, rotateReq)

	if rotateW.Code != http.StatusOK {
		t.Fatalf("rotate failed: %d: %s", rotateW.Code, rotateW.Body.String())
	}

	var rotateRes struct {
		Status    string `json:"status"`
		OpenKJKey string `json:"openkj_key"`
	}
	_ = json.Unmarshal(rotateW.Body.Bytes(), &rotateRes)
	newKey := rotateRes.OpenKJKey
	if newKey == "" || newKey == origKey {
		t.Fatalf("expected fresh distinct OpenKJKey, got %s", newKey)
	}

	// Verify old key now fails with 401 Unauthorized
	req = httptest.NewRequest(http.MethodGet, "/api/v1/openkj?action=getvenues&key="+origKey, nil)
	w = httptest.NewRecorder()
	openkj.HandleAction(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected old key to be rejected with 401, got %d", w.Code)
	}

	// Verify new key succeeds
	req = httptest.NewRequest(http.MethodGet, "/api/v1/openkj?action=getvenues&key="+newKey, nil)
	w = httptest.NewRecorder()
	openkj.HandleAction(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected new key to authenticate, got %d", w.Code)
	}
}

func TestOpenKJHandler_PostBodyJSONAndForm(t *testing.T) {
	sbStore := newTestStore()
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	openkj := &OpenKJHandler{Events: evStore, Songbooks: sbStore, CatalogChanges: sbStore, Requests: qStore}

	_ = sbStore.CreateSongbook(context.Background(), &models.Songbook{ID: "sb-body", Name: "Book", OwnerID: "host-1"})
	key := models.GenerateOpenKJKey("ev-body-1")
	_ = evStore.CreateEvent(context.Background(), &models.Event{
		ID: "ev-body-1", OwnerID: "host-1", SongbookID: "sb-body", VenueName: "Duplex", Status: models.EventStatusActive, OpenKJKey: key, QueueVersion: 1,
	})
	_ = qStore.CreateRequest(context.Background(), &models.SongRequest{
		ID: "req-body-1", EventID: "ev-body-1", SingerName: "Alex", SongTitle: "Dreams", Status: models.StatusAccepted,
	})

	// 1. JSON POST with command + api_key (standard OpenKJ v2 payload)
	for _, tc := range []struct {
		name string
		body string
	}{
		{"getVenues", fmt.Sprintf(`{"command":"getVenues","api_key":%q}`, key)},
		{"get_requests_snake", fmt.Sprintf(`{"command":"get_requests","api_key":%q}`, key)},
		{"getSerial", fmt.Sprintf(`{"command":"getSerial","api_key":%q}`, key)},
		{"getRequests", fmt.Sprintf(`{"command":"getRequests","api_key":%q}`, key)},
		{"addSongs", fmt.Sprintf(`{"command":"addSongs","api_key":%q,"songs":[{"artist":"Queen","title":"Bohemian Rhapsody"}]}`, key)},
		{"setAccepting_bool", fmt.Sprintf(`{"command":"setAccepting","api_key":%q,"accepting":false}`, key)},
		{"setAccepting_num", fmt.Sprintf(`{"command":"setAccepting","api_key":%q,"accepting":1,"venue_id":42}`, key)},
		{"setAccepting_str", fmt.Sprintf(`{"command":"set_accepting","api_key":%q,"accepting":"0"}`, key)},
		{"deleteRequest", fmt.Sprintf(`{"command":"deleteRequest","api_key":%q,"request_id":"req-body-1"}`, key)},
		{"clearRequests", fmt.Sprintf(`{"command":"clearRequests","api_key":%q}`, key)},
		{"clearDatabase", fmt.Sprintf(`{"command":"clearDatabase","api_key":%q}`, key)},
	} {
		t.Run("json_"+tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/openkj", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			openkj.HandleAction(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("%s status = %d: %s", tc.name, w.Code, w.Body.String())
			}
		})
	}

	// 2. Form-urlencoded POST with action + key in body
	formBody := "action=setAccepting&key=" + key + "&accepting=1"
	formReq := httptest.NewRequest(http.MethodPost, "/api/v1/openkj", bytes.NewBufferString(formBody))
	formReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	formW := httptest.NewRecorder()
	openkj.HandleAction(formW, formReq)
	if formW.Code != http.StatusOK {
		t.Fatalf("form setAccepting status = %d: %s", formW.Code, formW.Body.String())
	}
	ev, _ := evStore.GetEvent(context.Background(), "ev-body-1")
	if ev.Status != models.EventStatusActive {
		t.Fatalf("expected event resumed to active, got %s", ev.Status)
	}

	// 3. Form POST without Content-Type header
	noCTReq := httptest.NewRequest(http.MethodPost, "/api/v1/openkj", bytes.NewBufferString("action=setAccepting&key="+key+"&accepting=0"))
	noCTW := httptest.NewRecorder()
	openkj.HandleAction(noCTW, noCTReq)
	if noCTW.Code != http.StatusOK {
		t.Fatalf("no-CT form setAccepting status = %d: %s", noCTW.Code, noCTW.Body.String())
	}
	ev, _ = evStore.GetEvent(context.Background(), "ev-body-1")
	if ev.Status != models.EventStatusPaused {
		t.Fatalf("expected event paused, got %s", ev.Status)
	}
}

func TestOpenKJHandler_ClearDatabaseFollowedByAddSongs(t *testing.T) {
	sbStore := newTestStore()
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	openkj := &OpenKJHandler{Events: evStore, Songbooks: sbStore, CatalogChanges: sbStore, Requests: qStore}

	_ = sbStore.CreateSongbook(context.Background(), &models.Songbook{ID: "sb-sync", Name: "Sync Book", OwnerID: "host-1"})
	key := models.GenerateOpenKJKey("ev-sync-1")
	_ = evStore.CreateEvent(context.Background(), &models.Event{
		ID: "ev-sync-1", OwnerID: "host-1", SongbookID: "sb-sync", Status: models.EventStatusActive, OpenKJKey: key,
	})

	// Seed pre-clear song
	prePayload, _ := json.Marshal([]OpenKJSong{{Artist: "Old Band", Title: "Old Track", SongID: "OLD-1"}})
	w := httptest.NewRecorder()
	openkj.HandleAction(w, httptest.NewRequest(http.MethodPost, "/api/v1/openkj/api.php?action=addSongs&key="+key, bytes.NewReader(prePayload)))
	if w.Code != http.StatusOK {
		t.Fatalf("initial addSongs failed: %d: %s", w.Code, w.Body.String())
	}

	// OpenKJ full sync: clearDatabase followed by addSongs
	w = httptest.NewRecorder()
	openkj.HandleAction(w, httptest.NewRequest(http.MethodPost, "/api/v1/openkj/api.php?action=clearDatabase&key="+key, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("clearDatabase failed: %d: %s", w.Code, w.Body.String())
	}

	postPayload, _ := json.Marshal([]OpenKJSong{{Artist: "New Band", Title: "New Track", SongID: "NEW-1"}})
	w = httptest.NewRecorder()
	openkj.HandleAction(w, httptest.NewRequest(http.MethodPost, "/api/v1/openkj/api.php?action=addSongs&key="+key, bytes.NewReader(postPayload)))
	if w.Code != http.StatusOK {
		t.Fatalf("post-clear addSongs failed: %d: %s", w.Code, w.Body.String())
	}

	changes, err := sbStore.ListCatalogChanges(context.Background(), "sb-sync")
	if err != nil {
		t.Fatalf("list catalog changes: %v", err)
	}
	effective := models.ApplyCatalogChanges(nil, changes)
	if len(effective) != 1 {
		t.Fatalf("expected 1 post-clear song, got %d: %+v", len(effective), effective)
	}
	for _, s := range effective {
		if s.Title != "New Track" {
			t.Fatalf("expected post-clear song New Track, got %q", s.Title)
		}
	}
}

type failingCatalogChangeStore struct {
	store.CatalogChangeStore
	putErr error
}

func (f *failingCatalogChangeStore) PutCatalogChange(_ context.Context, _ *models.CatalogChange) error {
	return f.putErr
}

type failingRequestStore struct {
	store.RequestStore
	getErr  error
	listErr error
}

func (f *failingRequestStore) GetRequest(_ context.Context, _ string) (*models.SongRequest, error) {
	return nil, f.getErr
}

func (f *failingRequestStore) ListRequestsByEvent(_ context.Context, _ string) ([]*models.SongRequest, error) {
	return nil, f.listErr
}

func TestOpenKJHandler_NumericDeleteRequestScopingAndErrorPropagation(t *testing.T) {
	evStore := newTestEventStore()
	qStore := newTestQueueStore()
	openkj := &OpenKJHandler{Events: evStore, Requests: qStore}

	keyA := models.GenerateOpenKJKey("ev-num-A")
	keyB := models.GenerateOpenKJKey("ev-num-B")
	_ = evStore.CreateEvent(context.Background(), &models.Event{ID: "ev-num-A", OwnerID: "host-A", Status: models.EventStatusActive, OpenKJKey: keyA})
	_ = evStore.CreateEvent(context.Background(), &models.Event{ID: "ev-num-B", OwnerID: "host-B", Status: models.EventStatusActive, OpenKJKey: keyB})
	_ = qStore.CreateRequest(context.Background(), &models.SongRequest{ID: "req-num-B", EventID: "ev-num-B", SingerName: "Bob", SongTitle: "Song B", Status: models.StatusPending})

	numIDB := openKJRequestID("req-num-B")
	body := fmt.Sprintf(`{"command":"deleteRequest","api_key":%q,"request_id":%d}`, keyA, numIDB)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/openkj/api.php", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	openkj.HandleAction(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-event numeric deleteRequest status = %d; want 404", w.Code)
	}
	stored, _ := qStore.GetRequest(context.Background(), "req-num-B")
	if stored.Status != models.StatusPending {
		t.Fatalf("event B request was mutated by event A numeric delete: %s", stored.Status)
	}

	// Non-NotExist storage error from GetRequest must return 500, not 404
	openkjErrGet := &OpenKJHandler{
		Events:   evStore,
		Requests: &failingRequestStore{getErr: fmt.Errorf("dynamodb throttled")},
	}
	w = httptest.NewRecorder()
	openkjErrGet.HandleAction(w, httptest.NewRequest(http.MethodPost, "/api/v1/openkj/api.php?action=deleteRequest&key="+keyA+"&id=12345", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("GetRequest storage error status = %d; want 500", w.Code)
	}

	// Storage error from ListRequestsByEvent during numeric fallback must return 500, not 404
	openkjErrList := &OpenKJHandler{
		Events:   evStore,
		Requests: &failingRequestStore{getErr: fmt.Errorf("missing: %w", os.ErrNotExist), listErr: fmt.Errorf("dynamodb throttled")},
	}
	w = httptest.NewRecorder()
	openkjErrList.HandleAction(w, httptest.NewRequest(http.MethodPost, "/api/v1/openkj/api.php?action=deleteRequest&key="+keyA+"&id=12345", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("ListRequestsByEvent storage error status = %d; want 500", w.Code)
	}
}

func TestOpenKJHandler_CatalogWriteErrorMapping(t *testing.T) {
	sbStore := newTestStore()
	evStore := newTestEventStore()
	_ = sbStore.CreateSongbook(context.Background(), &models.Songbook{ID: "sb-err", Name: "Book", OwnerID: "host-1"})
	key := models.GenerateOpenKJKey("ev-err-1")
	_ = evStore.CreateEvent(context.Background(), &models.Event{
		ID: "ev-err-1", OwnerID: "host-1", SongbookID: "sb-err", Status: models.EventStatusActive, OpenKJKey: key,
	})

	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"deleting songbook", store.ErrSongbookDeleting, http.StatusConflict},
		{"permission denied", store.ErrPermissionDenied, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			openkj := &OpenKJHandler{
				Events:         evStore,
				Songbooks:      sbStore,
				CatalogChanges: &failingCatalogChangeStore{putErr: tc.err},
			}
			w := httptest.NewRecorder()
			openkj.HandleAction(w, httptest.NewRequest(http.MethodPost, "/api/v1/openkj/api.php?action=clearDatabase&key="+key, nil))
			if w.Code != tc.wantStatus {
				t.Fatalf("clearDatabase %s status = %d; want %d", tc.name, w.Code, tc.wantStatus)
			}

			payload, _ := json.Marshal([]OpenKJSong{{Artist: "Queen", Title: "Radio Ga Ga"}})
			w = httptest.NewRecorder()
			openkj.HandleAction(w, httptest.NewRequest(http.MethodPost, "/api/v1/openkj/api.php?action=addSongs&key="+key, bytes.NewReader(payload)))
			if w.Code != tc.wantStatus {
				t.Fatalf("addSongs %s status = %d; want %d", tc.name, w.Code, tc.wantStatus)
			}
		})
	}
}
