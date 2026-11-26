package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"micup.live/server/internal/config"
	"micup.live/server/internal/handlers"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type keyRouteAuthStore struct {
	store.AuthStore
	users map[string]*models.User
}

func (s *keyRouteAuthStore) GetUser(_ context.Context, id string) (*models.User, error) {
	return s.users[id], nil
}

func (s *keyRouteAuthStore) CreateAuthSession(_ context.Context, _ *models.AuthSession) error {
	return nil
}

type keyRouteStore struct {
	store.DeveloperKeyStore
	keys  map[string]*models.DeveloperKey
	calls int
}

func (s *keyRouteStore) CreateDeveloperKey(_ context.Context, raw string, key *models.DeveloperKey) error {
	s.calls++
	key.ID = "created-key"
	s.keys[raw] = key
	return nil
}

func (s *keyRouteStore) GetDeveloperKey(_ context.Context, raw string) (*models.DeveloperKey, error) {
	return s.keys[raw], nil
}

func (s *keyRouteStore) ListDeveloperKeys(_ context.Context, owner string) ([]*models.DeveloperKey, error) {
	s.calls++
	var keys []*models.DeveloperKey
	for _, key := range s.keys {
		if key.OwnerID == owner {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

func (s *keyRouteStore) RevokeDeveloperKey(_ context.Context, id, owner string) error {
	s.calls++
	for raw, key := range s.keys {
		if key.ID == id && key.OwnerID == owner {
			delete(s.keys, raw)
		}
	}
	return nil
}

func (s *keyRouteStore) TouchDeveloperKeyLastUsed(_ context.Context, _ string) error {
	return nil
}

func keyRouteRequest(router http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	return resp
}

func TestRouterDeveloperKeyManagementRequiresHostSession(t *testing.T) {
	const secret = "test-signing-secret-with-at-least-32-bytes"
	authStore := &keyRouteAuthStore{users: map[string]*models.User{
		"host":   {ID: "host", IsHost: true},
		"singer": {ID: "singer"},
	}}
	keys := &keyRouteStore{keys: map[string]*models.DeveloperKey{
		"mk_live_readonly": {ID: "read-key", OwnerID: "host", ScopeVersion: 1, Scopes: []string{handlers.DeveloperScopeRequestsRead}},
		"mk_live_full":     {ID: "full-key", OwnerID: "host", ScopeVersion: 1, Scopes: []string{handlers.DeveloperScopeRequestsRead, handlers.DeveloperScopeRequestsWrite}},
	}}
	router, err := NewRouter(config.Config{Environment: config.Development, AuthSecret: secret,
		DeveloperKeyScopeMode: handlers.DeveloperKeyScopeEnforce}, Dependencies{Auth: authStore, DeveloperKeys: keys})
	if err != nil {
		t.Fatalf("create router: %v", err)
	}
	for _, token := range []string{"mk_live_readonly", "mk_live_full"} {
		for _, tc := range []struct{ method, path, body string }{
			{http.MethodPost, "/api/v1/api-keys", `{"name":"escalated","scopes":["events:write"]}`},
			{http.MethodGet, "/api/v1/api-keys", ""},
			{http.MethodDelete, "/api/v1/api-keys/read-key", ""},
		} {
			t.Run(token+"/"+tc.method+" "+tc.path, func(t *testing.T) {
				before := keys.calls
				resp := keyRouteRequest(router, tc.method, tc.path, tc.body, token)
				if resp.Code != http.StatusUnauthorized {
					t.Fatalf("status = %d, body = %s; want 401", resp.Code, resp.Body.String())
				}
				if keys.calls != before {
					t.Fatalf("key store calls = %d; want %d", keys.calls, before)
				}
			})
		}
	}
	auth, err := handlers.NewAuthHandler(authStore, secret)
	if err != nil {
		t.Fatalf("create auth handler: %v", err)
	}
	issueToken := func(user *models.User) string {
		t.Helper()
		resp := httptest.NewRecorder()
		auth.WriteSession(resp, httptest.NewRequest(http.MethodGet, "/", nil), user, http.StatusOK)
		if resp.Code != http.StatusOK {
			t.Fatalf("issue session: status = %d, body = %s", resp.Code, resp.Body.String())
		}
		var session struct {
			AccessToken string `json:"access_token"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &session); err != nil {
			t.Fatalf("decode session: %v", err)
		}
		return session.AccessToken
	}
	singerToken := issueToken(authStore.users["singer"])
	if resp := keyRouteRequest(router, http.MethodGet, "/api/v1/api-keys", "", singerToken); resp.Code != http.StatusForbidden {
		t.Fatalf("singer status = %d; want 403", resp.Code)
	}
	hostToken := issueToken(authStore.users["host"])
	created := keyRouteRequest(router, http.MethodPost, "/api/v1/api-keys", `{"name":"integration","scopes":["requests:read"]}`, hostToken)
	if created.Code != http.StatusCreated {
		t.Fatalf("host create status = %d, body = %s; want 201", created.Code, created.Body.String())
	}
	if resp := keyRouteRequest(router, http.MethodGet, "/api/v1/api-keys", "", hostToken); resp.Code != http.StatusOK {
		t.Fatalf("host list status = %d; want 200", resp.Code)
	}
	if resp := keyRouteRequest(router, http.MethodDelete, "/api/v1/api-keys/created-key", "", hostToken); resp.Code != http.StatusNoContent {
		t.Fatalf("host revoke status = %d; want 204", resp.Code)
	}
}
