// Package handlers provides HTTP endpoints for the MicUp API.
package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"micup.live/server/internal/auth"
	"micup.live/server/internal/email"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

const (
	accessTokenLifetime           = 15 * time.Minute
	refreshTokenLifetime          = 30 * 24 * time.Hour
	deletionRecoveryTokenLifetime = 24 * time.Hour
	deletionRecoveryAction        = "delete-account-recovery"
)

type contextKey string

const userContextKey contextKey = "authenticated-user"

// AuthHandler handles password authentication, bearer-token identity, and email verification.
type AuthHandler struct {
	Store         store.AuthStore
	PasswordReset *auth.Service
	secret        []byte
	EmailSender   email.Sender
	PublicBaseURL string
	CookieSecure  bool
}

type authRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	IsHost      *bool  `json:"is_host"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type tokenResponse struct {
	AccessToken string       `json:"access_token"`
	User        *models.User `json:"user,omitempty"`
}

type accessClaims struct {
	UserID        string `json:"sub"`
	AuthEpoch     string `json:"auth_epoch,omitempty"`
	Action        string `json:"act,omitempty"`
	DeletionEpoch string `json:"del_epoch,omitempty"`
	ExpiresAt     int64  `json:"exp"`
}

type verificationClaims struct {
	UserID    string `json:"sub"`
	Email     string `json:"email"`
	Action    string `json:"action"`
	ExpiresAt int64  `json:"exp"`
}

// NewAuthHandler constructs an auth handler using the required signing secret.
func NewAuthHandler(authStore store.AuthStore, secret string) (*AuthHandler, error) {
	if len(secret) < 32 {
		return nil, errors.New("AUTH_SECRET must be at least 32 bytes")
	}
	return &AuthHandler{
		Store:         authStore,
		PasswordReset: auth.NewService(authStore, secret),
		secret:        []byte(secret),
		EmailSender:   email.NewLogSender(),
	}, nil
}

// Register creates a host account and issues a new session.
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := decodeJSON(w, r, &req, authBodyLimit); err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	email := normalizeEmail(req.Email)
	if email == "" {
		writeAuthError(w, http.StatusBadRequest, "email and an 8-character password are required")
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" {
		displayName = strings.Split(email, "@")[0]
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "unable to create account")
		return
	}
	isHost := true
	if req.IsHost != nil {
		isHost = *req.IsHost
	}
	user := &models.User{ID: newID(), Email: email, DisplayName: displayName, PasswordHash: string(passwordHash), IsHost: isHost}
	if err := h.Store.CreateUser(r.Context(), user); err != nil {
		log.Printf("[AUTH ERROR] failed to create user %s: %v", email, err)
		writeAuthError(w, http.StatusConflict, "email is already registered")
		return
	}
	log.Printf("[AUTH] Registered new account: id=%s email=%s host=%t", user.ID, user.Email, user.IsHost)
	h.SendVerificationEmailSafe(h.publicBaseURL(r), user)
	h.WriteSession(w, r, user, http.StatusCreated)
}

// Login verifies a password and issues a new session.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req authRequest
	if err := decodeJSON(w, r, &req, authBodyLimit); err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	user, err := h.Store.GetUserByEmail(r.Context(), normalizeEmail(req.Email))
	if err != nil || user == nil || user.Deleting || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)) != nil {
		log.Printf("[AUTH WARN] Failed login attempt for email=%s", req.Email)
		writeAuthError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	log.Printf("[AUTH] User logged in: id=%s email=%s", user.ID, user.Email)
	h.WriteSession(w, r, user, http.StatusOK)
}

// Refresh rotates a refresh token and issues a new session.
func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	_ = decodeJSON(w, r, &req, authBodyLimit)
	refreshToken := req.RefreshToken
	if cookie, err := r.Cookie("micup_refresh"); err == nil && cookie.Value != "" {
		refreshToken = cookie.Value
	}
	if refreshToken == "" {
		writeAuthError(w, http.StatusBadRequest, "refresh token is required")
		return
	}

	tokenHash := hashToken(refreshToken)
	user, err := h.Store.ConsumeAuthSession(r.Context(), tokenHash)
	if err != nil || user == nil || user.Deleting {
		log.Printf("[AUTH WARN] Failed session refresh: %v", err)
		writeAuthError(w, http.StatusUnauthorized, "invalid refresh token")
		return
	}
	log.Printf("[AUTH] Session refreshed for user: id=%s", user.ID)
	h.WriteSession(w, r, user, http.StatusOK)
}

// Me returns the authenticated user's profile.
func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	writeAuthJSON(w, http.StatusOK, user)
}

type updateProfileRequest struct {
	DisplayName *string `json:"display_name"`
	Email       *string `json:"email"`
	AvatarURL   *string `json:"avatar_url"`
	IsHost      *bool   `json:"is_host"`
	Password    *string `json:"password"`
}

// UpdateMe updates profile settings (display name, email, is_host, avatar, password) for the authenticated user.
func (h *AuthHandler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil || user.Deleting {
		writeAuthError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req updateProfileRequest
	if err := decodeJSON(w, r, &req, authBodyLimit); err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	updatedUser := *user
	if req.DisplayName != nil {
		name := strings.TrimSpace(*req.DisplayName)
		if name != "" {
			updatedUser.DisplayName = name
		}
	}
	emailChanged := false
	var oldEmail, newEmail string
	if req.Email != nil {
		newEmail = normalizeEmail(*req.Email)
		if newEmail != user.Email {
			emailChanged = true
			oldEmail = user.Email
			updatedUser.Email = newEmail
			updatedUser.EmailVerified = false
		}
	}
	if req.AvatarURL != nil {
		updatedUser.AvatarURL = strings.TrimSpace(*req.AvatarURL)
	}
	if req.IsHost != nil {
		updatedUser.IsHost = *req.IsHost
	}
	if req.Password != nil {
		pwd := *req.Password
		if strings.TrimSpace(pwd) != "" {
			if err := auth.ValidatePassword(pwd); err != nil {
				writeAuthError(w, http.StatusBadRequest, err.Error())
				return
			}
			passwordHash, err := bcrypt.GenerateFromPassword([]byte(pwd), bcrypt.DefaultCost)
			if err != nil {
				writeAuthError(w, http.StatusInternalServerError, "failed to hash password")
				return
			}
			updatedUser.PasswordHash = string(passwordHash)
		}
	}

	if emailChanged {
		if err := h.Store.ChangeUserEmail(r.Context(), user.ID, oldEmail, newEmail, &updatedUser); err != nil {
			log.Printf("[AUTH ERROR] Failed changing email for user %s: %v", user.ID, err)
			if errors.Is(err, store.ErrAccountDeleting) {
				writeAuthError(w, http.StatusUnauthorized, "invalid access token")
				return
			}
			writeAuthError(w, http.StatusConflict, "an account with this email already exists")
			return
		}
		h.SendVerificationEmailSafe(h.publicBaseURL(r), &updatedUser)
	} else {
		if err := h.Store.UpdateUser(r.Context(), &updatedUser); err != nil {
			log.Printf("[AUTH ERROR] Failed updating profile for user %s: %v", user.ID, err)
			if errors.Is(err, store.ErrAccountDeleting) {
				writeAuthError(w, http.StatusUnauthorized, "invalid access token")
				return
			}
			writeAuthError(w, http.StatusInternalServerError, "failed to update profile")
			return
		}
	}
	log.Printf("[AUTH] Profile updated: id=%s name=%q email=%q host=%t", updatedUser.ID, updatedUser.DisplayName, updatedUser.Email, updatedUser.IsHost)
	writeAuthJSON(w, http.StatusOK, &updatedUser)
}

// Logout revokes all refresh sessions for the authenticated user.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if err := h.Store.DeleteAuthSessionsForUser(r.Context(), user.ID); err != nil {
		log.Printf("[AUTH ERROR] Failed logout for user %s: %v", user.ID, err)
		writeAuthError(w, http.StatusInternalServerError, "unable to end session")
		return
	}
	log.Printf("[AUTH] User logged out: id=%s", user.ID)
	h.clearRefreshCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

type confirmEmailRequest struct {
	Token string `json:"token"`
}

// RequestEmailVerification generates and delivers an email verification link for the user.
func (h *AuthHandler) RequestEmailVerification(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if user == nil || user.Deleting {
		writeAuthError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if user.Email == "" {
		writeAuthError(w, http.StatusBadRequest, "no email address is associated with this account")
		return
	}
	if user.EmailVerified {
		writeAuthJSON(w, http.StatusOK, map[string]string{"message": "email is already verified"})
		return
	}

	h.SendVerificationEmailSafe(h.publicBaseURL(r), user)
	writeAuthJSON(w, http.StatusOK, map[string]string{"message": "verification email sent"})
}

// ConfirmEmailVerification validates the signed token and marks the email verified.
func (h *AuthHandler) ConfirmEmailVerification(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" && r.Method == http.MethodPost {
		var req confirmEmailRequest
		_ = decodeJSON(w, r, &req, authBodyLimit)
		token = req.Token
	}
	token = strings.TrimSpace(token)
	if token == "" {
		writeAuthError(w, http.StatusBadRequest, "verification token is required")
		return
	}

	claims, err := h.parseVerificationToken(token)
	if err != nil {
		log.Printf("[AUTH WARN] invalid or expired verification token: %v", err)
		writeAuthError(w, http.StatusBadRequest, "invalid or expired verification link")
		return
	}

	ctx := r.Context()
	user, err := h.Store.GetUser(ctx, claims.UserID)
	if err != nil || user == nil || user.Deleting {
		log.Printf("[AUTH WARN] user %s not found or deleting during verification: %v", claims.UserID, err)
		writeAuthError(w, http.StatusNotFound, "user account not found")
		return
	}

	if normalizeEmail(user.Email) != normalizeEmail(claims.Email) {
		log.Printf("[AUTH WARN] verification token email mismatch: token=%s user=%s", claims.Email, user.Email)
		writeAuthError(w, http.StatusBadRequest, "verification token is no longer valid for this email")
		return
	}

	user.EmailVerified = true
	if err := h.Store.UpdateUser(ctx, user); err != nil {
		log.Printf("[AUTH ERROR] failed to update email_verified status for %s: %v", user.ID, err)
		if errors.Is(err, store.ErrAccountDeleting) {
			writeAuthError(w, http.StatusNotFound, "user account not found")
			return
		}
		writeAuthError(w, http.StatusInternalServerError, "failed to update verification status")
		return
	}

	log.Printf("[AUTH] Email verified successfully: user=%s email=%s", user.ID, user.Email)
	writeAuthJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Email verified successfully!",
		"user":    user,
	})
}

type passwordResetRequest struct {
	Email string `json:"email"`
}

type passwordResetConfirmRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// RequestPasswordReset sends a password reset link to the specified email if an account exists.
func (h *AuthHandler) RequestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var req passwordResetRequest
	if err := decodeJSON(w, r, &req, authBodyLimit); err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	email := normalizeEmail(req.Email)
	if email == "" {
		writeAuthError(w, http.StatusBadRequest, "email address is required")
		return
	}

	// Always return a neutral success message to prevent user enumeration
	successResp := map[string]string{
		"message": "If an account exists with that email address, a password reset link has been sent.",
	}

	if err := h.PasswordReset.RequestPasswordReset(r.Context(), email, h.publicBaseURL(r), h.EmailSender); err != nil {
		log.Printf("[AUTH ERROR] password reset delivery failed: %v", err)
	}

	writeAuthJSON(w, http.StatusOK, successResp)
}

// ConfirmPasswordReset validates the reset token and updates the user's password.
func (h *AuthHandler) ConfirmPasswordReset(w http.ResponseWriter, r *http.Request) {
	var req passwordResetConfirmRequest
	if err := decodeJSON(w, r, &req, authBodyLimit); err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		token = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	if token == "" {
		writeAuthError(w, http.StatusBadRequest, "reset token is required")
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.PasswordReset.ConfirmPasswordReset(r.Context(), token, req.Password); err != nil {
		if errors.Is(err, store.ErrInvalidPasswordReset) {
			writeAuthError(w, http.StatusBadRequest, "invalid or expired reset link")
		} else if errors.Is(err, auth.ErrShortPassword) || errors.Is(err, auth.ErrLongPassword) {
			writeAuthError(w, http.StatusBadRequest, err.Error())
		} else {
			log.Printf("[AUTH ERROR] password reset failed: %v", err)
			writeAuthError(w, http.StatusInternalServerError, "failed to update password")
		}
		return
	}
	h.clearRefreshCookie(w)
	writeAuthJSON(w, http.StatusOK, map[string]string{
		"message": "Password reset successfully. You can now sign in.",
	})
}

// RequireAuth validates a bearer token and loads its user into the request context.
func (h *AuthHandler) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == r.Header.Get("Authorization") {
			writeAuthError(w, http.StatusUnauthorized, "bearer token is required")
			return
		}

		claims, err := h.parseAccessToken(token)
		if err != nil || claims.Action != "" {
			writeAuthError(w, http.StatusUnauthorized, "invalid access token")
			return
		}
		user, err := h.Store.GetUser(r.Context(), claims.UserID)
		if err != nil || user == nil || user.Deleting || user.AuthEpoch != claims.AuthEpoch {
			writeAuthError(w, http.StatusUnauthorized, "invalid access token")
			return
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey, user)))
	}
}

// RequireAccountDeletionAuth authorizes DELETE /api/v1/auth/me for either:
//  1. An active authenticated user session initiating account deletion, or
//  2. A scoped deletion recovery token (via X-Deletion-Token or Authorization: Bearer)
//     or the same account's interrupted deletion request token resuming a partially completed
//     or already completed deletion without restoring general session credentials.
func (h *AuthHandler) RequireAccountDeletionAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		delHeader := strings.TrimSpace(r.Header.Get("X-Deletion-Token"))
		authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
		var bearerToken string
		if strings.HasPrefix(authHeader, "Bearer ") {
			bearerToken = strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		}

		if delHeader == "" && bearerToken == "" {
			writeAuthError(w, http.StatusUnauthorized, "bearer token is required")
			return
		}

		// If both headers are supplied, verify they do not belong to different tenants.
		if delHeader != "" && bearerToken != "" {
			delClaims, delErr := h.parseAccessToken(delHeader)
			if delErr != nil {
				writeAuthError(w, http.StatusUnauthorized, "invalid deletion recovery token")
				return
			}
			if bearerClaims, bErr := h.parseAccessToken(bearerToken); bErr == nil && bearerClaims.UserID != delClaims.UserID {
				writeAuthError(w, http.StatusForbidden, "cannot resume account deletion for another user")
				return
			}
		}

		primaryToken := delHeader
		if primaryToken == "" {
			primaryToken = bearerToken
		}

		claims, err := h.parseAccessToken(primaryToken)
		if err != nil {
			writeAuthError(w, http.StatusUnauthorized, "invalid access token")
			return
		}

		if claims.Action == deletionRecoveryAction {
			if claims.DeletionEpoch == "" {
				writeAuthError(w, http.StatusUnauthorized, "invalid deletion recovery token")
				return
			}
			user, err := h.Store.GetUser(r.Context(), claims.UserID)
			if errors.Is(err, os.ErrNotExist) {
				// Account record was already deleted; allow idempotent completion check.
				synthetic := &models.User{
					ID:            claims.UserID,
					Deleting:      true,
					DeletionEpoch: claims.DeletionEpoch,
				}
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey, synthetic)))
				return
			}
			if err != nil || user == nil || !user.Deleting || user.DeletionEpoch != claims.DeletionEpoch {
				writeAuthError(w, http.StatusUnauthorized, "invalid deletion recovery token")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey, user)))
			return
		}

		if claims.Action != "" {
			writeAuthError(w, http.StatusUnauthorized, "invalid access token")
			return
		}

		user, err := h.Store.GetUser(r.Context(), claims.UserID)
		if errors.Is(err, os.ErrNotExist) {
			writeAuthError(w, http.StatusUnauthorized, "invalid access token")
			return
		}
		if err != nil || user == nil {
			writeAuthError(w, http.StatusUnauthorized, "invalid access token")
			return
		}
		if !user.Deleting {
			if user.AuthEpoch != claims.AuthEpoch {
				writeAuthError(w, http.StatusUnauthorized, "invalid access token")
				return
			}
		} else {
			if user.DeletionEpoch == "" || user.DeletionAuthEpoch != claims.AuthEpoch {
				writeAuthError(w, http.StatusUnauthorized, "invalid access token")
				return
			}
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey, user)))
	}
}

// NewDeletionRecoveryToken issues a single-purpose token scoped strictly to resuming
// DELETE /api/v1/auth/me for the given user and deletion epoch.
func (h *AuthHandler) NewDeletionRecoveryToken(userID, deletionEpoch string) (string, error) {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(deletionEpoch) == "" {
		return "", errors.New("user ID and deletion epoch are required")
	}
	payload, err := json.Marshal(accessClaims{
		UserID:        userID,
		Action:        deletionRecoveryAction,
		DeletionEpoch: deletionEpoch,
		ExpiresAt:     time.Now().Add(deletionRecoveryTokenLifetime).Unix(),
	})
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, h.secret)
	mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// RequireHost permits management endpoints only for host accounts.
func (h *AuthHandler) RequireHost(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		if user == nil || !user.IsHost {
			writeAuthError(w, http.StatusForbidden, "host access is required")
			return
		}
		next.ServeHTTP(w, r)
	}
}

// OptionalAuth attaches user to context if a valid Bearer token is provided, without failing if missing.
func (h *AuthHandler) OptionalAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			token := strings.TrimPrefix(authHeader, "Bearer ")
			if claims, err := h.parseAccessToken(token); err == nil && claims != nil && claims.Action == "" {
				if user, err := h.Store.GetUser(r.Context(), claims.UserID); err == nil && user != nil && !user.Deleting && user.AuthEpoch == claims.AuthEpoch {
					r = r.WithContext(context.WithValue(r.Context(), userContextKey, user))
				}
			}
		}
		next.ServeHTTP(w, r)
	}
}

// UserFromContext returns the authenticated user added by RequireAuth.
func UserFromContext(ctx context.Context) *models.User {
	user, _ := ctx.Value(userContextKey).(*models.User)
	return user
}

func (h *AuthHandler) WriteSession(w http.ResponseWriter, r *http.Request, user *models.User, status int) {
	if user == nil || user.Deleting {
		writeAuthError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	accessToken, err := h.newAccessToken(user.ID, user.AuthEpoch)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "unable to create session")
		return
	}
	refreshToken, err := newRefreshToken()
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "unable to create session")
		return
	}
	if err := h.Store.CreateAuthSession(r.Context(), &models.AuthSession{
		TokenHash: hashToken(refreshToken),
		UserID:    user.ID,
		AuthEpoch: user.AuthEpoch,
		ExpiresAt: time.Now().Add(refreshTokenLifetime),
	}); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "unable to create session")
		return
	}
	h.setRefreshCookie(w, refreshToken)
	writeAuthJSON(w, status, tokenResponse{AccessToken: accessToken, User: user})
}

func (h *AuthHandler) newAccessToken(userID, epoch string) (string, error) {
	payload, err := json.Marshal(accessClaims{UserID: userID, AuthEpoch: epoch, ExpiresAt: time.Now().Add(accessTokenLifetime).Unix()})
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, h.secret)
	mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (h *AuthHandler) parseAccessToken(token string) (*accessClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil, errors.New("invalid token format")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, h.secret)
	mac.Write([]byte(parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, errors.New("invalid token signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	var claims accessClaims
	if err := json.Unmarshal(payload, &claims); err != nil || claims.UserID == "" || claims.ExpiresAt <= time.Now().Unix() {
		return nil, errors.New("expired or invalid token")
	}
	return &claims, nil
}

const verificationTokenLifetime = 24 * time.Hour

func (h *AuthHandler) newVerificationToken(userID, email string) (string, error) {
	payload, err := json.Marshal(verificationClaims{
		UserID:    userID,
		Email:     email,
		Action:    "verify-email",
		ExpiresAt: time.Now().Add(verificationTokenLifetime).Unix(),
	})
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, h.secret)
	mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (h *AuthHandler) parseVerificationToken(token string) (*verificationClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil, errors.New("invalid token format")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, h.secret)
	mac.Write([]byte(parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, errors.New("invalid token signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	var claims verificationClaims
	if err := json.Unmarshal(payload, &claims); err != nil || claims.UserID == "" || claims.Action != "verify-email" || claims.ExpiresAt <= time.Now().Unix() {
		return nil, errors.New("expired or invalid verification link")
	}
	return &claims, nil
}
