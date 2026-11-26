package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"micup.live/server/internal/email"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

type authTestStore struct {
	mu           sync.Mutex
	usersByEmail map[string]*models.User
	usersByID    map[string]*models.User
	sessions     map[string]*models.AuthSession
}

func newAuthTestStore() *authTestStore {
	return &authTestStore{
		usersByEmail: make(map[string]*models.User),
		usersByID:    make(map[string]*models.User),
		sessions:     make(map[string]*models.AuthSession),
	}
}

func (s *authTestStore) CreateUser(_ context.Context, user *models.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.usersByEmail[user.Email]; exists {
		return os.ErrExist
	}
	user.CreatedAt = time.Now()
	user.UpdatedAt = user.CreatedAt
	s.usersByEmail[user.Email] = user
	s.usersByID[user.ID] = user
	return nil
}

func (s *authTestStore) UpdateUser(_ context.Context, user *models.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if user.Deleting {
		return store.ErrAccountDeleting
	}
	existing, ok := s.usersByID[user.ID]
	if !ok || existing.AuthEpoch != user.AuthEpoch {
		return os.ErrNotExist
	}
	if existing.Deleting {
		return store.ErrAccountDeleting
	}
	existing.DisplayName = user.DisplayName
	existing.Email = user.Email
	existing.EmailVerified = user.EmailVerified
	existing.IsHost = user.IsHost
	existing.AvatarURL = user.AvatarURL
	if user.PasswordHash != "" {
		existing.PasswordHash = user.PasswordHash
	}
	existing.UpdatedAt = time.Now()
	s.usersByEmail[user.Email] = existing
	return nil
}

func (s *authTestStore) ChangeUserEmail(_ context.Context, userID, oldEmail, newEmail string, user *models.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if user.Deleting {
		return store.ErrAccountDeleting
	}
	existing, ok := s.usersByID[userID]
	if !ok || existing.AuthEpoch != user.AuthEpoch {
		return os.ErrNotExist
	}
	if existing.Deleting {
		return store.ErrAccountDeleting
	}
	if newEmail != "" {
		if owner, exists := s.usersByEmail[newEmail]; exists && owner.ID != userID {
			return fmt.Errorf("email already claimed: %w", os.ErrExist)
		}
	}
	if oldEmail != "" {
		delete(s.usersByEmail, oldEmail)
	}
	existing.DisplayName = user.DisplayName
	existing.Email = newEmail
	existing.EmailVerified = user.EmailVerified
	existing.IsHost = user.IsHost
	existing.AvatarURL = user.AvatarURL
	if user.PasswordHash != "" {
		existing.PasswordHash = user.PasswordHash
	}
	existing.UpdatedAt = time.Now()
	if newEmail != "" {
		s.usersByEmail[newEmail] = existing
	}
	return nil
}

func (s *authTestStore) GetUserByEmail(_ context.Context, email string) (*models.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.usersByEmail[email]
	if !ok {
		return nil, os.ErrNotExist
	}
	copy := *user
	return &copy, nil
}

func (s *authTestStore) GetUser(_ context.Context, id string) (*models.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getUser(id)
}

func (s *authTestStore) getUser(id string) (*models.User, error) {
	user, ok := s.usersByID[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	copy := *user
	return &copy, nil
}

func (s *authTestStore) CreateAuthSession(_ context.Context, session *models.AuthSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[string(session.TokenHash)] = session
	return nil
}

func (s *authTestStore) ConsumeAuthSession(_ context.Context, tokenHash []byte) (*models.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[string(tokenHash)]
	if !ok || !session.ExpiresAt.After(time.Now()) {
		return nil, os.ErrNotExist
	}
	delete(s.sessions, string(tokenHash))
	user, err := s.getUser(session.UserID)
	if err != nil || user.Deleting || user.AuthEpoch != session.AuthEpoch {
		return nil, os.ErrNotExist
	}
	return user, nil
}

func (s *authTestStore) DeleteAuthSessionsForUser(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for tokenHash, session := range s.sessions {
		if session.UserID == userID {
			delete(s.sessions, tokenHash)
		}
	}
	return nil
}

func (s *authTestStore) BeginAccountDeletion(_ context.Context, userID string, pendingCatalogVersions []string) (*models.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.usersByID[userID]
	if !ok {
		return nil, os.ErrNotExist
	}
	if !user.Deleting {
		user.Deleting = true
		if user.DeletionEpoch == "" {
			user.DeletionEpoch = "del-epoch-" + userID
		}
		if user.DeletionStartedAt.IsZero() {
			user.DeletionStartedAt = time.Now().UTC()
		}
		user.DeletionAuthEpoch = user.AuthEpoch
		user.AuthEpoch = "deleting:" + user.DeletionEpoch
	}
	seen := make(map[string]bool)
	var merged []string
	for _, list := range [][]string{user.PendingCatalogVersions, pendingCatalogVersions} {
		for _, id := range list {
			if id != "" && !seen[id] {
				seen[id] = true
				merged = append(merged, id)
			}
		}
	}
	user.PendingCatalogVersions = merged
	user.UpdatedAt = time.Now().UTC()
	copy := *user
	return &copy, nil
}

func (s *authTestStore) RecordAccountDeletionProgress(_ context.Context, userID, step, errMsg string, resolvedCatalogVersions []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.usersByID[userID]
	if !ok {
		return nil
	}
	user.DeletionStep = step
	user.DeletionError = errMsg
	if len(resolvedCatalogVersions) > 0 {
		resolvedSet := make(map[string]bool)
		for _, v := range resolvedCatalogVersions {
			resolvedSet[v] = true
		}
		var remaining []string
		for _, v := range user.PendingCatalogVersions {
			if !resolvedSet[v] {
				remaining = append(remaining, v)
			}
		}
		user.PendingCatalogVersions = remaining
	}
	user.UpdatedAt = time.Now().UTC()
	return nil
}

func (s *authTestStore) ListDeletingUsers(_ context.Context) ([]*models.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*models.User
	for _, u := range s.usersByID {
		if u.Deleting {
			copy := *u
			out = append(out, &copy)
		}
	}
	return out, nil
}

func (s *authTestStore) DeleteUser(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.usersByID[userID]
	if !ok {
		return nil
	}
	delete(s.usersByID, userID)
	if user.Email != "" {
		delete(s.usersByEmail, user.Email)
	}
	return nil
}

func (s *authTestStore) GetUserFavorites(_ context.Context, userID string) (*models.UserFavorites, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.usersByID[userID]
	if !ok {
		return nil, os.ErrNotExist
	}
	songs := user.FavoriteSongs
	if songs == nil {
		songs = []models.FavoriteSong{}
	}
	artists := user.FavoriteArtists
	if artists == nil {
		artists = []models.FavoriteArtist{}
	}
	return &models.UserFavorites{
		Songs:   songs,
		Artists: artists,
	}, nil
}

func (s *authTestStore) SaveUserFavorites(_ context.Context, userID string, favs *models.UserFavorites) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.usersByID[userID]
	if !ok {
		return os.ErrNotExist
	}
	if user.Deleting {
		return store.ErrAccountDeleting
	}
	if favs == nil {
		user.FavoriteSongs = []models.FavoriteSong{}
		user.FavoriteArtists = []models.FavoriteArtist{}
	} else {
		user.FavoriteSongs = favs.Songs
		user.FavoriteArtists = favs.Artists
	}
	user.UpdatedAt = time.Now().UTC()
	return nil
}

func newAuthTestServer(t *testing.T) *http.ServeMux {
	t.Helper()
	handler, err := NewAuthHandler(newAuthTestStore(), "test-signing-secret-with-at-least-32-bytes")
	if err != nil {
		t.Fatalf("create auth handler: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/register", handler.Register)
	mux.HandleFunc("POST /auth/login", handler.Login)
	mux.HandleFunc("POST /auth/refresh", handler.Refresh)
	mux.HandleFunc("GET /auth/me", handler.RequireAuth(handler.Me))
	mux.HandleFunc("PATCH /auth/me", handler.RequireAuth(handler.UpdateMe))
	mux.HandleFunc("POST /auth/logout", handler.RequireAuth(handler.Logout))
	mux.HandleFunc("POST /auth/verify-email/request", handler.RequireAuth(handler.RequestEmailVerification))
	mux.HandleFunc("POST /auth/verify-email/confirm", handler.ConfirmEmailVerification)
	mux.HandleFunc("GET /auth/verify-email/confirm", handler.ConfirmEmailVerification)
	return mux
}

func authRequestFor(t *testing.T, mux *http.ServeMux, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	return response
}

type testTokens struct {
	AccessToken  string
	RefreshToken string
	User         *models.User
}

func decodeTokens(t *testing.T, response *httptest.ResponseRecorder) testTokens {
	t.Helper()
	var tokens tokenResponse
	if err := json.NewDecoder(response.Body).Decode(&tokens); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	if tokens.AccessToken == "" {
		t.Fatalf("expected access token, got %+v", tokens)
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "micup_refresh" && cookie.Value != "" {
			return testTokens{AccessToken: tokens.AccessToken, RefreshToken: cookie.Value, User: tokens.User}
		}
	}
	t.Fatal("expected HttpOnly refresh cookie")
	return testTokens{}
}

func refreshRequestFor(t *testing.T, mux *http.ServeMux, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	req.AddCookie(&http.Cookie{Name: "micup_refresh", Value: token})
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	return response
}

func TestRegisterAndLoadProfile(t *testing.T) {
	mux := newAuthTestServer(t)
	response := authRequestFor(t, mux, http.MethodPost, "/auth/register", `{"email":"Host@Example.com","password":"secure-pass","display_name":"Host"}`, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("register status = %d; want %d", response.Code, http.StatusCreated)
	}
	tokens := decodeTokens(t, response)

	response = authRequestFor(t, mux, http.MethodGet, "/auth/me", "", tokens.AccessToken)
	if response.Code != http.StatusOK {
		t.Fatalf("profile status = %d; want %d", response.Code, http.StatusOK)
	}
	var user models.User
	if err := json.NewDecoder(response.Body).Decode(&user); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	if user.Email != "host@example.com" || user.DisplayName != "Host" || !user.IsHost {
		t.Errorf("unexpected profile: %+v", user)
	}
}

func TestLoginRejectsInvalidCredentials(t *testing.T) {
	mux := newAuthTestServer(t)
	response := authRequestFor(t, mux, http.MethodPost, "/auth/login", `{"email":"missing@example.com","password":"secure-pass"}`, "")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("login status = %d; want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestRefreshRotatesToken(t *testing.T) {
	mux := newAuthTestServer(t)
	registration := authRequestFor(t, mux, http.MethodPost, "/auth/register", `{"email":"host@example.com","password":"secure-pass"}`, "")
	tokens := decodeTokens(t, registration)

	refreshed := refreshRequestFor(t, mux, tokens.RefreshToken)
	if refreshed.Code != http.StatusOK {
		t.Fatalf("refresh status = %d; want %d", refreshed.Code, http.StatusOK)
	}
	newTokens := decodeTokens(t, refreshed)
	if newTokens.RefreshToken == tokens.RefreshToken {
		t.Error("refresh token was not rotated")
	}

	reused := refreshRequestFor(t, mux, tokens.RefreshToken)
	if reused.Code != http.StatusUnauthorized {
		t.Fatalf("reused refresh status = %d; want %d", reused.Code, http.StatusUnauthorized)
	}
}

func TestRefreshTokenCanOnlyBeConsumedOnceConcurrently(t *testing.T) {
	mux := newAuthTestServer(t)
	registration := authRequestFor(t, mux, http.MethodPost, "/auth/register", `{"email":"host@example.com","password":"secure-pass"}`, "")
	tokens := decodeTokens(t, registration)

	start := make(chan struct{})
	statuses := make(chan int, 2)
	var requests sync.WaitGroup
	for range 2 {
		requests.Add(1)
		go func() {
			defer requests.Done()
			<-start
			statuses <- refreshRequestFor(t, mux, tokens.RefreshToken).Code
		}()
	}
	close(start)
	requests.Wait()
	close(statuses)

	successes := 0
	unauthorized := 0
	for status := range statuses {
		if status == http.StatusOK {
			successes++
		}
		if status == http.StatusUnauthorized {
			unauthorized++
		}
	}
	if successes != 1 || unauthorized != 1 {
		t.Fatalf("refresh statuses: successes=%d unauthorized=%d; want one each", successes, unauthorized)
	}
}

func TestLogoutRevokesRefreshSessions(t *testing.T) {
	mux := newAuthTestServer(t)
	registration := authRequestFor(t, mux, http.MethodPost, "/auth/register", `{"email":"host@example.com","password":"secure-pass"}`, "")
	tokens := decodeTokens(t, registration)

	logout := authRequestFor(t, mux, http.MethodPost, "/auth/logout", "", tokens.AccessToken)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d; want %d", logout.Code, http.StatusNoContent)
	}

	refresh := refreshRequestFor(t, mux, tokens.RefreshToken)
	if refresh.Code != http.StatusUnauthorized {
		t.Fatalf("refresh status = %d; want %d", refresh.Code, http.StatusUnauthorized)
	}
}

func TestProfileRequiresBearerToken(t *testing.T) {
	mux := newAuthTestServer(t)
	response := authRequestFor(t, mux, http.MethodGet, "/auth/me", "", "")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("profile status = %d; want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestRequireHostRejectsNonHost(t *testing.T) {
	handler, err := NewAuthHandler(newAuthTestStore(), "test-signing-secret-with-at-least-32-bytes")
	if err != nil {
		t.Fatalf("create auth handler: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/host", nil)
	request = request.WithContext(context.WithValue(request.Context(), userContextKey, &models.User{ID: "singer", IsHost: false}))
	response := httptest.NewRecorder()
	handler.RequireHost(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d; want %d", response.Code, http.StatusForbidden)
	}
}

func TestUpdateProfile(t *testing.T) {
	mux := newAuthTestServer(t)
	registration := authRequestFor(t, mux, http.MethodPost, "/auth/register", `{"email":"profile@example.com","password":"secure-password"}`, "")
	tokens := decodeTokens(t, registration)

	// Update display name and role. Users can switch between Host and Singer freely.
	update := authRequestFor(t, mux, http.MethodPatch, "/auth/me", `{"display_name":"Rockstar Host","is_host":true}`, tokens.AccessToken)
	if update.Code != http.StatusOK {
		t.Fatalf("update status = %d; want %d: %s", update.Code, http.StatusOK, update.Body.String())
	}

	var updatedUser models.User
	if err := json.Unmarshal(update.Body.Bytes(), &updatedUser); err != nil {
		t.Fatalf("unmarshal updated user: %v", err)
	}
	if updatedUser.DisplayName != "Rockstar Host" {
		t.Fatalf("display_name = %q; want %q", updatedUser.DisplayName, "Rockstar Host")
	}
	if !updatedUser.IsHost {
		t.Fatalf("is_host = false; want true")
	}

	// Toggle to Singer mode (is_host = false)
	toggleSinger := authRequestFor(t, mux, http.MethodPatch, "/auth/me", `{"is_host":false}`, tokens.AccessToken)
	if toggleSinger.Code != http.StatusOK {
		t.Fatalf("toggle to singer status = %d; want %d", toggleSinger.Code, http.StatusOK)
	}
	var singerUser models.User
	if err := json.Unmarshal(toggleSinger.Body.Bytes(), &singerUser); err != nil {
		t.Fatalf("unmarshal singer user: %v", err)
	}
	if singerUser.IsHost {
		t.Fatalf("is_host = true; want false after toggling to singer")
	}

	// Toggle back to Host mode (is_host = true)
	toggleHost := authRequestFor(t, mux, http.MethodPatch, "/auth/me", `{"is_host":true}`, tokens.AccessToken)
	if toggleHost.Code != http.StatusOK {
		t.Fatalf("toggle to host status = %d; want %d", toggleHost.Code, http.StatusOK)
	}
	var hostUser models.User
	if err := json.Unmarshal(toggleHost.Body.Bytes(), &hostUser); err != nil {
		t.Fatalf("unmarshal host user: %v", err)
	}
	if !hostUser.IsHost {
		t.Fatalf("is_host = false; want true after toggling back to host")
	}
}

func TestUpdateMeEmailChangeAndConflict(t *testing.T) {
	mux := newAuthTestServer(t)
	reg1 := authRequestFor(t, mux, http.MethodPost, "/auth/register", `{"email":"user1@example.com","password":"secure-password-1"}`, "")
	tokens1 := decodeTokens(t, reg1)

	reg2 := authRequestFor(t, mux, http.MethodPost, "/auth/register", `{"email":"user2@example.com","password":"secure-password-2"}`, "")
	_ = decodeTokens(t, reg2)

	// 1. User 1 tries to change email to User 2's email -> expect 409 Conflict
	conflictUpdate := authRequestFor(t, mux, http.MethodPatch, "/auth/me", `{"email":"user2@example.com"}`, tokens1.AccessToken)
	if conflictUpdate.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for duplicate email update, got %d: %s", conflictUpdate.Code, conflictUpdate.Body.String())
	}

	// 2. User 1 changes email to a new valid email -> expect 200 OK
	validUpdate := authRequestFor(t, mux, http.MethodPatch, "/auth/me", `{"email":"user1.new@example.com"}`, tokens1.AccessToken)
	if validUpdate.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for valid email update, got %d: %s", validUpdate.Code, validUpdate.Body.String())
	}
	var updatedUser models.User
	if err := json.Unmarshal(validUpdate.Body.Bytes(), &updatedUser); err != nil {
		t.Fatalf("unmarshal updated user: %v", err)
	}
	if updatedUser.Email != "user1.new@example.com" {
		t.Fatalf("email = %q; want %q", updatedUser.Email, "user1.new@example.com")
	}
	if updatedUser.EmailVerified {
		t.Fatalf("email_verified should be false after email change")
	}

	// 3. User 1 can log in with new email and password
	newLogin := authRequestFor(t, mux, http.MethodPost, "/auth/login", `{"email":"user1.new@example.com","password":"secure-password-1"}`, "")
	if newLogin.Code != http.StatusOK {
		t.Fatalf("login with new email status = %d; want %d: %s", newLogin.Code, http.StatusOK, newLogin.Body.String())
	}

	// 4. Old email no longer valid for login
	oldLogin := authRequestFor(t, mux, http.MethodPost, "/auth/login", `{"email":"user1@example.com","password":"secure-password-1"}`, "")
	if oldLogin.Code != http.StatusUnauthorized {
		t.Fatalf("login with old email status = %d; want %d", oldLogin.Code, http.StatusUnauthorized)
	}

	// 5. User 1 can update / establish password while logged in
	shortPwdUpdate := authRequestFor(t, mux, http.MethodPatch, "/auth/me", `{"password":"short"}`, tokens1.AccessToken)
	if shortPwdUpdate.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for short password, got %d", shortPwdUpdate.Code)
	}

	validPwdUpdate := authRequestFor(t, mux, http.MethodPatch, "/auth/me", `{"password":"brand-new-secure-password"}`, tokens1.AccessToken)
	if validPwdUpdate.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid password update, got %d: %s", validPwdUpdate.Code, validPwdUpdate.Body.String())
	}

	// 6. User 1 can log in with new password
	updatedLogin := authRequestFor(t, mux, http.MethodPost, "/auth/login", `{"email":"user1.new@example.com","password":"brand-new-secure-password"}`, "")
	if updatedLogin.Code != http.StatusOK {
		t.Fatalf("login with brand new password failed: %d", updatedLogin.Code)
	}
}

func TestEmailVerificationFlow(t *testing.T) {
	store := newAuthTestStore()
	handler, err := NewAuthHandler(store, "test-signing-secret-with-at-least-32-bytes")
	if err != nil {
		t.Fatalf("create auth handler: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/register", handler.Register)
	mux.HandleFunc("GET /auth/me", handler.RequireAuth(handler.Me))
	mux.HandleFunc("POST /auth/verify-email/request", handler.RequireAuth(handler.RequestEmailVerification))
	mux.HandleFunc("POST /auth/verify-email/confirm", handler.ConfirmEmailVerification)
	mux.HandleFunc("GET /auth/verify-email/confirm", handler.ConfirmEmailVerification)

	// 1. Register user
	reg := authRequestFor(t, mux, http.MethodPost, "/auth/register", `{"email":"unverified@example.com","password":"password123","display_name":"Singer One"}`, "")
	if reg.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want %d", reg.Code, http.StatusCreated)
	}
	tokens := decodeTokens(t, reg)
	if tokens.User.EmailVerified {
		t.Fatalf("new user email_verified = true, want false")
	}

	// 2. Request verification email
	reqVer := authRequestFor(t, mux, http.MethodPost, "/auth/verify-email/request", "", tokens.AccessToken)
	if reqVer.Code != http.StatusOK {
		t.Fatalf("request verification status = %d, want %d: %s", reqVer.Code, http.StatusOK, reqVer.Body.String())
	}

	// 3. Generate valid token and test confirm
	validToken, err := handler.newVerificationToken(tokens.User.ID, tokens.User.Email)
	if err != nil {
		t.Fatalf("newVerificationToken: %v", err)
	}

	// Test invalid token
	badConfirm := authRequestFor(t, mux, http.MethodPost, "/auth/verify-email/confirm", `{"token":"invalid.token.here"}`, "")
	if badConfirm.Code != http.StatusBadRequest {
		t.Fatalf("bad confirm status = %d, want %d", badConfirm.Code, http.StatusBadRequest)
	}

	// Test valid confirmation
	goodConfirm := authRequestFor(t, mux, http.MethodPost, "/auth/verify-email/confirm", `{"token":"`+validToken+`"}`, "")
	if goodConfirm.Code != http.StatusOK {
		t.Fatalf("good confirm status = %d, want %d: %s", goodConfirm.Code, http.StatusOK, goodConfirm.Body.String())
	}

	// 4. Verify user record reflects verified status
	me := authRequestFor(t, mux, http.MethodGet, "/auth/me", "", tokens.AccessToken)
	if me.Code != http.StatusOK {
		t.Fatalf("me status = %d, want %d", me.Code, http.StatusOK)
	}
	var meUser models.User
	if err := json.Unmarshal(me.Body.Bytes(), &meUser); err != nil {
		t.Fatalf("unmarshal me: %v", err)
	}
	if !meUser.EmailVerified {
		t.Fatalf("email_verified = false, want true")
	}

	// 5. Test already verified message
	reqAgain := authRequestFor(t, mux, http.MethodPost, "/auth/verify-email/request", "", tokens.AccessToken)
	if reqAgain.Code != http.StatusOK {
		t.Fatalf("request again status = %d, want %d", reqAgain.Code, http.StatusOK)
	}
	if !strings.Contains(reqAgain.Body.String(), "already verified") {
		t.Fatalf("expected 'already verified' in response, got %s", reqAgain.Body.String())
	}
}

func TestPasswordResetFlow(t *testing.T) {
	store := newAuthTestStore()
	handler, err := NewAuthHandler(store, "test-auth-secret-minimum-32-bytes!")
	if err != nil {
		t.Fatalf("NewAuthHandler: %v", err)
	}
	handler.EmailSender = email.NewLogSender()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/register", handler.Register)
	mux.HandleFunc("POST /auth/login", handler.Login)
	mux.HandleFunc("POST /auth/forgot-password", handler.RequestPasswordReset)
	mux.HandleFunc("POST /auth/reset-password", handler.ConfirmPasswordReset)

	// 1. Register test user
	reg := authRequestFor(t, mux, http.MethodPost, "/auth/register", `{"email":"forgot@example.com","password":"oldpassword123","display_name":"Forgot User","is_host":true}`, "")
	if reg.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want %d: %s", reg.Code, http.StatusCreated, reg.Body.String())
	}

	// 2. Request password reset
	reqReset := authRequestFor(t, mux, http.MethodPost, "/auth/forgot-password", `{"email":"forgot@example.com"}`, "")
	if reqReset.Code != http.StatusOK {
		t.Fatalf("forgot-password status = %d, want %d", reqReset.Code, http.StatusOK)
	}

	// 3. Generate valid token
	u, _ := store.GetUserByEmail(context.Background(), "forgot@example.com")
	resetToken, err := handler.PasswordReset.IssueResetToken(u)
	if err != nil {
		t.Fatalf("newPasswordResetToken: %v", err)
	}

	// 4. Confirm with invalid token -> 400
	badConfirm := authRequestFor(t, mux, http.MethodPost, "/auth/reset-password", `{"token":"invalid.token","password":"newpassword456"}`, "")
	if badConfirm.Code != http.StatusBadRequest {
		t.Fatalf("bad token status = %d, want 400", badConfirm.Code)
	}

	// 5. Confirm with valid token and short password -> 400
	shortConfirm := authRequestFor(t, mux, http.MethodPost, "/auth/reset-password", fmt.Sprintf(`{"token":%q,"password":"short"}`, resetToken), "")
	if shortConfirm.Code != http.StatusBadRequest {
		t.Fatalf("short password status = %d, want 400", shortConfirm.Code)
	}

	// 6. Confirm with valid token and strong password -> 200
	goodConfirm := authRequestFor(t, mux, http.MethodPost, "/auth/reset-password", fmt.Sprintf(`{"token":%q,"password":"newpassword456"}`, resetToken), "")
	if goodConfirm.Code != http.StatusOK {
		t.Fatalf("good confirm status = %d, want 200: %s", goodConfirm.Code, goodConfirm.Body.String())
	}

	// 7. Old password login must fail -> 401
	oldLogin := authRequestFor(t, mux, http.MethodPost, "/auth/login", `{"email":"forgot@example.com","password":"oldpassword123"}`, "")
	if oldLogin.Code != http.StatusUnauthorized {
		t.Fatalf("old password status = %d, want 401", oldLogin.Code)
	}

	// 8. New password login must succeed -> 200
	newLogin := authRequestFor(t, mux, http.MethodPost, "/auth/login", `{"email":"forgot@example.com","password":"newpassword456"}`, "")
	if newLogin.Code != http.StatusOK {
		t.Fatalf("new password login status = %d, want 200: %s", newLogin.Code, newLogin.Body.String())
	}
}

func (s *authTestStore) ResetPassword(_ context.Context, reset store.PasswordResetCommit) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.usersByID[reset.UserID]
	if !ok || user.Deleting || user.Email != reset.Email || user.AuthEpoch != reset.ExpectedAuthEpoch || !reset.ExpiresAt.After(time.Now()) || reset.AuthEpoch == "" || reset.AuthEpoch == reset.ExpectedAuthEpoch {
		return store.ErrInvalidPasswordReset
	}
	user.PasswordHash = reset.PasswordHash
	user.AuthEpoch = reset.AuthEpoch
	user.UpdatedAt = time.Now()
	return nil
}

func TestPasswordResetRevokesOptionalAndPasskeyIdentity(t *testing.T) {
	repository := newAuthTestStore()
	user := &models.User{ID: "epoch-user", Email: "epoch@example.com"}
	if err := repository.CreateUser(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	handler, err := NewAuthHandler(repository, "test-signing-secret-with-at-least-32-bytes")
	if err != nil {
		t.Fatal(err)
	}
	oldToken, err := handler.newAccessToken(user.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	token, err := handler.PasswordReset.IssueResetToken(user)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.PasswordReset.ConfirmPasswordReset(context.Background(), token, "new-password"); err != nil {
		t.Fatal(err)
	}
	actual, err := repository.GetUser(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	freshToken, err := handler.newAccessToken(actual.ID, actual.AuthEpoch)
	if err != nil {
		t.Fatal(err)
	}
	passkeys := &PasskeyHandler{authStore: repository, authHandler: handler}
	for _, tc := range []struct {
		name, token   string
		authenticated bool
	}{{"revoked", oldToken, false}, {"fresh", freshToken, true}} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("Authorization", "Bearer "+tc.token)
			handler.OptionalAuth(func(_ http.ResponseWriter, r *http.Request) {
				if (UserFromContext(r.Context()) != nil) != tc.authenticated {
					t.Fatal("optional auth accepted wrong epoch")
				}
			})(httptest.NewRecorder(), request)
			if (passkeys.resolveUser(request) != nil) != tc.authenticated {
				t.Fatal("passkey adapter accepted wrong epoch")
			}
		})
	}
}

func TestAuthHandlerRejectsOverlongPasswordWithBadRequest(t *testing.T) {
	store := newAuthTestStore()
	handler, err := NewAuthHandler(store, "test-signing-secret-with-at-least-32-bytes")
	if err != nil {
		t.Fatal(err)
	}
	overlong := strings.Repeat("p", 73)

	t.Run("register", func(t *testing.T) {
		body := `{"email":"long@example.com","password":"` + overlong + `"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(body))
		w := httptest.NewRecorder()
		handler.Register(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("Register status = %d, want 400", w.Code)
		}
	})

	t.Run("update_me", func(t *testing.T) {
		user := &models.User{ID: "u-long", Email: "u@example.com", DisplayName: "Host", IsHost: true}
		_ = store.CreateUser(context.Background(), user)
		body := `{"password":"` + overlong + `"}`
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/auth/me", strings.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), userContextKey, user))
		w := httptest.NewRecorder()
		handler.UpdateMe(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("UpdateMe status = %d, want 400", w.Code)
		}
	})

	t.Run("confirm_password_reset", func(t *testing.T) {
		body := `{"token":"dummy","password":"` + overlong + `"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reset-password", strings.NewReader(body))
		w := httptest.NewRecorder()
		handler.ConfirmPasswordReset(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("ConfirmPasswordReset status = %d, want 400", w.Code)
		}
	})
}
