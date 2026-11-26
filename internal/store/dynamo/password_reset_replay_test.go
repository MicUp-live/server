package dynamo

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"
	"micup.live/server/internal/app"
	"micup.live/server/internal/config"
	"micup.live/server/internal/email"
)

const resetTestSecret = "test-signing-secret-with-at-least-32-bytes"

type resetEmailCapture struct{ links chan string }

func (s *resetEmailCapture) SendVerificationEmail(context.Context, string, string, string) error {
	return nil
}
func (s *resetEmailCapture) SendPasswordResetEmail(_ context.Context, _, _, link string) error {
	s.links <- link
	return nil
}
func (s *resetEmailCapture) SendSongbookInvitationEmail(context.Context, email.SongbookInvitationEmail) error {
	return nil
}

func requestResetLink(t *testing.T, router http.Handler, sender *resetEmailCapture, address string) string {
	t.Helper()
	response := requestJSON(t, router, http.MethodPost, "/api/v1/auth/forgot-password", map[string]interface{}{"email": address}, "")
	if response.Code != http.StatusOK {
		t.Fatalf("request reset: %d %s", response.Code, response.Body.String())
	}
	select {
	case link := <-sender.links:
		parsed, err := url.Parse(link)
		if err != nil {
			t.Fatal(err)
		}
		token := parsed.Query().Get("token")
		if token == "" {
			t.Fatal("reset email omitted token")
		}
		return token
	default:
		t.Fatal("reset email was not sent")
		return ""
	}
}

func TestDynamoHTTPPasswordResetRejectsReplayAndRevokesSessions(t *testing.T) {
	s := newLocalStore(t)
	sender := &resetEmailCapture{links: make(chan string, 2)}
	router, err := app.NewRouter(config.Config{Environment: config.Development, AuthSecret: resetTestSecret}, app.Dependencies{Auth: s, EmailSender: sender})
	if err != nil {
		t.Fatal(err)
	}
	address := uuid.NewString() + "@example.com"
	registered := requestJSON(t, router, http.MethodPost, "/api/v1/auth/register", map[string]interface{}{"email": address, "password": "old-password"}, "")
	if registered.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", registered.Code, registered.Body.String())
	}
	var tokens struct {
		AccessToken string `json:"access_token"`
	}
	decodeResponse(t, registered, &tokens)
	var refresh string
	for _, cookie := range registered.Result().Cookies() {
		if cookie.Name == "micup_refresh" {
			refresh = cookie.Value
		}
	}
	if refresh == "" {
		t.Fatal("missing refresh cookie")
	}
	if response := requestJSON(t, router, http.MethodGet, "/api/v1/auth/me", nil, tokens.AccessToken); response.Code != http.StatusOK {
		t.Fatalf("initial session: %d", response.Code)
	}
	token := requestResetLink(t, router, sender, address)
	reset := func(token, password string) int {
		return requestJSON(t, router, http.MethodPost, "/api/v1/auth/reset-password", map[string]interface{}{"token": token, "password": password}, "").Code
	}
	if status := reset(token, "new-password"); status != http.StatusOK {
		t.Fatalf("first reset: %d", status)
	}
	if status := reset(token, "replayed-password"); status != http.StatusBadRequest {
		t.Errorf("replay: %d; want 400", status)
	}
	if response := requestJSON(t, router, http.MethodGet, "/api/v1/auth/me", nil, tokens.AccessToken); response.Code != http.StatusUnauthorized {
		t.Errorf("old access token: %d; want 401", response.Code)
	}
	if response := requestJSON(t, router, http.MethodPost, "/api/v1/auth/refresh", map[string]interface{}{"refresh_token": refresh}, ""); response.Code != http.StatusUnauthorized {
		t.Errorf("old refresh token: %d; want 401", response.Code)
	}
	for _, password := range []string{"old-password", "replayed-password"} {
		if response := requestJSON(t, router, http.MethodPost, "/api/v1/auth/login", map[string]interface{}{"email": address, "password": password}, ""); response.Code != http.StatusUnauthorized {
			t.Errorf("obsolete password %q: %d", password, response.Code)
		}
	}
	login := requestJSON(t, router, http.MethodPost, "/api/v1/auth/login", map[string]interface{}{"email": address, "password": "new-password"}, "")
	if login.Code != http.StatusOK {
		t.Fatalf("new login: %d %s", login.Code, login.Body.String())
	}
	decodeResponse(t, login, &tokens)
	if response := requestJSON(t, router, http.MethodGet, "/api/v1/auth/me", nil, tokens.AccessToken); response.Code != http.StatusOK {
		t.Fatalf("new access token: %d", response.Code)
	}
	var nextRefresh string
	for _, cookie := range login.Result().Cookies() {
		if cookie.Name == "micup_refresh" {
			nextRefresh = cookie.Value
		}
	}
	if response := requestJSON(t, router, http.MethodPost, "/api/v1/auth/refresh", map[string]interface{}{"refresh_token": nextRefresh}, ""); response.Code != http.StatusOK {
		t.Fatalf("new refresh token: %d", response.Code)
	}
	if status := reset(requestResetLink(t, router, sender, address), "third-password"); status != http.StatusOK {
		t.Fatalf("fresh reset link: %d", status)
	}
}
