package dynamo

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"micup.live/server/internal/app"
	"micup.live/server/internal/config"
	"micup.live/server/internal/models"
)

func TestDynamoHTTPSongbookWorkflow(t *testing.T) {
	store := newLocalStore(t)
	router, err := app.NewRouter(config.Config{
		Environment:    config.Development,
		Port:           "8080",
		AuthSecret:     "test-signing-secret-with-at-least-32-bytes",
		AllowedOrigins: []string{"http://localhost:3000"},
	}, app.Dependencies{Auth: store, Songbooks: store, CatalogChanges: store})
	if err != nil {
		t.Fatalf("create router: %v", err)
	}

	registered := requestJSON(t, router, http.MethodPost, "/api/v1/auth/register", map[string]interface{}{
		"email": "workflow@example.com", "password": "secure-password", "display_name": "Workflow Host",
	}, "")
	if registered.Code != http.StatusCreated {
		t.Fatalf("register status = %d: %s", registered.Code, registered.Body.String())
	}
	var tokens struct {
		AccessToken string `json:"access_token"`
	}
	decodeResponse(t, registered, &tokens)
	if tokens.AccessToken == "" {
		t.Fatal("registration did not return an access token")
	}

	created := requestJSON(t, router, http.MethodPost, "/api/v1/songbooks", map[string]interface{}{
		"name": "Workflow Book",
	}, tokens.AccessToken)
	if created.Code != http.StatusCreated {
		t.Fatalf("create songbook status = %d: %s", created.Code, created.Body.String())
	}
	var songbook models.Songbook
	decodeResponse(t, created, &songbook)
	if songbook.ID == "" || songbook.PublicID == "" {
		t.Fatalf("created songbook is missing IDs: %+v", songbook)
	}

	manualSong := requestJSON(t, router, http.MethodPost, "/api/v1/songbooks/"+songbook.ID+"/songs", map[string]interface{}{
		"artist": "Queen", "title": "Somebody to Love",
	}, tokens.AccessToken)
	if manualSong.Code != http.StatusCreated {
		t.Fatalf("create manual song status = %d: %s", manualSong.Code, manualSong.Body.String())
	}

	listed := requestJSON(t, router, http.MethodGet, "/api/v1/songbooks/"+songbook.ID+"/songs", nil, tokens.AccessToken)
	if listed.Code != http.StatusOK {
		t.Fatalf("list songs status = %d: %s", listed.Code, listed.Body.String())
	}

	published := requestJSON(t, router, http.MethodPatch, "/api/v1/songbooks/"+songbook.ID, map[string]interface{}{
		"is_public": true,
	}, tokens.AccessToken)
	if published.Code != http.StatusOK {
		t.Fatalf("publish status = %d: %s", published.Code, published.Body.String())
	}

	unpublished := requestJSON(t, router, http.MethodPatch, "/api/v1/songbooks/"+songbook.ID, map[string]interface{}{
		"is_public": false,
	}, tokens.AccessToken)
	if unpublished.Code != http.StatusOK {
		t.Fatalf("unpublish status = %d: %s", unpublished.Code, unpublished.Body.String())
	}
}

func requestJSON(t *testing.T, handler http.Handler, method, path string, body interface{}, accessToken string) *httptest.ResponseRecorder {
	t.Helper()
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatalf("encode request: %v", err)
		}
	}
	request := httptest.NewRequest(method, path, &payload)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if accessToken != "" {
		request.Header.Set("Authorization", "Bearer "+accessToken)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, destination interface{}) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
