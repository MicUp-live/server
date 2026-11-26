package dynamo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"micup.live/server/internal/app"
	"micup.live/server/internal/config"
	"micup.live/server/internal/event"
	"micup.live/server/internal/handlers"
	"micup.live/server/internal/models"
)

func TestEventMutationHTTPConflicts(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()
	const secret = "test-event-signing-secret-at-least-32-bytes"
	owner := &models.User{ID: "settings-host", Email: "settings-host@example.com", IsHost: true}
	other := &models.User{ID: "other-host", Email: "other-host@example.com", IsHost: true}
	for _, user := range []*models.User{owner, other} {
		if err := s.CreateUser(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	auth, err := handlers.NewAuthHandler(s, secret)
	if err != nil {
		t.Fatal(err)
	}
	issueAccess := func(user *models.User) string {
		response := httptest.NewRecorder()
		auth.WriteSession(response, httptest.NewRequest(http.MethodPost, "/", nil), user, http.StatusOK)
		if response.Code != http.StatusOK {
			t.Fatalf("issue session: %d", response.Code)
		}
		var tokens struct {
			AccessToken string `json:"access_token"`
		}
		decodeResponse(t, response, &tokens)
		return tokens.AccessToken
	}
	hostToken, otherToken := issueAccess(owner), issueAccess(other)
	developerToken := "mk_live_" + uuid.NewString()
	if err := s.CreateDeveloperKey(ctx, developerToken, &models.DeveloperKey{ID: uuid.NewString(), OwnerID: owner.ID, Name: "settings", ScopeVersion: 1, Scopes: []string{handlers.DeveloperScopeEventsWrite}, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	readOnlyToken := "mk_live_" + uuid.NewString()
	if err := s.CreateDeveloperKey(ctx, readOnlyToken, &models.DeveloperKey{ID: uuid.NewString(), OwnerID: owner.ID, Name: "read", ScopeVersion: 1, Scopes: []string{handlers.DeveloperScopeEventsRead}, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	for _, credential := range []struct{ name, token string }{{"host", hostToken}, {"developer", developerToken}} {
		for _, route := range []struct {
			name, method, suffix string
			body                 map[string]interface{}
		}{
			{"settings", http.MethodPatch, "", map[string]interface{}{"name": "Stale settings"}},
			{"pause", http.MethodPost, "/pause", nil},
			{"resume", http.MethodPost, "/resume", nil},
			{"key", http.MethodPost, "/openkj-key/rotate", nil},
			{"restart without clearing", http.MethodPost, "/restart", map[string]interface{}{"clear_queue": false}},
		} {
			t.Run(credential.name+"/"+route.name, func(t *testing.T) {
				ev := settingsEvent(t, s)
				var winner *models.Event
				commits := 0
				boundary := &eventCommitInterleave{Store: s, beforeUpdate: func() error {
					commits++
					var err error
					winner, err = event.NewService(s, nil, s, 0).RotateOpenKJKey(ctx, ev.ID)
					return err
				}}
				router, err := app.NewRouter(config.Config{Environment: config.Development, AuthSecret: secret, DeveloperKeyScopeMode: handlers.DeveloperKeyScopeEnforce}, app.Dependencies{Auth: s, Events: boundary, Requests: s, DeveloperKeys: s})
				if err != nil {
					t.Fatal(err)
				}
				path := "/api/v1/events/" + ev.ID + route.suffix
				for _, denied := range []struct {
					token  string
					status int
				}{{otherToken, 404}, {readOnlyToken, 403}, {"", 401}} {
					response := requestJSON(t, router, route.method, path, route.body, denied.token)
					if response.Code != denied.status {
						t.Fatalf("denied status=%d; want %d", response.Code, denied.status)
					}
				}
				if commits != 0 {
					t.Fatal("denied request reached commit")
				}
				response := requestJSON(t, router, route.method, path, route.body, credential.token)
				if response.Code != http.StatusConflict {
					t.Fatalf("status=%d %s", response.Code, response.Body.String())
				}
				if response.Header().Get("X-Queue-Version") != strconv.FormatInt(winner.QueueVersion, 10) {
					t.Fatal("missing current queue version")
				}
				var body struct {
					Error    string `json:"error"`
					Expected int64  `json:"expected_queue_version"`
					Current  int64  `json:"current_queue_version"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Error != "queue_version_conflict" || body.Expected != ev.QueueVersion || body.Current != winner.QueueVersion {
					t.Fatalf("conflict=%+v", body)
				}
				actual, err := s.GetEvent(ctx, ev.ID)
				if err != nil {
					t.Fatal(err)
				}
				if actual.Status != ev.Status || actual.Name != ev.Name || actual.TTL != ev.TTL || actual.OpenKJKey != winner.OpenKJKey || actual.MetadataVersion != winner.MetadataVersion || actual.QueueVersion != winner.QueueVersion {
					t.Fatal("conflicted route changed event")
				}
			})
		}
	}
	t.Run("storage failure", func(t *testing.T) {
		ev := settingsEvent(t, s)
		boundary := &eventCommitInterleave{Store: s, beforeUpdate: func() error { return errors.New("injected commit failure") }}
		router, err := app.NewRouter(config.Config{Environment: config.Development, AuthSecret: secret}, app.Dependencies{Auth: s, Events: boundary, Requests: s})
		if err != nil {
			t.Fatal(err)
		}
		response := requestJSON(t, router, http.MethodPatch, "/api/v1/events/"+ev.ID, map[string]interface{}{"name": "Failed edit"}, hostToken)
		if response.Code != 500 {
			t.Fatalf("storage failure status=%d", response.Code)
		}
		actual, err := s.GetEvent(ctx, ev.ID)
		if err != nil {
			t.Fatal(err)
		}
		if actual.Name != ev.Name || actual.MetadataVersion != ev.MetadataVersion || actual.QueueVersion != ev.QueueVersion {
			t.Fatal("failed commit changed event")
		}
	})
}
