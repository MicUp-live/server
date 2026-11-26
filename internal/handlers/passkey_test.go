package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type passkeyTestStore struct {
	mu          sync.Mutex
	authStore   *authTestStore
	passkeys    map[string]*models.PasskeyCredential
	userKeys    map[string][]*models.PasskeyCredential
	webSessions map[string]*models.WebAuthnSession
}

func newPasskeyTestStore(auth *authTestStore) *passkeyTestStore {
	return &passkeyTestStore{
		authStore:   auth,
		passkeys:    make(map[string]*models.PasskeyCredential),
		userKeys:    make(map[string][]*models.PasskeyCredential),
		webSessions: make(map[string]*models.WebAuthnSession),
	}
}

func (s *passkeyTestStore) CreateUserWithPasskey(ctx context.Context, user *models.User, cred *models.PasskeyCredential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.authStore.CreateUser(ctx, user); err != nil {
		return err
	}
	s.passkeys[cred.ID] = cred
	s.userKeys[cred.UserID] = append(s.userKeys[cred.UserID], cred)
	return nil
}

func (s *passkeyTestStore) SavePasskeyCredential(_ context.Context, cred *models.PasskeyCredential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.passkeys[cred.ID] = cred
	s.userKeys[cred.UserID] = append(s.userKeys[cred.UserID], cred)
	return nil
}

func (s *passkeyTestStore) GetPasskeyCredential(_ context.Context, id string) (*models.PasskeyCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.passkeys[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	return c, nil
}

func (s *passkeyTestStore) ListPasskeyCredentialsForUser(_ context.Context, userID string) ([]*models.PasskeyCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []*models.PasskeyCredential
	for _, cred := range s.userKeys[userID] {
		snapshot := *cred
		result = append(result, &snapshot)
	}
	return result, nil
}

func (s *passkeyTestStore) RecordPasskeyUse(_ context.Context, observed *models.PasskeyCredential, usage store.PasskeyUsage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authStore.mu.Lock()
	defer s.authStore.mu.Unlock()
	user, err := s.authStore.getUser(observed.UserID)
	if err != nil || user.Deleting {
		return store.ErrAccountDeleting
	}
	c := s.passkeys[observed.ID]
	if c == nil || c.UserID != observed.UserID || !bytes.Equal(c.PublicKey, observed.PublicKey) || c.SignCount != observed.SignCount || c.BackupEligible != observed.BackupEligible {
		return store.ErrPasskeyChanged
	}
	c.SignCount = usage.SignCount
	c.BackupEligible = usage.BackupEligible
	c.BackupState = usage.BackupState
	c.LastUsedAt = time.Now()
	return nil
}

func (s *passkeyTestStore) DeletePasskeyCredential(_ context.Context, id string, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.passkeys[id]
	if !ok || c.UserID != userID {
		return os.ErrNotExist
	}
	delete(s.passkeys, id)
	filtered := make([]*models.PasskeyCredential, 0)
	for _, k := range s.userKeys[userID] {
		if k.ID != id {
			filtered = append(filtered, k)
		}
	}
	s.userKeys[userID] = filtered
	return nil
}

func (s *passkeyTestStore) SaveWebAuthnSession(_ context.Context, session *models.WebAuthnSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.webSessions[session.SessionID] = session
	return nil
}

func (s *passkeyTestStore) ConsumeWebAuthnSession(_ context.Context, sessionID string) (*models.WebAuthnSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.webSessions[sessionID]
	if !ok {
		return nil, os.ErrNotExist
	}
	delete(s.webSessions, sessionID)
	return sess, nil
}

func TestPasskeyHandler_RegisterAndLoginOptions(t *testing.T) {
	authStore := newAuthTestStore()
	pkStore := newPasskeyTestStore(authStore)
	authHandler, err := NewAuthHandler(authStore, "01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("new auth handler: %v", err)
	}

	pkHandler, err := NewPasskeyHandler(authStore, pkStore, authHandler)
	if err != nil {
		t.Fatalf("new passkey handler: %v", err)
	}

	// 1. RegisterOptions for guest singer
	regReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkey/register-options", strings.NewReader(`{"display_name":"Taylor Swift","email":"taylor@example.com"}`))
	regRec := httptest.NewRecorder()
	pkHandler.RegisterOptions(regRec, regReq)

	if regRec.Code != http.StatusOK {
		t.Fatalf("register options status = %d, want %d; body: %s", regRec.Code, http.StatusOK, regRec.Body.String())
	}

	var regResp struct {
		SessionID string                 `json:"session_id"`
		Options   map[string]interface{} `json:"options"`
	}
	if err := json.NewDecoder(regRec.Body).Decode(&regResp); err != nil {
		t.Fatalf("decode register options: %v", err)
	}
	if regResp.SessionID == "" {
		t.Fatalf("expected non-empty session_id")
	}
	publicKey, ok := regResp.Options["publicKey"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected publicKey in options, got: %v", regResp.Options)
	}
	if challenge, ok := publicKey["challenge"].(string); !ok || challenge == "" {
		t.Fatalf("expected non-empty challenge in publicKey options")
	}

	// Verify user does NOT exist in auth store yet (no pre-claim before ceremony)
	if _, err := authStore.GetUserByEmail(context.Background(), "taylor@example.com"); err == nil {
		t.Fatalf("expected taylor@example.com to NOT exist after register-options alone")
	}

	// 2. LoginOptions (Discoverable 1-tap)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkey/login-options", strings.NewReader(`{}`))
	loginRec := httptest.NewRecorder()
	pkHandler.LoginOptions(loginRec, loginReq)

	if loginRec.Code != http.StatusOK {
		t.Fatalf("login options status = %d, want %d; body: %s", loginRec.Code, http.StatusOK, loginRec.Body.String())
	}

	var loginResp struct {
		SessionID string                 `json:"session_id"`
		Options   map[string]interface{} `json:"options"`
	}
	if err := json.NewDecoder(loginRec.Body).Decode(&loginResp); err != nil {
		t.Fatalf("decode login options: %v", err)
	}
	if loginResp.SessionID == "" {
		t.Fatalf("expected non-empty session_id")
	}
	loginPK, ok := loginResp.Options["publicKey"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected publicKey in login options, got: %v", loginResp.Options)
	}
	if loginChallenge, ok := loginPK["challenge"].(string); !ok || loginChallenge == "" {
		t.Fatalf("expected non-empty challenge in login options")
	}
}

func TestPasskeyRegistration_AtomicLifecycle(t *testing.T) {
	authStore := newAuthTestStore()
	pkStore := newPasskeyTestStore(authStore)
	authHandler, err := NewAuthHandler(authStore, "01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("new auth handler: %v", err)
	}
	pkHandler, err := NewPasskeyHandler(authStore, pkStore, authHandler)
	if err != nil {
		t.Fatalf("new passkey handler: %v", err)
	}

	// Existing user
	existing := &models.User{
		ID:          "existing-user-1",
		Email:       "existing@example.com",
		DisplayName: "Existing Host",
	}
	_ = authStore.CreateUser(context.Background(), existing)

	// 1. RegisterOptions with an already registered email -> expect 409 Conflict
	dupReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkey/register-options", strings.NewReader(`{"display_name":"Duplicate","email":"existing@example.com"}`))
	dupRec := httptest.NewRecorder()
	pkHandler.RegisterOptions(dupRec, dupReq)
	if dupRec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for existing email in register-options, got %d", dupRec.Code)
	}

	// 2. RegisterOptions for fresh email generates challenge without creating user
	regReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkey/register-options", strings.NewReader(`{"display_name":"Newbie","email":"newbie@example.com"}`))
	regRec := httptest.NewRecorder()
	pkHandler.RegisterOptions(regRec, regReq)
	if regRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for register-options, got %d: %s", regRec.Code, regRec.Body.String())
	}
	var regResp struct {
		SessionID string `json:"session_id"`
	}
	_ = json.NewDecoder(regRec.Body).Decode(&regResp)
	if regResp.SessionID == "" {
		t.Fatal("expected session_id from register-options")
	}

	// Ensure user is NOT created yet
	if _, err := authStore.GetUserByEmail(context.Background(), "newbie@example.com"); err == nil {
		t.Fatal("user should NOT exist in authStore after register-options")
	}

	// 3. If the user calls register-options again before completing verify (e.g. page refresh / retry),
	// it should succeed without conflict
	retryReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkey/register-options", strings.NewReader(`{"display_name":"Newbie","email":"newbie@example.com"}`))
	retryRec := httptest.NewRecorder()
	pkHandler.RegisterOptions(retryRec, retryReq)
	if retryRec.Code != http.StatusOK {
		t.Fatalf("retry register-options should succeed, got %d: %s", retryRec.Code, retryRec.Body.String())
	}

	// 4. If register-verify is called with invalid / malformed ceremony payload, it fails and does not create the user
	badVerifyReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/passkey/register-verify", strings.NewReader(`{"session_id":"`+regResp.SessionID+`","credential":{"id":"invalid"}}`))
	badVerifyRec := httptest.NewRecorder()
	pkHandler.RegisterVerify(badVerifyRec, badVerifyReq)
	if badVerifyRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for failed verification ceremony, got %d", badVerifyRec.Code)
	}

	// Ensure user is STILL not created in auth store
	if _, err := authStore.GetUserByEmail(context.Background(), "newbie@example.com"); err == nil {
		t.Fatal("user should NOT exist after failed register-verify")
	}
}

func TestPasskeyHandler_ListAndDelete(t *testing.T) {
	authStore := newAuthTestStore()
	pkStore := newPasskeyTestStore(authStore)
	authHandler, err := NewAuthHandler(authStore, "01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("new auth handler: %v", err)
	}
	pkHandler, err := NewPasskeyHandler(authStore, pkStore, authHandler)
	if err != nil {
		t.Fatalf("new passkey handler: %v", err)
	}

	testUser := &models.User{
		ID:          "user-123",
		Email:       "test@example.com",
		DisplayName: "Test Host",
		IsHost:      true,
	}
	_ = authStore.CreateUser(context.Background(), testUser)

	_ = pkStore.SavePasskeyCredential(context.Background(), &models.PasskeyCredential{
		ID:           "cred-abc",
		UserID:       "user-123",
		FriendlyName: "MacBook Touch ID",
		CreatedAt:    time.Now(),
	})

	// List passkeys for authenticated user
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/passkeys", nil)
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, testUser))
	rec := httptest.NewRecorder()
	pkHandler.ListPasskeys(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list passkeys status = %d, want %d", rec.Code, http.StatusOK)
	}
	var listResp struct {
		Passkeys []passkeyInfoResponse `json:"passkeys"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list resp: %v", err)
	}
	if len(listResp.Passkeys) != 1 || listResp.Passkeys[0].FriendlyName != "MacBook Touch ID" {
		t.Fatalf("unexpected passkeys list: %+v", listResp.Passkeys)
	}

	// Delete passkey
	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/passkeys/cred-abc", nil)
	delReq.SetPathValue("id", "cred-abc")
	delReq = delReq.WithContext(context.WithValue(delReq.Context(), userContextKey, testUser))
	delRec := httptest.NewRecorder()
	pkHandler.DeletePasskey(delRec, delReq)

	if delRec.Code != http.StatusOK {
		t.Fatalf("delete passkey status = %d, want %d", delRec.Code, http.StatusOK)
	}

	// Verify deleted
	creds, _ := pkStore.ListPasskeyCredentialsForUser(context.Background(), "user-123")
	if len(creds) != 0 {
		t.Fatalf("expected 0 passkeys after delete, got %d", len(creds))
	}
}
