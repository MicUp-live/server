package dynamo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/google/uuid"
	"micup.live/server/internal/app"
	"micup.live/server/internal/config"
	"micup.live/server/internal/email"
	"micup.live/server/internal/handlers"
	"micup.live/server/internal/importjob"
	"micup.live/server/internal/models"
	"micup.live/server/internal/songbook"
	"micup.live/server/internal/store"
)

type collabEmailCapture struct {
	mu      sync.Mutex
	invites []email.SongbookInvitationEmail
	failErr error
}

func (c *collabEmailCapture) SendVerificationEmail(context.Context, string, string, string) error {
	return nil
}
func (c *collabEmailCapture) SendPasswordResetEmail(context.Context, string, string, string) error {
	return nil
}
func (c *collabEmailCapture) SendSongbookInvitationEmail(_ context.Context, msg email.SongbookInvitationEmail) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failErr != nil {
		return c.failErr
	}
	c.invites = append(c.invites, msg)
	return nil
}
func (c *collabEmailCapture) last() email.SongbookInvitationEmail {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.invites) == 0 {
		return email.SongbookInvitationEmail{}
	}
	return c.invites[len(c.invites)-1]
}
func (c *collabEmailCapture) setFail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failErr = err
}

type noopImportQueue struct{}

func (noopImportQueue) Enqueue(context.Context, importjob.Job) error { return nil }

type noopManifestWriter struct{}

func (noopManifestWriter) Put(context.Context, string, []byte) error { return nil }
func (noopManifestWriter) Presign(context.Context, string, int64) (string, error) {
	return "https://upload.example.test/presigned", nil
}
func (noopManifestWriter) DeleteKey(context.Context, string) error    { return nil }
func (noopManifestWriter) DeletePrefix(context.Context, string) error { return nil }

func newCollabRouter(t *testing.T, s *Store, sender email.Sender, authRateLimit int) http.Handler {
	t.Helper()
	if authRateLimit <= 0 {
		authRateLimit = 100
	}
	cfg := config.Config{
		Environment:           config.Development,
		AuthSecret:            "songbook-collaboration-test-secret-at-least-32-bytes",
		PublicBaseURL:         "https://app.micup.test",
		AllowedOrigins:        []string{"https://app.micup.test"},
		DeveloperKeyScopeMode: handlers.DeveloperKeyScopeEnforce,
		AuthRateLimit:         authRateLimit,
		PublicRequestLimit:    100,
	}
	h, err := app.NewRouter(cfg, app.Dependencies{
		Auth:           s,
		Passkeys:       s,
		Songbooks:      s,
		Requests:       s,
		Events:         s,
		Catalogs:       s,
		CatalogChanges: s,
		ImportJobs:     s,
		ImportQueue:    noopImportQueue{},
		ManifestWriter: noopManifestWriter{},
		DeveloperKeys:  s,
		RateLimits:     s,
		SongbookAccess: s,
		EmailSender:    sender,
	})
	if err != nil {
		t.Fatalf("new collab router: %v", err)
	}
	return h
}

func registerAndVerifyHost(t *testing.T, router http.Handler, s *Store, emailAddr, displayName string, isHost, verified bool) (string, *models.User) {
	t.Helper()
	resp := requestJSON(t, router, http.MethodPost, "/api/v1/auth/register", map[string]any{
		"email":        emailAddr,
		"password":     "Password123!",
		"display_name": displayName,
		"is_host":      isHost,
	}, "")
	if resp.Code != http.StatusCreated {
		t.Fatalf("register %s: %d %s", emailAddr, resp.Code, resp.Body.String())
	}
	var out struct {
		AccessToken string       `json:"access_token"`
		User        *models.User `json:"user"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode register: %v", err)
	}
	if verified {
		u, err := s.GetUser(context.Background(), out.User.ID)
		if err != nil {
			t.Fatalf("get user: %v", err)
		}
		u.EmailVerified = true
		if err := s.UpdateUser(context.Background(), u); err != nil {
			t.Fatalf("verify user: %v", err)
		}
		out.User = u
	}
	return out.AccessToken, out.User
}

func extractTokenFromAcceptURL(t *testing.T, acceptURL string) string {
	t.Helper()
	parsed, err := url.Parse(acceptURL)
	if err != nil {
		t.Fatalf("parse accept URL %q: %v", acceptURL, err)
	}
	tok := parsed.Query().Get("token")
	if tok == "" {
		t.Fatalf("missing token in %q", acceptURL)
	}
	return tok
}

func TestDynamoSongbookCollaboration_InvitationEmailLifecycle(t *testing.T) {
	s := newLocalStore(t)
	capture := &collabEmailCapture{}
	router := newCollabRouter(t, s, capture, 50)

	ownerToken, owner := registerAndVerifyHost(t, router, s, "owner-inv@example.com", "DJ Owner", true, true)
	existingToken, existingUser := registerAndVerifyHost(t, router, s, "existing@example.com", "DJ Existing", true, true)
	nonHostToken, _ := registerAndVerifyHost(t, router, s, "singer-only@example.com", "Singer Only", false, true)
	unverifiedToken, _ := registerAndVerifyHost(t, router, s, "unverified@example.com", "Unverified Host", true, false)

	// Owner creates a songbook.
	sbResp := requestJSON(t, router, http.MethodPost, "/api/v1/songbooks", map[string]any{
		"name": "Shared Friday Book", "is_public": false,
	}, ownerToken)
	if sbResp.Code != http.StatusCreated {
		t.Fatalf("create songbook: %d %s", sbResp.Code, sbResp.Body.String())
	}
	var sb models.Songbook
	_ = json.Unmarshal(sbResp.Body.Bytes(), &sb)

	// Developer key must NOT manage access or accept invitations (C01: host session required).
	devKeyResp := requestJSON(t, router, http.MethodPost, "/api/v1/api-keys", map[string]any{
		"name": "Catalog Key", "scopes": []string{"catalogs:write", "events:write"},
	}, ownerToken)
	var devKey struct {
		RawKey string `json:"raw_key"`
	}
	_ = json.Unmarshal(devKeyResp.Body.Bytes(), &devKey)
	if resp := requestJSON(t, router, http.MethodGet, "/api/v1/songbooks/"+sb.ID+"/access", nil, devKey.RawKey); resp.Code != http.StatusUnauthorized {
		t.Fatalf("dev key GET /access = %d, want 401", resp.Code)
	}
	if resp := requestJSON(t, router, http.MethodPost, "/api/v1/songbooks/"+sb.ID+"/invitations", map[string]any{
		"email": "existing@example.com", "can_host_shows": true,
	}, devKey.RawKey); resp.Code != http.StatusUnauthorized {
		t.Fatalf("dev key POST /invitations = %d, want 401", resp.Code)
	}

	// 1. Provider failure keeps invite pending and records send_error; resend succeeds & invalidates prior link.
	capture.setFail(errors.New("ses throttled"))
	invResp := requestJSON(t, router, http.MethodPost, "/api/v1/songbooks/"+sb.ID+"/invitations", map[string]any{
		"email": "existing@example.com", "can_edit_catalog": true, "can_host_shows": true,
	}, ownerToken)
	if invResp.Code != http.StatusCreated {
		t.Fatalf("create invite on provider failure = %d %s, want 201", invResp.Code, invResp.Body.String())
	}
	var invOut struct {
		Invitation     models.SongbookInvitation `json:"invitation"`
		DeliveryFailed bool                      `json:"delivery_failed"`
	}
	_ = json.Unmarshal(invResp.Body.Bytes(), &invOut)
	if !invOut.DeliveryFailed || invOut.Invitation.SendError == "" || invOut.Invitation.Status != models.InvitationStatusPending {
		t.Fatalf("expected pending invite with delivery_failed=true, got %+v", invOut)
	}

	// Retry/Resend succeeds and sends email with book name, inviter, permissions, expiry, accept URL.
	capture.setFail(nil)
	resendResp := requestJSON(t, router, http.MethodPost, "/api/v1/songbooks/"+sb.ID+"/invitations/"+invOut.Invitation.ID+"/resend", nil, ownerToken)
	if resendResp.Code != http.StatusOK {
		t.Fatalf("resend invite = %d %s", resendResp.Code, resendResp.Body.String())
	}
	msg1 := capture.last()
	if msg1.ToEmail != "existing@example.com" || msg1.SongbookName != "Shared Friday Book" || msg1.InviterName != owner.DisplayName || !msg1.CanEditCatalog || !msg1.CanHostShows || msg1.ExpiresAt.IsZero() {
		t.Fatalf("captured email mismatch: %+v", msg1)
	}
	tok1 := extractTokenFromAcceptURL(t, msg1.AcceptURL)

	// Resend again -> fresh link invalidates tok1!
	resendResp2 := requestJSON(t, router, http.MethodPost, "/api/v1/songbooks/"+sb.ID+"/invitations/"+invOut.Invitation.ID+"/resend", nil, ownerToken)
	if resendResp2.Code != http.StatusOK {
		t.Fatalf("second resend = %d %s", resendResp2.Code, resendResp2.Body.String())
	}
	tok2 := extractTokenFromAcceptURL(t, capture.last().AcceptURL)
	if tok1 == tok2 {
		t.Fatalf("resend reused token")
	}
	if resp := requestJSON(t, router, http.MethodPost, "/api/v1/songbook-invitations/accept", map[string]any{"token": tok1}, existingToken); resp.Code != http.StatusNotFound {
		t.Fatalf("old token after resend = %d, want 404", resp.Code)
	}

	// Wrong recipient cannot accept tok2.
	if resp := requestJSON(t, router, http.MethodPost, "/api/v1/songbook-invitations/accept", map[string]any{"token": tok2}, ownerToken); resp.Code != http.StatusForbidden {
		t.Fatalf("wrong recipient accept = %d, want 403", resp.Code)
	}

	// Eligible existing recipient accepts tok2.
	accResp := requestJSON(t, router, http.MethodPost, "/api/v1/songbook-invitations/accept", map[string]any{"token": tok2}, existingToken)
	if accResp.Code != http.StatusOK {
		t.Fatalf("eligible accept = %d %s", accResp.Code, accResp.Body.String())
	}

	// Revoke grant, then replay tok2 -> must NEVER restore removed rights!
	if resp := requestJSON(t, router, http.MethodDelete, "/api/v1/songbooks/"+sb.ID+"/members/"+existingUser.ID, nil, ownerToken); resp.Code != http.StatusNoContent {
		t.Fatalf("revoke member = %d %s", resp.Code, resp.Body.String())
	}
	if resp := requestJSON(t, router, http.MethodPost, "/api/v1/songbook-invitations/accept", map[string]any{"token": tok2}, existingToken); resp.Code != http.StatusNotFound && resp.Code != http.StatusGone {
		t.Fatalf("replay after revocation = %d, want 404 or 410", resp.Code)
	}
	if _, err := s.GetSongbookGrant(context.Background(), sb.ID, existingUser.ID); !errors.Is(err, store.ErrGrantNotFound) {
		t.Fatalf("grant should remain absent after replay, got err=%v", err)
	}

	// 2. New account flow: invite sent before recipient registers; recipient registers, verifies, accepts.
	newInvResp := requestJSON(t, router, http.MethodPost, "/api/v1/songbooks/"+sb.ID+"/invitations", map[string]any{
		"email": "newcollab@example.com", "can_host_shows": true,
	}, ownerToken)
	if newInvResp.Code != http.StatusCreated {
		t.Fatalf("invite new user = %d %s", newInvResp.Code, newInvResp.Body.String())
	}
	newTokFirst := extractTokenFromAcceptURL(t, capture.last().AcceptURL)
	// Replace pending invite for same email -> earlier link invalidated!
	newInvResp2 := requestJSON(t, router, http.MethodPost, "/api/v1/songbooks/"+sb.ID+"/invitations", map[string]any{
		"email": "newcollab@example.com", "can_edit_catalog": true, "can_host_shows": true,
	}, ownerToken)
	if newInvResp2.Code != http.StatusCreated {
		t.Fatalf("replace invite = %d %s", newInvResp2.Code, newInvResp2.Body.String())
	}
	newTokSecond := extractTokenFromAcceptURL(t, capture.last().AcceptURL)

	newCollabToken, _ := registerAndVerifyHost(t, router, s, "newcollab@example.com", "New Collab", true, true)
	if resp := requestJSON(t, router, http.MethodPost, "/api/v1/songbook-invitations/accept", map[string]any{"token": newTokFirst}, newCollabToken); resp.Code != http.StatusNotFound {
		t.Fatalf("replaced invite token = %d, want 404", resp.Code)
	}
	if resp := requestJSON(t, router, http.MethodPost, "/api/v1/songbook-invitations/accept", map[string]any{"token": newTokSecond}, newCollabToken); resp.Code != http.StatusOK {
		t.Fatalf("new account accept = %d %s", resp.Code, resp.Body.String())
	}

	// 3. Ineligible accounts (non-host and unverified) rejected.
	invNonHost := requestJSON(t, router, http.MethodPost, "/api/v1/songbooks/"+sb.ID+"/invitations", map[string]any{
		"email": "singer-only@example.com", "can_host_shows": true,
	}, ownerToken)
	if invNonHost.Code != http.StatusCreated {
		t.Fatalf("invite non-host = %d", invNonHost.Code)
	}
	nonHostTok := extractTokenFromAcceptURL(t, capture.last().AcceptURL)
	if resp := requestJSON(t, router, http.MethodPost, "/api/v1/songbook-invitations/accept", map[string]any{"token": nonHostTok}, nonHostToken); resp.Code != http.StatusForbidden {
		t.Fatalf("non-host accept = %d, want 403", resp.Code)
	}

	invUnverified := requestJSON(t, router, http.MethodPost, "/api/v1/songbooks/"+sb.ID+"/invitations", map[string]any{
		"email": "unverified@example.com", "can_host_shows": true,
	}, ownerToken)
	if invUnverified.Code != http.StatusCreated {
		t.Fatalf("invite unverified = %d", invUnverified.Code)
	}
	unverifiedTok := extractTokenFromAcceptURL(t, capture.last().AcceptURL)
	if resp := requestJSON(t, router, http.MethodPost, "/api/v1/songbook-invitations/accept", map[string]any{"token": unverifiedTok}, unverifiedToken); resp.Code != http.StatusForbidden {
		t.Fatalf("unverified accept = %d, want 403", resp.Code)
	}

	// 4. Canceled invitation cannot be accepted.
	var unvInv struct {
		Invitation models.SongbookInvitation `json:"invitation"`
	}
	_ = json.Unmarshal(invUnverified.Body.Bytes(), &unvInv)
	if resp := requestJSON(t, router, http.MethodDelete, "/api/v1/songbooks/"+sb.ID+"/invitations/"+unvInv.Invitation.ID, nil, ownerToken); resp.Code != http.StatusNoContent {
		t.Fatalf("cancel invite = %d", resp.Code)
	}
	if resp := requestJSON(t, router, http.MethodPost, "/api/v1/songbook-invitations/accept", map[string]any{"token": unverifiedTok}, unverifiedToken); resp.Code != http.StatusForbidden && resp.Code != http.StatusNotFound {
		t.Fatalf("canceled invite accept = %d", resp.Code)
	}

	// 5. Expired invitation cannot be accepted.
	expiredRaw := "expired-raw-token-12345"
	expiredInv := &models.SongbookInvitation{
		ID:             uuid.NewString(),
		SongbookID:     sb.ID,
		OwnerID:        owner.ID,
		RecipientEmail: "existing@example.com",
		CanHostShows:   true,
		TokenHash:      songbook.HashInvitationToken(expiredRaw),
		ExpiresAt:      time.Now().UTC().Add(-time.Minute),
	}
	if err := s.CreateSongbookInvitation(context.Background(), expiredInv, nil); err != nil {
		t.Fatalf("seed expired invite: %v", err)
	}
	if resp := requestJSON(t, router, http.MethodPost, "/api/v1/songbook-invitations/accept", map[string]any{"token": expiredRaw}, existingToken); resp.Code != http.StatusGone {
		t.Fatalf("expired invite accept = %d, want 410", resp.Code)
	}

	// 6. Rate limiting on sends/resends (fresh client IP so earlier sends in this test do not consume the window).
	rlRouter := newCollabRouter(t, s, capture, 2)
	sendFromIP := func(ip string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]any{"email": "rl@example.com", "can_host_shows": true})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/songbooks/"+sb.ID+"/invitations", bytes.NewReader(raw))
		req.RemoteAddr = ip
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+ownerToken)
		rec := httptest.NewRecorder()
		rlRouter.ServeHTTP(rec, req)
		return rec
	}
	for i := 0; i < 2; i++ {
		if r := sendFromIP("198.51.100.42:12345"); r.Code != http.StatusCreated {
			t.Fatalf("rate-limit attempt %d = %d, want 201", i, r.Code)
		}
	}
	if r := sendFromIP("198.51.100.42:12345"); r.Code != http.StatusTooManyRequests {
		t.Fatalf("third invite = %d, want 429", r.Code)
	}
}

func TestDynamoSongbookCollaboration_PermissionMatrixAndTwoHosts(t *testing.T) {
	s := newLocalStore(t)
	capture := &collabEmailCapture{}
	router := newCollabRouter(t, s, capture, 100)

	ownerToken, _ := registerAndVerifyHost(t, router, s, "owner-m@example.com", "Owner Host", true, true)
	editorToken, editor := registerAndVerifyHost(t, router, s, "editor-m@example.com", "Editor Host", true, true)
	hostToken, hostUser := registerAndVerifyHost(t, router, s, "host-m@example.com", "Show Host", true, true)
	combinedToken, combinedUser := registerAndVerifyHost(t, router, s, "combined-m@example.com", "Combined Host", true, true)
	strangerToken, _ := registerAndVerifyHost(t, router, s, "stranger-m@example.com", "Stranger Host", true, true)

	// Owner creates a PRIVATE songbook and adds an initial song.
	sbResp := requestJSON(t, router, http.MethodPost, "/api/v1/songbooks", map[string]any{
		"name": "Private Shared Book", "is_public": false,
	}, ownerToken)
	var sb models.Songbook
	_ = json.Unmarshal(sbResp.Body.Bytes(), &sb)

	if r := requestJSON(t, router, http.MethodPost, "/api/v1/songbooks/"+sb.ID+"/songs", map[string]any{
		"artist": "Queen", "title": "Bohemian Rhapsody",
	}, ownerToken); r.Code != http.StatusCreated {
		t.Fatalf("owner add song = %d %s", r.Code, r.Body.String())
	}

	inviteAndAccept := func(emailAddr, tok string, edit, host bool) {
		t.Helper()
		r := requestJSON(t, router, http.MethodPost, "/api/v1/songbooks/"+sb.ID+"/invitations", map[string]any{
			"email": emailAddr, "can_edit_catalog": edit, "can_host_shows": host,
		}, ownerToken)
		if r.Code != http.StatusCreated {
			t.Fatalf("invite %s = %d %s", emailAddr, r.Code, r.Body.String())
		}
		rawTok := extractTokenFromAcceptURL(t, capture.last().AcceptURL)
		acc := requestJSON(t, router, http.MethodPost, "/api/v1/songbook-invitations/accept", map[string]any{"token": rawTok}, tok)
		if acc.Code != http.StatusOK {
			t.Fatalf("accept %s = %d %s", emailAddr, acc.Code, acc.Body.String())
		}
	}
	inviteAndAccept("editor-m@example.com", editorToken, true, false)
	inviteAndAccept("host-m@example.com", hostToken, false, true)
	inviteAndAccept("combined-m@example.com", combinedToken, true, true)

	// Discovery: editor, host, combined see book with effective permissions; stranger gets 404.
	for _, tc := range []struct {
		name      string
		tok       string
		wantCode  int
		wantEdit  bool
		wantHost  bool
		wantOwner bool
	}{
		{"owner", ownerToken, http.StatusOK, true, true, true},
		{"editor", editorToken, http.StatusOK, true, false, false},
		{"host", hostToken, http.StatusOK, false, true, false},
		{"combined", combinedToken, http.StatusOK, true, true, false},
		{"stranger", strangerToken, http.StatusNotFound, false, false, false},
	} {
		r := requestJSON(t, router, http.MethodGet, "/api/v1/songbooks/"+sb.ID, nil, tc.tok)
		if r.Code != tc.wantCode {
			t.Fatalf("GET songbook as %s = %d, want %d", tc.name, r.Code, tc.wantCode)
		}
		if tc.wantCode == http.StatusOK {
			var got models.Songbook
			_ = json.Unmarshal(r.Body.Bytes(), &got)
			if got.Permissions == nil || got.Permissions.CanEditCatalog != tc.wantEdit || got.Permissions.CanHostShows != tc.wantHost || got.Permissions.IsOwner != tc.wantOwner {
				t.Fatalf("unexpected permissions for %s: %+v", tc.name, got.Permissions)
			}
		}
	}

	// Owner-only management denied for collaborators.
	for _, tok := range []string{editorToken, hostToken, combinedToken} {
		if r := requestJSON(t, router, http.MethodPatch, "/api/v1/songbooks/"+sb.ID, map[string]any{"name": "Hacked"}, tok); r.Code != http.StatusForbidden {
			t.Fatalf("collaborator PATCH songbook = %d, want 403", r.Code)
		}
		if r := requestJSON(t, router, http.MethodDelete, "/api/v1/songbooks/"+sb.ID, nil, tok); r.Code != http.StatusForbidden {
			t.Fatalf("collaborator DELETE songbook = %d, want 403", r.Code)
		}
	}

	// Catalog read/write: editor & combined allowed; host-only denied (403); stranger 404.
	if r := requestJSON(t, router, http.MethodGet, "/api/v1/songbooks/"+sb.ID+"/managed-manifest", nil, editorToken); r.Code != http.StatusOK {
		t.Fatalf("editor managed-manifest = %d %s", r.Code, r.Body.String())
	}
	if r := requestJSON(t, router, http.MethodPost, "/api/v1/songbooks/"+sb.ID+"/songs", map[string]any{
		"artist": "Journey", "title": "Don't Stop Believin'",
	}, editorToken); r.Code != http.StatusCreated {
		t.Fatalf("editor add song = %d %s", r.Code, r.Body.String())
	}
	if r := requestJSON(t, router, http.MethodGet, "/api/v1/songbooks/"+sb.ID+"/managed-manifest", nil, hostToken); r.Code != http.StatusForbidden {
		t.Fatalf("host-only managed-manifest = %d, want 403", r.Code)
	}
	if r := requestJSON(t, router, http.MethodPost, "/api/v1/songbooks/"+sb.ID+"/songs/clear", nil, hostToken); r.Code != http.StatusForbidden {
		t.Fatalf("host-only clear = %d, want 403", r.Code)
	}

	// Edit-only collaborator cannot start a show or switch an existing show onto the book.
	if r := requestJSON(t, router, http.MethodPost, "/api/v1/events", map[string]any{
		"songbook_id": sb.ID, "name": "Unauthorized Show",
	}, editorToken); r.Code != http.StatusForbidden {
		t.Fatalf("edit-only start show = %d, want 403", r.Code)
	}

	// TWO HOSTS RUN SIMULTANEOUS SHOWS FROM ONE PRIVATE SONGBOOK!
	evOwnerResp := requestJSON(t, router, http.MethodPost, "/api/v1/events", map[string]any{
		"songbook_id": sb.ID, "name": "Owner Friday Show", "auto_accept": true,
	}, ownerToken)
	if evOwnerResp.Code != http.StatusCreated {
		t.Fatalf("owner create show = %d %s", evOwnerResp.Code, evOwnerResp.Body.String())
	}
	var evOwner models.Event
	_ = json.Unmarshal(evOwnerResp.Body.Bytes(), &evOwner)

	evHostResp := requestJSON(t, router, http.MethodPost, "/api/v1/events", map[string]any{
		"songbook_id": sb.ID, "name": "Collab Friday Show", "auto_accept": true,
	}, hostToken)
	if evHostResp.Code != http.StatusCreated {
		t.Fatalf("collab create show = %d %s", evHostResp.Code, evHostResp.Body.String())
	}
	var evHost models.Event
	_ = json.Unmarshal(evHostResp.Body.Bytes(), &evHost)

	// Singers at collab's show can browse Owner's PRIVATE songbook via event-scoped public projection!
	if r := requestJSON(t, router, http.MethodGet, "/api/v1/songbooks/"+sb.PublicID+"/public-overrides?code="+evHost.JoinCode, nil, ""); r.Code != http.StatusOK {
		t.Fatalf("public overrides for collab show on private book = %d %s", r.Code, r.Body.String())
	}
	// Without event code, private book stays private (404).
	if r := requestJSON(t, router, http.MethodGet, "/api/v1/songbooks/"+sb.PublicID+"/public-overrides", nil, ""); r.Code != http.StatusNotFound {
		t.Fatalf("public overrides without event code = %d, want 404", r.Code)
	}

	// Independent queues & tenant isolation between the two shows.
	reqOwnerResp := requestJSON(t, router, http.MethodPost, "/api/v1/events/"+evOwner.ID+"/requests", map[string]any{
		"song_title": "Owner Queue Song", "song_artist": "Queen", "singer_name": "Alice",
	}, "")
	if reqOwnerResp.Code != http.StatusCreated {
		t.Fatalf("submit owner show request = %d %s", reqOwnerResp.Code, reqOwnerResp.Body.String())
	}
	var reqOwner models.SongRequest
	_ = json.Unmarshal(reqOwnerResp.Body.Bytes(), &reqOwner)

	reqHostResp := requestJSON(t, router, http.MethodPost, "/api/v1/events/"+evHost.ID+"/requests", map[string]any{
		"song_title": "Collab Queue Song", "song_artist": "Journey", "singer_name": "Bob",
	}, "")
	if reqHostResp.Code != http.StatusCreated {
		t.Fatalf("submit collab show request = %d %s", reqHostResp.Code, reqHostResp.Body.String())
	}
	var reqHost models.SongRequest
	_ = json.Unmarshal(reqHostResp.Body.Bytes(), &reqHost)

	// Neither host can access the other host's event or requests, including via compatibility songbook routes!
	if r := requestJSON(t, router, http.MethodGet, "/api/v1/events/"+evHost.ID+"/requests", nil, ownerToken); r.Code != http.StatusNotFound {
		t.Fatalf("owner reading collab event requests = %d, want 404", r.Code)
	}
	// Retired songbook-scoped request routes return 404.
	if r := requestJSON(t, router, http.MethodGet, "/api/v1/songbooks/"+sb.ID+"/requests/"+reqHost.ID, nil, ownerToken); r.Code != http.StatusNotFound {
		t.Fatalf("retired GET /songbooks/{id}/requests/{rid} = %d, want 404", r.Code)
	}
	if r := requestJSON(t, router, http.MethodDelete, "/api/v1/songbooks/"+sb.ID+"/requests/"+reqHost.ID, nil, ownerToken); r.Code != http.StatusNotFound {
		t.Fatalf("retired DELETE /songbooks/{id}/requests/{rid} = %d, want 404", r.Code)
	}
	if r := requestJSON(t, router, http.MethodPatch, "/api/v1/songbooks/"+sb.ID+"/requests/"+reqHost.ID, map[string]any{"status": "completed"}, ownerToken); r.Code != http.StatusNotFound {
		t.Fatalf("retired PATCH /songbooks/{id}/requests/{rid} = %d, want 404", r.Code)
	}
	if r := requestJSON(t, router, http.MethodGet, "/api/v1/songbooks/"+sb.ID+"/requests", nil, ownerToken); r.Code != http.StatusNotFound {
		t.Fatalf("retired GET /songbooks/{id}/requests = %d, want 404", r.Code)
	}
	if r := requestJSON(t, router, http.MethodPost, "/api/v1/songbooks/"+sb.ID+"/requests/reorder", map[string]any{"order": []string{}}, ownerToken); r.Code != http.StatusNotFound {
		t.Fatalf("retired POST /songbooks/{id}/requests/reorder = %d, want 404", r.Code)
	}

	// HOSTING-ONLY OPENKJ KEY CANNOT MODIFY CATALOG!
	if r := requestJSON(t, router, http.MethodGet, "/api/v1/openkj/api.php?action=getrequests&key="+evHost.OpenKJKey, nil, ""); r.Code != http.StatusOK {
		t.Fatalf("host-only OpenKJ getrequests = %d %s", r.Code, r.Body.String())
	}
	if r := requestJSON(t, router, http.MethodPost, "/api/v1/openkj/api.php?action=cleardatabase&key="+evHost.OpenKJKey, nil, ""); r.Code != http.StatusForbidden {
		t.Fatalf("host-only OpenKJ cleardatabase = %d, want 403", r.Code)
	}
	if r := requestJSON(t, router, http.MethodPost, "/api/v1/openkj/api.php?action=addsongs&key="+evHost.OpenKJKey, []map[string]any{
		{"artist": "Bad", "title": "Track"},
	}, ""); r.Code != http.StatusForbidden {
		t.Fatalf("host-only OpenKJ addsongs = %d, want 403", r.Code)
	}

	// Combined host starts a show, gets OpenKJ key, then has edit revoked -> OpenKJ key immediately loses catalog write!
	evCombResp := requestJSON(t, router, http.MethodPost, "/api/v1/events", map[string]any{
		"songbook_id": sb.ID, "name": "Combined Show",
	}, combinedToken)
	var evComb models.Event
	_ = json.Unmarshal(evCombResp.Body.Bytes(), &evComb)
	if r := requestJSON(t, router, http.MethodPost, "/api/v1/openkj/api.php?action=addsongs&key="+evComb.OpenKJKey, []map[string]any{
		{"artist": "Allowed", "title": "Before Revoke"},
	}, ""); r.Code != http.StatusOK {
		t.Fatalf("combined OpenKJ addsongs before revoke = %d %s", r.Code, r.Body.String())
	}
	// Downgrade combined user to host-only.
	if r := requestJSON(t, router, http.MethodPatch, "/api/v1/songbooks/"+sb.ID+"/members/"+combinedUser.ID, map[string]any{
		"can_edit_catalog": false, "can_host_shows": true,
	}, ownerToken); r.Code != http.StatusOK {
		t.Fatalf("downgrade combined member = %d %s", r.Code, r.Body.String())
	}
	if r := requestJSON(t, router, http.MethodPost, "/api/v1/openkj/api.php?action=cleardatabase&key="+evComb.OpenKJKey, nil, ""); r.Code != http.StatusForbidden {
		t.Fatalf("pre-existing OpenKJ key after edit revoke = %d, want 403", r.Code)
	}

	// Revoking editing stops incomplete upload completion!
	uploadResp := requestJSON(t, router, http.MethodPost, "/api/v1/songbooks/"+sb.ID+"/import-uploads", map[string]any{
		"total_songs": 10, "size": 1024, "prune_missing": false,
	}, editorToken)
	if uploadResp.Code != http.StatusOK {
		t.Fatalf("editor create import upload = %d %s", uploadResp.Code, uploadResp.Body.String())
	}
	var uploadOut struct {
		Job models.ImportJob `json:"job"`
	}
	_ = json.Unmarshal(uploadResp.Body.Bytes(), &uploadOut)
	if r := requestJSON(t, router, http.MethodDelete, "/api/v1/songbooks/"+sb.ID+"/members/"+editor.ID, nil, ownerToken); r.Code != http.StatusNoContent {
		t.Fatalf("revoke editor = %d", r.Code)
	}
	if r := requestJSON(t, router, http.MethodPost, "/api/v1/songbooks/"+sb.ID+"/import-jobs/"+uploadOut.Job.ID+"/complete", nil, editorToken); r.Code != http.StatusNotFound && r.Code != http.StatusForbidden {
		t.Fatalf("complete upload after edit revoke = %d, want 403/404", r.Code)
	}

	// Revoking hosting stops new shows & ended restart, while active/paused show can pause, resume, and finish!
	if r := requestJSON(t, router, http.MethodDelete, "/api/v1/songbooks/"+sb.ID+"/members/"+hostUser.ID, nil, ownerToken); r.Code != http.StatusNoContent {
		t.Fatalf("revoke host = %d", r.Code)
	}
	if r := requestJSON(t, router, http.MethodPost, "/api/v1/events", map[string]any{
		"songbook_id": sb.ID, "name": "After Revoke Show",
	}, hostToken); r.Code != http.StatusBadRequest && r.Code != http.StatusForbidden {
		t.Fatalf("new show after host revoke = %d, want 400/403", r.Code)
	}
	// Active show can pause and resume from pause!
	if r := requestJSON(t, router, http.MethodPost, "/api/v1/events/"+evHost.ID+"/pause", nil, hostToken); r.Code != http.StatusOK {
		t.Fatalf("pause active show after revoke = %d %s", r.Code, r.Body.String())
	}
	if r := requestJSON(t, router, http.MethodPost, "/api/v1/events/"+evHost.ID+"/resume", nil, hostToken); r.Code != http.StatusOK {
		t.Fatalf("resume paused show after revoke = %d %s", r.Code, r.Body.String())
	}
	// End the show -> now cannot restart or reactivate via PATCH status!
	if r := requestJSON(t, router, http.MethodPatch, "/api/v1/events/"+evHost.ID, map[string]any{"status": "ended"}, hostToken); r.Code != http.StatusOK {
		t.Fatalf("end show = %d %s", r.Code, r.Body.String())
	}
	if r := requestJSON(t, router, http.MethodPost, "/api/v1/events/"+evHost.ID+"/restart", map[string]any{"clear_queue": false}, hostToken); r.Code != http.StatusNotFound && r.Code != http.StatusForbidden {
		t.Fatalf("restart ended show after revoke = %d, want 403/404", r.Code)
	}
	if r := requestJSON(t, router, http.MethodPatch, "/api/v1/events/"+evHost.ID, map[string]any{"status": "active"}, hostToken); r.Code != http.StatusNotFound && r.Code != http.StatusForbidden {
		t.Fatalf("reactivate ended show via PATCH after revoke = %d, want 403/404", r.Code)
	}
}

func TestDynamoSongbookCollaboration_OwnerDeletionAndAccountCleanup(t *testing.T) {
	s := newLocalStore(t)
	capture := &collabEmailCapture{}
	router := newCollabRouter(t, s, capture, 100)

	ownerToken, _ := registerAndVerifyHost(t, router, s, "owner-del@example.com", "Owner Del", true, true)
	collabToken, collab := registerAndVerifyHost(t, router, s, "collab-del@example.com", "Collab Del", true, true)

	// Owner creates book, invites collab with host access, collab starts a show with a queued request.
	sbResp := requestJSON(t, router, http.MethodPost, "/api/v1/songbooks", map[string]any{"name": "Owner Book"}, ownerToken)
	var sb models.Songbook
	_ = json.Unmarshal(sbResp.Body.Bytes(), &sb)

	// Collab also has their own replacement book.
	replResp := requestJSON(t, router, http.MethodPost, "/api/v1/songbooks", map[string]any{"name": "Collab Replacement Book"}, collabToken)
	var replBook models.Songbook
	_ = json.Unmarshal(replResp.Body.Bytes(), &replBook)

	requestJSON(t, router, http.MethodPost, "/api/v1/songbooks/"+sb.ID+"/invitations", map[string]any{
		"email": "collab-del@example.com", "can_host_shows": true,
	}, ownerToken)
	tok := extractTokenFromAcceptURL(t, capture.last().AcceptURL)
	requestJSON(t, router, http.MethodPost, "/api/v1/songbook-invitations/accept", map[string]any{"token": tok}, collabToken)

	// Account export for collab must NOT include owner's shared book!
	expResp := requestJSON(t, router, http.MethodGet, "/api/v1/auth/me/export", nil, collabToken)
	if expResp.Code != http.StatusOK || strings.Contains(expResp.Body.String(), sb.ID) {
		t.Fatalf("collab export leaked shared book or failed: %d %s", expResp.Code, expResp.Body.String())
	}

	evResp := requestJSON(t, router, http.MethodPost, "/api/v1/events", map[string]any{
		"songbook_id": sb.ID, "name": "Secret Collab Show", "auto_accept": true,
	}, collabToken)
	var ev models.Event
	_ = json.Unmarshal(evResp.Body.Bytes(), &ev)
	requestJSON(t, router, http.MethodPost, "/api/v1/events/"+ev.ID+"/requests", map[string]any{
		"song_title": "Keep Me In History", "song_artist": "Queen", "singer_name": "Dana",
	}, "")

	// Owner tries DELETE without confirmation -> 409 foreign_events_associated, no private event name leaked!
	del1 := requestJSON(t, router, http.MethodDelete, "/api/v1/songbooks/"+sb.ID, nil, ownerToken)
	if del1.Code != http.StatusConflict {
		t.Fatalf("unconfirmed delete = %d %s, want 409", del1.Code, del1.Body.String())
	}
	if strings.Contains(del1.Body.String(), "Secret Collab Show") || strings.Contains(del1.Body.String(), "Dana") {
		t.Fatalf("delete conflict leaked collaborator private data: %s", del1.Body.String())
	}
	var conflict struct {
		Error              string `json:"error"`
		AffectedShowsCount int    `json:"affected_shows_count"`
		ActiveShowsCount   int    `json:"active_shows_count"`
		ConfirmationToken  string `json:"confirmation_token"`
	}
	_ = json.Unmarshal(del1.Body.Bytes(), &conflict)
	if conflict.Error != "foreign_events_associated" || conflict.AffectedShowsCount != 1 || conflict.ActiveShowsCount != 1 || conflict.ConfirmationToken == "" {
		t.Fatalf("unexpected conflict payload: %+v", conflict)
	}

	// Owner confirms deletion -> 204!
	del2 := requestJSON(t, router, http.MethodDelete, "/api/v1/songbooks/"+sb.ID+"?confirm_detach_token="+url.QueryEscape(conflict.ConfirmationToken), nil, ownerToken)
	if del2.Code != http.StatusNoContent {
		t.Fatalf("confirmed delete = %d %s, want 204", del2.Code, del2.Body.String())
	}

	// Collab's show is ended & detached, history is preserved & readable without the deleted book!
	evGet := requestJSON(t, router, http.MethodGet, "/api/v1/events/"+ev.ID, nil, collabToken)
	if evGet.Code != http.StatusOK {
		t.Fatalf("collab get detached event = %d %s", evGet.Code, evGet.Body.String())
	}
	var detachedEv models.Event
	_ = json.Unmarshal(evGet.Body.Bytes(), &detachedEv)
	if detachedEv.Status != models.EventStatusEnded || detachedEv.SongbookID != "" || detachedEv.DetachedSongbookName != "Owner Book" {
		t.Fatalf("expected ended detached event, got %+v", detachedEv)
	}
	reqsGet := requestJSON(t, router, http.MethodGet, "/api/v1/events/"+ev.ID+"/requests", nil, collabToken)
	if reqsGet.Code != http.StatusOK || !strings.Contains(reqsGet.Body.String(), "Keep Me In History") {
		t.Fatalf("detached event history missing: %d %s", reqsGet.Code, reqsGet.Body.String())
	}
	// Public view of detached ended event works without fetching deleted songbook!
	if pubGet := requestJSON(t, router, http.MethodGet, "/api/v1/events/"+ev.ID+"/public", nil, ""); pubGet.Code != http.StatusOK {
		t.Fatalf("public view of detached ended event = %d %s", pubGet.Code, pubGet.Body.String())
	}
	// Cannot restart detached show until switching to authorized replacement book.
	if r := requestJSON(t, router, http.MethodPost, "/api/v1/events/"+ev.ID+"/restart", map[string]any{"clear_queue": false}, collabToken); r.Code != http.StatusConflict {
		t.Fatalf("restart detached show = %d, want 409", r.Code)
	}
	if r := requestJSON(t, router, http.MethodPatch, "/api/v1/events/"+ev.ID, map[string]any{"songbook_id": replBook.ID}, collabToken); r.Code != http.StatusOK {
		t.Fatalf("switch detached show to replacement book = %d %s", r.Code, r.Body.String())
	}
	// Fresh read after reassignment must have cleared detached_songbook_name in DynamoDB.
	reattachedGet := requestJSON(t, router, http.MethodGet, "/api/v1/events/"+ev.ID, nil, collabToken)
	if reattachedGet.Code != http.StatusOK {
		t.Fatalf("fresh get after reattach = %d %s", reattachedGet.Code, reattachedGet.Body.String())
	}
	var reattachedEv models.Event
	_ = json.Unmarshal(reattachedGet.Body.Bytes(), &reattachedEv)
	if reattachedEv.SongbookID != replBook.ID || reattachedEv.DetachedSongbookName != "" {
		t.Fatalf("reattached event still has stale detached_songbook_name: %+v", reattachedEv)
	}
	if r := requestJSON(t, router, http.MethodPost, "/api/v1/events/"+ev.ID+"/restart", map[string]any{"clear_queue": false}, collabToken); r.Code != http.StatusOK {
		t.Fatalf("restart after replacement book = %d %s", r.Code, r.Body.String())
	}

	// Collaborator account deletion leaves owner's shared book intact!
	sb2Resp := requestJSON(t, router, http.MethodPost, "/api/v1/songbooks", map[string]any{"name": "Survives Collab Deletion"}, ownerToken)
	var sb2 models.Songbook
	_ = json.Unmarshal(sb2Resp.Body.Bytes(), &sb2)
	requestJSON(t, router, http.MethodPost, "/api/v1/songbooks/"+sb2.ID+"/invitations", map[string]any{
		"email": "collab-del@example.com", "can_edit_catalog": true, "can_host_shows": true,
	}, ownerToken)
	tok2 := extractTokenFromAcceptURL(t, capture.last().AcceptURL)
	requestJSON(t, router, http.MethodPost, "/api/v1/songbook-invitations/accept", map[string]any{"token": tok2}, collabToken)

	if r := requestJSON(t, router, http.MethodDelete, "/api/v1/auth/me", map[string]any{"confirm": "DELETE"}, collabToken); r.Code != http.StatusNoContent {
		t.Fatalf("delete collaborator account = %d %s", r.Code, r.Body.String())
	}
	if r := requestJSON(t, router, http.MethodGet, "/api/v1/songbooks/"+sb2.ID, nil, ownerToken); r.Code != http.StatusOK {
		t.Fatalf("owner book should survive collab account deletion, got %d", r.Code)
	}
	if _, err := s.GetSongbookGrant(context.Background(), sb2.ID, collab.ID); !errors.Is(err, store.ErrGrantNotFound) {
		t.Fatalf("collab grant should be deleted, got err=%v", err)
	}

	// Owner account deletion automatically ends & detaches foreign shows while preserving their queue history!
	collab2Token, _ := registerAndVerifyHost(t, router, s, "collab2-del@example.com", "Collab Two", true, true)
	requestJSON(t, router, http.MethodPost, "/api/v1/songbooks/"+sb2.ID+"/invitations", map[string]any{
		"email": "collab2-del@example.com", "can_host_shows": true,
	}, ownerToken)
	tok3 := extractTokenFromAcceptURL(t, capture.last().AcceptURL)
	requestJSON(t, router, http.MethodPost, "/api/v1/songbook-invitations/accept", map[string]any{"token": tok3}, collab2Token)

	ev2Resp := requestJSON(t, router, http.MethodPost, "/api/v1/events", map[string]any{
		"songbook_id": sb2.ID, "name": "Collab2 Show", "auto_accept": true,
	}, collab2Token)
	var ev2 models.Event
	_ = json.Unmarshal(ev2Resp.Body.Bytes(), &ev2)
	requestJSON(t, router, http.MethodPost, "/api/v1/events/"+ev2.ID+"/requests", map[string]any{
		"song_title": "Preserved After Owner Account Delete", "song_artist": "ABBA", "singer_name": "Eve",
	}, "")

	if r := requestJSON(t, router, http.MethodDelete, "/api/v1/auth/me", map[string]any{"confirm": "DELETE"}, ownerToken); r.Code != http.StatusNoContent {
		t.Fatalf("delete owner account = %d %s", r.Code, r.Body.String())
	}
	ev2Get := requestJSON(t, router, http.MethodGet, "/api/v1/events/"+ev2.ID, nil, collab2Token)
	if ev2Get.Code != http.StatusOK {
		t.Fatalf("collab2 get detached event after owner account deletion = %d %s", ev2Get.Code, ev2Get.Body.String())
	}
	var detached2 models.Event
	_ = json.Unmarshal(ev2Get.Body.Bytes(), &detached2)
	if detached2.Status != models.EventStatusEnded || detached2.SongbookID != "" {
		t.Fatalf("expected ended detached event after owner account delete, got %+v", detached2)
	}
	reqs2 := requestJSON(t, router, http.MethodGet, "/api/v1/events/"+ev2.ID+"/requests", nil, collab2Token)
	if reqs2.Code != http.StatusOK || !strings.Contains(reqs2.Body.String(), "Preserved After Owner Account Delete") {
		t.Fatalf("collab2 queue history lost after owner account deletion: %d %s", reqs2.Code, reqs2.Body.String())
	}
}

func TestDynamoSongbookCollaboration_ControlledConcurrencyAndRecovery(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	owner := helperCreateUser(t, s, "")
	owner.IsHost, owner.EmailVerified = true, true
	_ = s.UpdateUser(ctx, owner)

	collab := helperCreateUser(t, s, "")
	collab.IsHost, collab.EmailVerified = true, true
	_ = s.UpdateUser(ctx, collab)

	sb := helperCreateSongbook(t, s, &models.Songbook{ID: uuid.NewString(), OwnerID: owner.ID, Name: "Race Book"})

	// 1. Accept vs Cancel race: exactly one wins!
	rawTok := "race-accept-cancel-token"
	inv := &models.SongbookInvitation{
		ID:             uuid.NewString(),
		SongbookID:     sb.ID,
		OwnerID:        owner.ID,
		RecipientEmail: collab.Email,
		CanEditCatalog: true,
		CanHostShows:   true,
		TokenHash:      songbook.HashInvitationToken(rawTok),
		ExpiresAt:      time.Now().UTC().Add(time.Hour),
	}
	if err := s.CreateSongbookInvitation(ctx, inv, nil); err != nil {
		t.Fatalf("create invite: %v", err)
	}
	start := make(chan struct{})
	var acceptErr, cancelErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, _, acceptErr = s.AcceptSongbookInvitation(ctx, inv.TokenHash, collab.ID, collab.Email, time.Now().UTC())
	}()
	go func() {
		defer wg.Done()
		<-start
		cancelErr = s.CancelSongbookInvitation(ctx, sb.ID, inv.ID)
	}()
	close(start)
	wg.Wait()
	if (acceptErr == nil) == (cancelErr == nil) {
		t.Fatalf("accept vs cancel must have exactly one winner: acceptErr=%v cancelErr=%v", acceptErr, cancelErr)
	}
	if cancelErr == nil {
		if _, err := s.GetSongbookGrant(ctx, sb.ID, collab.ID); !errors.Is(err, store.ErrGrantNotFound) {
			t.Fatalf("cancel won but grant exists: %v", err)
		}
	}

	// Ensure collab has a grant for revoke/edit and revoke/start tests.
	if acceptErr != nil {
		rawTok2 := "post-race-token"
		inv2 := &models.SongbookInvitation{
			ID: uuid.NewString(), SongbookID: sb.ID, OwnerID: owner.ID, RecipientEmail: collab.Email,
			CanEditCatalog: true, CanHostShows: true, TokenHash: songbook.HashInvitationToken(rawTok2),
			ExpiresAt: time.Now().UTC().Add(time.Hour),
		}
		_ = s.CreateSongbookInvitation(ctx, inv2, nil)
		if _, _, err := s.AcceptSongbookInvitation(ctx, inv2.TokenHash, collab.ID, collab.Email, time.Now().UTC()); err != nil {
			t.Fatalf("re-accept: %v", err)
		}
	}

	// 2. Revoke vs Edit (PutCatalogChange commit fence): revoke first, then fenced commit fails with ErrPermissionDenied.
	if err := s.DeleteSongbookGrant(ctx, sb.ID, collab.ID); err != nil {
		t.Fatalf("revoke grant: %v", err)
	}
	editCtx := store.WithSongbookEditActor(ctx, collab.ID)
	if err := s.PutCatalogChange(editCtx, &models.CatalogChange{
		SongbookID: sb.ID, SongID: "s-revoked", Song: &models.Song{ID: "s-revoked", Artist: "A", Title: "B"},
	}); !errors.Is(err, store.ErrPermissionDenied) {
		t.Fatalf("fenced PutCatalogChange after revoke = %v, want ErrPermissionDenied", err)
	}

	// 3. Revoke vs Start Show (CreateEvent commit fence): fenced CreateEvent fails with ErrPermissionDenied.
	hostCtx := store.WithSongbookHostFence(ctx, store.SongbookHostFence{SongbookID: sb.ID, ActorID: collab.ID, IsOwner: false})
	if err := s.CreateEvent(hostCtx, &models.Event{
		ID: uuid.NewString(), OwnerID: collab.ID, SongbookID: sb.ID, Name: "Raced Show", JoinCode: "RACE1",
	}); !errors.Is(err, store.ErrPermissionDenied) {
		t.Fatalf("fenced CreateEvent after revoke = %v, want ErrPermissionDenied", err)
	}

	// 4. Delete vs Create/Reassign & Mid-Cleanup Recovery!
	// Re-grant host access and create one show; observe confirmation token, then race a second show before deletion!
	rawTok3 := "del-race-token"
	inv3 := &models.SongbookInvitation{
		ID: uuid.NewString(), SongbookID: sb.ID, OwnerID: owner.ID, RecipientEmail: collab.Email,
		CanHostShows: true, TokenHash: songbook.HashInvitationToken(rawTok3), ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	_ = s.CreateSongbookInvitation(ctx, inv3, nil)
	_, _, _ = s.AcceptSongbookInvitation(ctx, inv3.TokenHash, collab.ID, collab.Email, time.Now().UTC())

	ev1 := &models.Event{ID: uuid.NewString(), OwnerID: collab.ID, SongbookID: sb.ID, Name: "Show 1", JoinCode: "SHW1"}
	if err := s.CreateEvent(hostCtx, ev1); err != nil {
		t.Fatalf("create ev1: %v", err)
	}
	sbSvc := songbook.NewService(s, s)
	sbSvc.Access = s
	sbSvc.CatalogChanges = s
	sbSvc.ImportJobs = s
	sbSvc.Requests = s
	sbSvc.Catalogs = s

	// First check returns token for 1 show.
	err := sbSvc.DeleteSongbookWithConfirmation(ctx, owner.ID, sb.ID, "")
	var fErr *songbook.ForeignEventsAssociatedError
	if !errors.As(err, &fErr) || fErr.AffectedShowsCount != 1 {
		t.Fatalf("expected 1 foreign show conflict, got %v", err)
	}
	staleToken := fErr.ConfirmationToken

	// A second show is created before owner submits staleToken -> MUST NOT silently end Show 2!
	ev2 := &models.Event{ID: uuid.NewString(), OwnerID: collab.ID, SongbookID: sb.ID, Name: "Show 2", JoinCode: "SHW2"}
	if err := s.CreateEvent(hostCtx, ev2); err != nil {
		t.Fatalf("create ev2: %v", err)
	}
	err = sbSvc.DeleteSongbookWithConfirmation(ctx, owner.ID, sb.ID, staleToken)
	var fErr2 *songbook.ForeignEventsAssociatedError
	if !errors.As(err, &fErr2) || fErr2.AffectedShowsCount != 2 {
		t.Fatalf("expected updated 2 foreign show conflict, got %v", err)
	}

	// Simulate mid-cleanup interruption: set barrier + confirm, then resume via DeleteSongbookWithConfirmation!
	if _, err := s.BeginSongbookDeletion(ctx, sb.ID); err != nil {
		t.Fatalf("begin deletion: %v", err)
	}
	if err := s.ConfirmSongbookDeletion(ctx, sb.ID); err != nil {
		t.Fatalf("confirm deletion: %v", err)
	}
	// Concurrent CreateEvent while deleting is fenced out!
	if err := s.CreateEvent(hostCtx, &models.Event{
		ID: uuid.NewString(), OwnerID: collab.ID, SongbookID: sb.ID, Name: "Blocked By Barrier", JoinCode: "BLK1",
	}); !errors.Is(err, store.ErrSongbookDeleting) {
		t.Fatalf("CreateEvent during deleting barrier = %v, want ErrSongbookDeleting", err)
	}
	// Resume cleanup to completion!
	if err := sbSvc.DeleteSongbookWithConfirmation(ctx, owner.ID, sb.ID, ""); err != nil {
		t.Fatalf("resume deletion cleanup: %v", err)
	}
	if _, err := s.GetSongbook(ctx, sb.ID); !errors.Is(err, store.ErrSongbookDeleting) && err == nil {
		t.Fatalf("expected deleted songbook")
	}
	for _, id := range []string{ev1.ID, ev2.ID} {
		ev, err := s.GetEvent(ctx, id)
		if err != nil || ev.Status != models.EventStatusEnded || ev.SongbookID != "" {
			t.Fatalf("expected detached ended event %s, got %+v err=%v", id, ev, err)
		}
	}

	// 5. Review regressions: mixed-case user email + fractional-second expiry + concurrent fenced generation allocation.
	sbGen := helperCreateSongbook(t, s, &models.Songbook{ID: uuid.NewString(), OwnerID: owner.ID, Name: "Gen Book"})
	mixedUser := &models.User{
		ID:            uuid.NewString(),
		Email:         "MixedCase." + uuid.NewString()[:8] + "@Example.COM",
		DisplayName:   "Mixed Case",
		IsHost:        true,
		EmailVerified: true,
	}
	if err := s.CreateUser(ctx, mixedUser); err != nil {
		t.Fatalf("create mixed-case user: %v", err)
	}
	nowExactSec := time.Now().UTC().Truncate(time.Second)
	expFrac := nowExactSec.Add(500 * time.Millisecond)
	invMixed := &models.SongbookInvitation{
		ID:             uuid.NewString(),
		SongbookID:     sbGen.ID,
		OwnerID:        owner.ID,
		RecipientEmail: strings.ToLower(mixedUser.Email),
		CanEditCatalog: true,
		TokenHash:      songbook.HashInvitationToken("mixed-case-token"),
		ExpiresAt:      expFrac,
	}
	if err := s.CreateSongbookInvitation(ctx, invMixed, nil); err != nil {
		t.Fatalf("create mixed-case invite: %v", err)
	}
	if _, _, err := s.AcceptSongbookInvitation(ctx, invMixed.TokenHash, mixedUser.ID, mixedUser.Email, nowExactSec); err != nil {
		t.Fatalf("mixed-case + exact-second accept failed: %v", err)
	}

	// Concurrent fenced NextCatalogGeneration must allocate strictly distinct monotonic generations.
	genCtx := store.WithSongbookEditActor(ctx, mixedUser.ID)
	genStart := make(chan struct{})
	var gen1, gen2 int64
	var gErr1, gErr2 error
	var genWG sync.WaitGroup
	genWG.Add(2)
	go func() {
		defer genWG.Done()
		<-genStart
		gen1, gErr1 = s.NextCatalogGeneration(genCtx, sbGen.ID)
	}()
	go func() {
		defer genWG.Done()
		<-genStart
		gen2, gErr2 = s.NextCatalogGeneration(genCtx, sbGen.ID)
	}()
	close(genStart)
	genWG.Wait()
	if gErr1 != nil || gErr2 != nil || gen1 == gen2 || gen1 <= 0 || gen2 <= 0 {
		t.Fatalf("concurrent fenced NextCatalogGeneration = (%d, %v) and (%d, %v); want distinct positive generations", gen1, gErr1, gen2, gErr2)
	}
}

func requestJSONWithBody(t *testing.T, router http.Handler, method, path string, body []byte, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

type controlledInviteSender struct {
	mu      sync.Mutex
	calls   int
	hook    func(call int, msg email.SongbookInvitationEmail) error
	capture []email.SongbookInvitationEmail
}

func (c *controlledInviteSender) SendVerificationEmail(context.Context, string, string, string) error {
	return nil
}
func (c *controlledInviteSender) SendPasswordResetEmail(context.Context, string, string, string) error {
	return nil
}
func (c *controlledInviteSender) SendSongbookInvitationEmail(_ context.Context, msg email.SongbookInvitationEmail) error {
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.capture = append(c.capture, msg)
	hook := c.hook
	c.mu.Unlock()
	if hook != nil {
		return hook(call, msg)
	}
	return nil
}

type failingDeliveryAccessStore struct {
	store.SongbookAccessStore
	failErr error
}

func (f *failingDeliveryAccessStore) RecordSongbookInvitationDelivery(context.Context, string, string, *time.Time, string) error {
	return f.failErr
}

func TestDynamoSongbookCollaboration_DeliveryAttemptOrderingAndPersistenceErrors(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	owner := &models.User{
		ID:            uuid.NewString(),
		Email:         "delivery-owner@example.com",
		DisplayName:   "Delivery Owner",
		IsHost:        true,
		EmailVerified: true,
	}
	if err := s.CreateUser(ctx, owner); err != nil {
		t.Fatalf("create owner: %v", err)
	}
	sb := &models.Songbook{
		ID:      uuid.NewString(),
		OwnerID: owner.ID,
		Name:    "Delivery Race Book",
	}
	if err := s.CreateSongbook(ctx, sb); err != nil {
		t.Fatalf("create songbook: %v", err)
	}

	// 1. Controlled interleaving: an older send attempt succeeds AFTER a newer resend fails.
	attempt1Started := make(chan struct{})
	releaseAttempt1 := make(chan struct{})
	sender := &controlledInviteSender{
		hook: func(call int, _ email.SongbookInvitationEmail) error {
			switch call {
			case 1:
				close(attempt1Started)
				<-releaseAttempt1
				return nil
			case 2:
				return errors.New("ses transient failure on resend")
			default:
				return nil
			}
		},
	}

	sbSvc := &songbook.Service{
		Songbooks:   s,
		Events:      s,
		Access:      s,
		Auth:        s,
		EmailSender: sender,
	}

	var inv1 *models.SongbookInvitation
	var err1 error
	done1 := make(chan struct{})
	go func() {
		defer close(done1)
		inv1, err1 = sbSvc.CreateInvitation(ctx, owner, sb.ID, "race-collab@example.com", false, true, "https://micup.live")
	}()

	// Wait until Attempt 1 has persisted its token and entered SendSongbookInvitationEmail.
	<-attempt1Started

	// Locate the persisted pending invitation and trigger a newer resend (Attempt 2) that fails.
	overviewBefore, err := sbSvc.GetAccessOverview(ctx, owner.ID, sb.ID)
	if err != nil || len(overviewBefore.Invitations) != 1 {
		t.Fatalf("expected 1 pending invite before resend: overview=%+v err=%v", overviewBefore, err)
	}
	invID := overviewBefore.Invitations[0].ID

	inv2, err2 := sbSvc.ResendInvitation(ctx, owner, sb.ID, invID, "https://micup.live")
	if err2 != nil {
		t.Fatalf("resend attempt 2 returned unexpected error: %v", err2)
	}
	if inv2.SendError == "" || inv2.LastSentAt != nil {
		t.Fatalf("attempt 2 should record send_error and clear last_sent_at; got send_error=%q last_sent_at=%v", inv2.SendError, inv2.LastSentAt)
	}

	// Now release Attempt 1, which succeeds sending the superseded token.
	close(releaseAttempt1)
	<-done1
	if err1 != nil {
		t.Fatalf("attempt 1 returned unexpected error: %v", err1)
	}
	if inv1.SendError == "" {
		t.Fatalf("attempt 1 reloaded state should reflect newer failure, got empty send_error")
	}

	// Simulate Manage Access refresh: persisted state must still show Attempt 2's failure so UI offers Retry.
	refreshed, err := sbSvc.GetAccessOverview(ctx, owner.ID, sb.ID)
	if err != nil || len(refreshed.Invitations) != 1 {
		t.Fatalf("refresh access overview failed: %+v err=%v", refreshed, err)
	}
	gotInv := refreshed.Invitations[0]
	if gotInv.SendError != "ses transient failure on resend" {
		t.Fatalf("refreshed send_error = %q, want %q (older send overwrote newer resend!)", gotInv.SendError, "ses transient failure on resend")
	}
	if gotInv.LastSentAt != nil {
		t.Fatalf("refreshed last_sent_at = %v, want nil", gotInv.LastSentAt)
	}

	// Retry (Attempt 3) succeeds, clears send_error, and sets last_sent_at.
	inv3, err3 := sbSvc.ResendInvitation(ctx, owner, sb.ID, invID, "https://micup.live")
	if err3 != nil {
		t.Fatalf("retry attempt 3 failed: %v", err3)
	}
	if inv3.SendError != "" || inv3.LastSentAt == nil {
		t.Fatalf("attempt 3 should clear send_error and set last_sent_at; got send_error=%q last_sent_at=%v", inv3.SendError, inv3.LastSentAt)
	}

	// 2. Failures to persist delivery status must be surfaced, never silently ignored.
	persistErr := errors.New("dynamodb write timeout")
	failingSvc := &songbook.Service{
		Songbooks:   s,
		Events:      s,
		Access:      &failingDeliveryAccessStore{SongbookAccessStore: s, failErr: persistErr},
		Auth:        s,
		EmailSender: &controlledInviteSender{},
	}
	if _, err := failingSvc.CreateInvitation(ctx, owner, sb.ID, "persist-fail@example.com", true, false, "https://micup.live"); !errors.Is(err, persistErr) {
		t.Fatalf("CreateInvitation on delivery-persist failure (send ok) = %v, want %v", err, persistErr)
	}
	failingSendSvc := &songbook.Service{
		Songbooks: s,
		Events:    s,
		Access:    &failingDeliveryAccessStore{SongbookAccessStore: s, failErr: persistErr},
		Auth:      s,
		EmailSender: &controlledInviteSender{
			hook: func(int, email.SongbookInvitationEmail) error { return errors.New("ses down") },
		},
	}
	if _, err := failingSendSvc.ResendInvitation(ctx, owner, sb.ID, invID, "https://micup.live"); !errors.Is(err, persistErr) {
		t.Fatalf("ResendInvitation on delivery-persist failure (send err) = %v, want %v", err, persistErr)
	}
	// After refresh, the invitation whose resend failed to persist has last_sent_at == nil so Manage Access still offers Retry.
	afterFail, err := sbSvc.GetAccessOverview(ctx, owner.ID, sb.ID)
	if err != nil {
		t.Fatalf("refresh after persist failure: %v", err)
	}
	var foundResent *models.SongbookInvitation
	for _, inv := range afterFail.Invitations {
		if inv.ID == invID {
			foundResent = inv
		}
	}
	if foundResent == nil || foundResent.LastSentAt != nil {
		t.Fatalf("expected resent invite with nil last_sent_at after persist failure; got %+v", foundResent)
	}

	// 3. When a superseded send fails to reload the current invitation, propagate the reload error.
	reloadErr := errors.New("dynamodb read timeout on reload")
	supersededReloadSvc := &songbook.Service{
		Songbooks:   s,
		Events:      s,
		Access:      &failingSupersededReloadStore{SongbookAccessStore: s, reloadErr: reloadErr},
		Auth:        s,
		EmailSender: &controlledInviteSender{},
	}
	if _, err := supersededReloadSvc.CreateInvitation(ctx, owner, sb.ID, "superseded-reload@example.com", true, false, "https://micup.live"); !errors.Is(err, reloadErr) {
		t.Fatalf("CreateInvitation on superseded reload error = %v, want %v", err, reloadErr)
	}
}

type failingSupersededReloadStore struct {
	store.SongbookAccessStore
	reloadErr error
}

func (f *failingSupersededReloadStore) RecordSongbookInvitationDelivery(context.Context, string, string, *time.Time, string) error {
	return store.ErrInvitationDeliverySuperseded
}

func (f *failingSupersededReloadStore) GetSongbookInvitation(context.Context, string) (*models.SongbookInvitation, error) {
	return nil, f.reloadErr
}

func TestDynamoSongbookCollaboration_ConcurrentInvitesAndPartialDetachRecovery(t *testing.T) {
	s := newLocalStore(t)
	ctx := context.Background()

	owner := &models.User{ID: uuid.NewString(), Email: "conc-owner@example.com", DisplayName: "Conc Owner", IsHost: true, EmailVerified: true}
	collab := &models.User{ID: uuid.NewString(), Email: "conc-collab@example.com", DisplayName: "Conc Collab", IsHost: true, EmailVerified: true}
	if err := s.CreateUser(ctx, owner); err != nil {
		t.Fatalf("create owner: %v", err)
	}
	if err := s.CreateUser(ctx, collab); err != nil {
		t.Fatalf("create collab: %v", err)
	}
	sb := &models.Songbook{ID: uuid.NewString(), OwnerID: owner.ID, Name: "Concurrent Invite Book"}
	if err := s.CreateSongbook(ctx, sb); err != nil {
		t.Fatalf("create songbook: %v", err)
	}

	// 1. Overlapping invitations for the same book and recipient with different permissions:
	// commit-time pending slot guarantees exactly ONE valid pending link survives.
	capture := &collabEmailCapture{}
	sbSvc := &songbook.Service{Songbooks: s, Events: s, Access: s, Auth: s, EmailSender: capture}
	start := make(chan struct{})
	var invA, invB *models.SongbookInvitation
	var errA, errB error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		invA, errA = sbSvc.CreateInvitation(ctx, owner, sb.ID, collab.Email, true, false, "https://micup.live")
	}()
	go func() {
		defer wg.Done()
		<-start
		invB, errB = sbSvc.CreateInvitation(ctx, owner, sb.ID, collab.Email, false, true, "https://micup.live")
	}()
	close(start)
	wg.Wait()
	if errA != nil || errB != nil {
		t.Fatalf("overlapping CreateInvitation failed: errA=%v errB=%v", errA, errB)
	}

	overview, err := sbSvc.GetAccessOverview(ctx, owner.ID, sb.ID)
	if err != nil {
		t.Fatalf("GetAccessOverview: %v", err)
	}
	if len(overview.Invitations) != 1 {
		t.Fatalf("expected exactly 1 pending invitation after concurrent sends, got %d", len(overview.Invitations))
	}
	winner := overview.Invitations[0]

	// Extract both emailed tokens: only the winning invitation's token can be accepted.
	capture.mu.Lock()
	sentEmails := append([]email.SongbookInvitationEmail(nil), capture.invites...)
	capture.mu.Unlock()
	if len(sentEmails) != 2 {
		t.Fatalf("expected 2 emailed links, got %d", len(sentEmails))
	}
	acceptedCount := 0
	for _, msg := range sentEmails {
		tok := extractTokenFromAcceptURL(t, msg.AcceptURL)
		grant, _, accErr := sbSvc.AcceptInvitation(ctx, tok, collab)
		if accErr == nil {
			acceptedCount++
			if grant.CanEditCatalog != winner.CanEditCatalog || grant.CanHostShows != winner.CanHostShows {
				t.Fatalf("accepted grant (%v,%v) != winner (%v,%v)", grant.CanEditCatalog, grant.CanHostShows, winner.CanEditCatalog, winner.CanHostShows)
			}
		}
	}
	if acceptedCount != 1 {
		t.Fatalf("expected exactly 1 token to be accepted, got %d (invA=%s invB=%s winner=%s)", acceptedCount, invA.ID, invB.ID, winner.ID)
	}

	// Creating an invitation when the recipient already holds a grant must fail at commit time.
	dupRaw := "dup-grant-token-12345"
	dupInv := &models.SongbookInvitation{
		ID:             uuid.NewString(),
		SongbookID:     sb.ID,
		OwnerID:        owner.ID,
		RecipientEmail: collab.Email,
		CanEditCatalog: true,
		CanHostShows:   true,
		TokenHash:      songbook.HashInvitationToken(dupRaw),
		ExpiresAt:      time.Now().UTC().Add(time.Hour),
	}
	if err := s.CreateSongbookInvitation(ctx, dupInv, nil); !errors.Is(err, store.ErrAlreadyCollaborator) {
		t.Fatalf("CreateSongbookInvitation for existing collaborator = %v, want ErrAlreadyCollaborator", err)
	}

	// 1b. Controlled race: Invite #1 is accepted while Invite #2 is in-flight after its pre-check.
	// Removing the collaborator afterward must leave NO usable invitation link.
	if err := sbSvc.RemoveMember(ctx, owner.ID, sb.ID, collab.ID); err != nil {
		t.Fatalf("remove collab before accept-during-send race: %v", err)
	}
	capture.mu.Lock()
	capture.invites = nil
	capture.mu.Unlock()

	invFirst, err := sbSvc.CreateInvitation(ctx, owner, sb.ID, collab.Email, false, true, "https://micup.live")
	if err != nil {
		t.Fatalf("create invite #1: %v", err)
	}
	tokFirst := extractTokenFromAcceptURL(t, capture.last().AcceptURL)

	invite2AtCreate := make(chan struct{})
	releaseInvite2 := make(chan struct{})
	var hookOnce sync.Once
	hookedAccess := &beforeCreateInviteStore{
		SongbookAccessStore: s,
		onBeforeCreate: func() {
			hookOnce.Do(func() {
				close(invite2AtCreate)
				<-releaseInvite2
			})
		},
	}
	racedSvc := &songbook.Service{Songbooks: s, Events: s, Access: hookedAccess, Auth: s, EmailSender: capture}
	var errSecond error
	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		_, errSecond = racedSvc.CreateInvitation(ctx, owner, sb.ID, collab.Email, true, true, "https://micup.live")
	}()

	<-invite2AtCreate
	// While Invite #2 is past its pre-check and about to commit, accept Invite #1.
	if _, _, err := sbSvc.AcceptInvitation(ctx, tokFirst, collab); err != nil {
		t.Fatalf("accept invite #1 during invite #2 send: %v", err)
	}
	close(releaseInvite2)
	<-secondDone
	if !errors.Is(errSecond, store.ErrAlreadyCollaborator) && !errors.Is(errSecond, songbook.ErrAlreadyCollaborator) {
		t.Fatalf("invite #2 committed despite concurrent acceptance: err=%v (want ErrAlreadyCollaborator)", errSecond)
	}
	// Remove the collaborator and verify NO pending invitation exists and neither link restores access.
	if err := sbSvc.RemoveMember(ctx, owner.ID, sb.ID, collab.ID); err != nil {
		t.Fatalf("remove collab after race: %v", err)
	}
	afterRemove, err := sbSvc.GetAccessOverview(ctx, owner.ID, sb.ID)
	if err != nil || len(afterRemove.Invitations) != 0 || len(afterRemove.Members) != 0 {
		t.Fatalf("expected 0 members and 0 pending invites after removal, got %+v err=%v", afterRemove, err)
	}
	if _, _, err := sbSvc.AcceptInvitation(ctx, tokFirst, collab); err == nil {
		t.Fatalf("expected invite #1 token to remain unusable after removal")
	}
	_ = invFirst

	// 1c. Atomic DeleteSongbookInvitation: preserves newer invite's slot, removes active invite's slot, propagates errors.
	sbSlot := &models.Songbook{ID: uuid.NewString(), OwnerID: owner.ID, Name: "Slot Cleanup Book"}
	if err := s.CreateSongbook(ctx, sbSlot); err != nil {
		t.Fatalf("create slot book: %v", err)
	}
	oldInv, err := sbSvc.CreateInvitation(ctx, owner, sbSlot.ID, collab.Email, false, true, "https://micup.live")
	if err != nil {
		t.Fatalf("create old slot invite: %v", err)
	}
	newInv, err := sbSvc.CreateInvitation(ctx, owner, sbSlot.ID, collab.Email, true, false, "https://micup.live")
	if err != nil {
		t.Fatalf("create replacement slot invite: %v", err)
	}
	// Deleting the replaced older invite must preserve the newer invite's pending slot.
	if err := s.DeleteSongbookInvitation(ctx, oldInv.ID); err != nil {
		t.Fatalf("delete replaced invite: %v", err)
	}
	slotKey := pendingInvitationSlotID(sbSlot.ID, collab.Email)
	slotOut, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(s.tables.songbookInvitations),
		Key:            stringKey("id", slotKey),
		ConsistentRead: boolValue(true),
	})
	if err != nil || len(slotOut.Item) == 0 {
		t.Fatalf("expected pending slot preserved after deleting replaced invite; err=%v item=%v", err, slotOut)
	}
	// Deleting the active pending invite must atomically remove its pending slot (no invitee email retained).
	if err := s.DeleteSongbookInvitation(ctx, newInv.ID); err != nil {
		t.Fatalf("delete active invite: %v", err)
	}
	slotAfter, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(s.tables.songbookInvitations),
		Key:            stringKey("id", slotKey),
		ConsistentRead: boolValue(true),
	})
	if err != nil || len(slotAfter.Item) != 0 {
		t.Fatalf("expected pending slot removed atomically with active invite; err=%v item=%v", err, slotAfter)
	}
	canceledCtx, cancelFn := context.WithCancel(ctx)
	cancelFn()
	if err := s.DeleteSongbookInvitation(canceledCtx, newInv.ID); err == nil {
		t.Fatalf("expected DeleteSongbookInvitation to propagate storage/context error")
	}

	// 2. Partial detach failure between ending the show and setting request retention TTL:
	// - Exact original retention deadline (e.g. 7 days) must be preserved even if the
	//   retention setting changes (to 14 days) before the retry.
	// - Reassigning the show through the real HTTP router while detach_in_progress is set
	//   must return 409 Conflict (never 500), and succeed (200) once detach finishes.
	router := newCollabRouter(t, s, capture, 100)
	collabToken, collabHTTPUser := registerAndVerifyHost(t, router, s, "detach-collab@example.com", "Detach Collab", true, true)
	otherBook := &models.Songbook{ID: uuid.NewString(), OwnerID: collabHTTPUser.ID, Name: "Collab Replacement Book"}
	if err := s.CreateSongbook(ctx, otherBook); err != nil {
		t.Fatalf("create otherBook: %v", err)
	}
	origEndedAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	origDeadline := origEndedAt.Add(7 * 24 * time.Hour).Unix()
	ev := &models.Event{
		ID:         uuid.NewString(),
		OwnerID:    collabHTTPUser.ID,
		SongbookID: sb.ID,
		Name:       "Partial Detach Show",
		VenueName:  "Club",
		JoinCode:   "PRTDET",
		Status:     models.EventStatusActive,
	}
	if err := s.CreateEvent(ctx, ev); err != nil {
		t.Fatalf("create collab show: %v", err)
	}
	req := &models.SongRequest{ID: uuid.NewString(), EventID: ev.ID, SongbookID: sb.ID, SongTitle: "Must Expire", SongArtist: "Band", SingerName: "Pat", Status: models.StatusAccepted}
	if err := s.CreateRequest(ctx, req); err != nil {
		t.Fatalf("create request: %v", err)
	}
	// End the show with an explicit 7-day retention deadline before book deletion begins.
	evCur, err := s.GetEvent(ctx, ev.ID)
	if err != nil {
		t.Fatalf("reload event: %v", err)
	}
	evCur.Status = models.EventStatusEnded
	evCur.EndedAt = &origEndedAt
	evCur.TTL = origDeadline
	if err := s.UpdateEventWithVersion(ctx, evCur, true); err != nil {
		t.Fatalf("end event with 7d deadline: %v", err)
	}

	delSvc := &songbook.Service{Songbooks: s, Events: s, Access: s, Auth: s, QueueRetention: 7 * 24 * time.Hour}
	err = delSvc.DeleteSongbookWithConfirmation(ctx, owner.ID, sb.ID, "")
	var foreignErr *songbook.ForeignEventsAssociatedError
	if !errors.As(err, &foreignErr) {
		t.Fatalf("expected ForeignEventsAssociatedError, got %v", err)
	}

	// Inject a failure right after Phase 1 saves pending_detach_ttl and before Phase 2 sets request TTLs.
	s.testDetachRetentionHook = func(context.Context, string, time.Time) error {
		return errors.New("injected retention write failure")
	}
	if err := delSvc.DeleteSongbookWithConfirmation(ctx, owner.ID, sb.ID, foreignErr.ConfirmationToken); err == nil {
		t.Fatalf("expected delete to fail when retention hook fails")
	}

	// Verify the show is ended, STILL linked to sb.ID, has TTL == 0 (cannot expire prematurely),
	// preserves PendingDetachTTL == origDeadline, and returns 409 Conflict on router reassignment.
	midEv, err := s.GetEvent(ctx, ev.ID)
	if err != nil || midEv.Status != models.EventStatusEnded || midEv.SongbookID != sb.ID || midEv.TTL != 0 || !midEv.Detaching || midEv.PendingDetachTTL != origDeadline {
		t.Fatalf("expected detaching event linked to %s with TTL=0 and PendingDetachTTL=%d, got %+v err=%v", sb.ID, origDeadline, midEv, err)
	}
	if r := requestJSON(t, router, http.MethodPatch, "/api/v1/events/"+ev.ID, map[string]any{"songbook_id": otherBook.ID}, collabToken); r.Code != http.StatusConflict {
		t.Fatalf("router reassignment during detach_in_progress = %d %s, want 409", r.Code, r.Body.String())
	}
	midReq, err := s.GetRequest(ctx, req.ID)
	if err != nil || midReq.TTL != 0 {
		t.Fatalf("expected request TTL=0 before retry, got %+v err=%v", midReq, err)
	}

	// Change retention setting from 7d to 14d before retrying deletion: retry MUST reuse origDeadline (7d)
	// on BOTH the event and its request rows, clear songbook_id, lift detach_in_progress, and delete sb.
	s.testDetachRetentionHook = nil
	delSvc.QueueRetention = 14 * 24 * time.Hour
	if err := delSvc.DeleteSongbookWithConfirmation(ctx, owner.ID, sb.ID, foreignErr.ConfirmationToken); err != nil {
		t.Fatalf("retry deletion after retention failure failed: %v", err)
	}
	finalEv, err := s.GetEvent(ctx, ev.ID)
	if err != nil || finalEv.SongbookID != "" || finalEv.Status != models.EventStatusEnded || finalEv.DetachedSongbookName != sb.Name || finalEv.TTL != origDeadline || finalEv.Detaching || finalEv.PendingDetachTTL != 0 {
		t.Fatalf("expected fully detached event with original TTL=%d, got %+v err=%v", origDeadline, finalEv, err)
	}
	finalReq, err := s.GetRequest(ctx, req.ID)
	if err != nil || finalReq.TTL != origDeadline {
		t.Fatalf("expected request TTL=%d (original deadline), got %+v err=%v", origDeadline, finalReq, err)
	}
	if _, err := s.GetSongbook(ctx, sb.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected songbook deleted after retry, got %v", err)
	}
	// Now that detach completed, reassigning the detached show through the router succeeds (200).
	if r := requestJSON(t, router, http.MethodPatch, "/api/v1/events/"+ev.ID, map[string]any{"songbook_id": otherBook.ID}, collabToken); r.Code != http.StatusOK {
		t.Fatalf("router reassignment after completed detach = %d %s, want 200", r.Code, r.Body.String())
	}
}

type beforeCreateInviteStore struct {
	store.SongbookAccessStore
	onBeforeCreate func()
}

func (b *beforeCreateInviteStore) CreateSongbookInvitation(ctx context.Context, inv *models.SongbookInvitation, replaceIDs []string) error {
	if b.onBeforeCreate != nil {
		b.onBeforeCreate()
	}
	return b.SongbookAccessStore.CreateSongbookInvitation(ctx, inv, replaceIDs)
}
