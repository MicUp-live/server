package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

const webAuthnSessionTTL = 5 * time.Minute

// PasskeyHandler handles FIDO2 / WebAuthn registration, authentication, and key management.
type PasskeyHandler struct {
	authStore    store.AuthStore
	passkeyStore store.PasskeyStore
	authHandler  *AuthHandler
	webauthn     *webauthn.WebAuthn
}

type webauthnUser struct {
	id          []byte
	name        string
	displayName string
	credentials []webauthn.Credential
}

func (u *webauthnUser) WebAuthnID() []byte                         { return u.id }
func (u *webauthnUser) WebAuthnName() string                       { return u.name }
func (u *webauthnUser) WebAuthnDisplayName() string                { return u.displayName }
func (u *webauthnUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

type passkeyRegistrationMeta struct {
	SessionData *webauthn.SessionData `json:"session_data"`
	UserID      string                `json:"user_id,omitempty"`
	DisplayName string                `json:"display_name,omitempty"`
	Email       string                `json:"email,omitempty"`
	IsHost      bool                  `json:"is_host,omitempty"`
}

type passkeyInfoResponse struct {
	ID           string `json:"id"`
	FriendlyName string `json:"friendly_name"`
	CreatedAt    string `json:"created_at"`
	LastUsedAt   string `json:"last_used_at,omitempty"`
}

// NewPasskeyHandler creates a PasskeyHandler with WebAuthn configuration.
func NewPasskeyHandler(authStore store.AuthStore, passkeyStore store.PasskeyStore, authHandler *AuthHandler) (*PasskeyHandler, error) {
	rpID := os.Getenv("WEBAUTHN_RP_ID")
	if rpID == "" {
		rpID = "localhost"
	}

	originsEnv := os.Getenv("WEBAUTHN_ORIGINS")
	var origins []string
	if originsEnv != "" {
		for _, o := range strings.Split(originsEnv, ",") {
			if trimmed := strings.TrimSpace(o); trimmed != "" {
				origins = append(origins, trimmed)
			}
		}
	}
	if len(origins) == 0 {
		origins = []string{
			"http://localhost:3000",
			"http://localhost:8080",
			"http://127.0.0.1:3000",
			"http://127.0.0.1:8080",
			"https://micup.live",
		}
	}

	w, err := webauthn.New(&webauthn.Config{
		RPDisplayName: "MicUp.live",
		RPID:          rpID,
		RPOrigins:     origins,
	})
	if err != nil {
		return nil, fmt.Errorf("init webauthn: %w", err)
	}

	return &PasskeyHandler{
		authStore:    authStore,
		passkeyStore: passkeyStore,
		authHandler:  authHandler,
		webauthn:     w,
	}, nil
}

// getWebAuthn returns the configured, allowlisted relying-party configuration.
func (h *PasskeyHandler) getWebAuthn(r *http.Request) *webauthn.WebAuthn {
	return h.webauthn
}

// resolveUser extracts the authenticated user from request context or Authorization header.
func (h *PasskeyHandler) resolveUser(r *http.Request) *models.User {
	if u := UserFromContext(r.Context()); u != nil {
		if u.Deleting {
			return nil
		}
		return u
	}
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		claims, err := h.authHandler.parseAccessToken(token)
		if err == nil && claims != nil && claims.Action == "" {
			if u, err := h.authStore.GetUser(r.Context(), claims.UserID); err == nil && u != nil && !u.Deleting && u.AuthEpoch == claims.AuthEpoch {
				return u
			}
		}
	}
	return nil
}

// RegisterOptions generates WebAuthn registration options for a new or existing user.
func (h *PasskeyHandler) RegisterOptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
		IsHost      bool   `json:"is_host"`
	}
	_ = decodeJSON(w, r, &req, authBodyLimit)

	currentUser := h.resolveUser(r)

	var wUser *webauthnUser
	var meta passkeyRegistrationMeta

	if currentUser != nil {
		// Existing logged in user adding a passkey
		creds, err := h.loadUserWebAuthnCredentials(ctx, currentUser.ID)
		if err != nil {
			writeAuthError(w, http.StatusInternalServerError, "failed to load credentials")
			return
		}
		wUser = &webauthnUser{
			id:          []byte(currentUser.ID),
			name:        currentUser.Email,
			displayName: currentUser.DisplayName,
			credentials: creds,
		}
		if currentUser.Email == "" {
			wUser.name = currentUser.DisplayName
		}
		meta.UserID = currentUser.ID
		meta.Email = currentUser.Email
		meta.DisplayName = currentUser.DisplayName
		meta.IsHost = currentUser.IsHost
	} else {
		// New registration
		displayName := strings.TrimSpace(req.DisplayName)
		if displayName == "" {
			writeAuthError(w, http.StatusBadRequest, "Name is required to sign up with a passkey")
			return
		}
		email := normalizeEmail(req.Email)
		if email == "" || !strings.Contains(email, "@") {
			writeAuthError(w, http.StatusBadRequest, "A valid email address is required to sign up with a passkey")
			return
		}
		if existing, _ := h.authStore.GetUserByEmail(ctx, email); existing != nil {
			writeAuthError(w, http.StatusConflict, "An account with this email already exists. Please sign in instead.")
			return
		}
		tempID := newID()
		wUser = &webauthnUser{
			id:          []byte(tempID),
			name:        email,
			displayName: displayName,
		}
		meta.UserID = tempID
		meta.DisplayName = displayName
		meta.Email = email
		meta.IsHost = req.IsHost
	}

	wn := h.getWebAuthn(r)
	creation, sessionData, err := wn.BeginRegistration(
		wUser,
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementPreferred),
	)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, fmt.Sprintf("begin registration: %v", err))
		return
	}

	meta.SessionData = sessionData
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "serialize session")
		return
	}

	sessionID := newRandomToken()
	if err := h.passkeyStore.SaveWebAuthnSession(ctx, &models.WebAuthnSession{
		SessionID:   sessionID,
		UserID:      meta.UserID,
		Challenge:   sessionData.Challenge,
		SessionData: metaBytes,
		ExpiresAt:   time.Now().UTC().Add(webAuthnSessionTTL),
	}); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "failed to save registration session")
		return
	}

	log.Printf("[PASSKEY] Began registration: user=%s session=%s", meta.UserID, sessionID)
	writeAuthJSON(w, http.StatusOK, map[string]interface{}{
		"session_id": sessionID,
		"options":    creation,
	})
}

// RegisterVerify verifies attestation and registers the passkey.
func (h *PasskeyHandler) RegisterVerify(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sessionID := extractSessionID(r)
	if sessionID == "" {
		writeAuthError(w, http.StatusBadRequest, "session_id required")
		return
	}

	session, err := h.passkeyStore.ConsumeWebAuthnSession(ctx, sessionID)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid or expired registration session")
		return
	}

	var meta passkeyRegistrationMeta
	if err := json.Unmarshal(session.SessionData, &meta); err != nil || meta.SessionData == nil {
		writeAuthError(w, http.StatusInternalServerError, "invalid session payload")
		return
	}

	friendlyName := strings.TrimSpace(r.URL.Query().Get("friendly_name"))
	if friendlyName == "" {
		friendlyName = "Passkey Device"
	}

	targetUser, isNewUser, err := h.resolveRegistrationTarget(ctx, r, meta)
	if err != nil {
		writeAuthError(w, http.StatusUnauthorized, "invalid access token")
		return
	}

	var creds []webauthn.Credential
	if !isNewUser {
		creds, _ = h.loadUserWebAuthnCredentials(ctx, targetUser.ID)
	}
	wUser := &webauthnUser{
		id:          []byte(targetUser.ID),
		name:        targetUser.Email,
		displayName: targetUser.DisplayName,
		credentials: creds,
	}
	if targetUser.Email == "" {
		wUser.name = targetUser.DisplayName
	}

	wn := h.getWebAuthn(r)
	credential, err := wn.FinishRegistration(wUser, *meta.SessionData, r)
	if err != nil {
		fmt.Printf("[Passkey RegisterVerify ERROR FinishRegistration] %v\n", err)
		writeAuthError(w, http.StatusBadRequest, fmt.Sprintf("registration failed: %v", err))
		return
	}

	pk := buildPasskeyCredential(targetUser.ID, friendlyName, credential)
	if !h.persistRegisteredPasskey(ctx, w, targetUser, pk, isNewUser) {
		return
	}

	log.Printf("[PASSKEY] Passkey registered: cred=%s name=%q user=%s", pk.ID, pk.FriendlyName, targetUser.ID)
	if isNewUser && targetUser.Email != "" {
		h.authHandler.SendVerificationEmailSafe(h.authHandler.publicBaseURL(r), targetUser)
	}
	// Issue login session
	h.authHandler.WriteSession(w, r, targetUser, http.StatusOK)
}

func extractSessionID(r *http.Request) string {
	if sessionID := r.URL.Query().Get("session_id"); sessionID != "" {
		return sessionID
	}
	return r.Header.Get("X-Session-ID")
}

func (h *PasskeyHandler) resolveRegistrationTarget(ctx context.Context, r *http.Request, meta passkeyRegistrationMeta) (*models.User, bool, error) {
	var targetUser *models.User
	if meta.UserID != "" {
		targetUser, _ = h.authStore.GetUser(ctx, meta.UserID)
	}
	if targetUser == nil {
		targetUser = h.resolveUser(r)
	}
	if targetUser != nil && targetUser.Deleting {
		return nil, false, errors.New("invalid access token")
	}
	if targetUser != nil {
		return targetUser, false, nil
	}

	now := time.Now().UTC()
	displayName := meta.DisplayName
	if displayName == "" {
		displayName = "Singer"
	}
	return &models.User{
		ID:          meta.UserID,
		Email:       meta.Email,
		DisplayName: displayName,
		IsHost:      meta.IsHost,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, true, nil
}

func buildPasskeyCredential(userID, friendlyName string, credential *webauthn.Credential) *models.PasskeyCredential {
	transports := make([]string, 0, len(credential.Transport))
	for _, t := range credential.Transport {
		transports = append(transports, string(t))
	}
	now := time.Now().UTC()
	return &models.PasskeyCredential{
		ID:              base64.RawURLEncoding.EncodeToString(credential.ID),
		UserID:          userID,
		PublicKey:       credential.PublicKey,
		AttestationType: credential.AttestationType,
		AAGUID:          credential.Authenticator.AAGUID,
		SignCount:       credential.Authenticator.SignCount,
		Transports:      transports,
		BackupEligible:  credential.Flags.BackupEligible,
		BackupState:     credential.Flags.BackupState,
		UserPresent:     credential.Flags.UserPresent,
		UserVerified:    credential.Flags.UserVerified,
		FriendlyName:    friendlyName,
		CreatedAt:       now,
		LastUsedAt:      now,
	}
}

func (h *PasskeyHandler) persistRegisteredPasskey(ctx context.Context, w http.ResponseWriter, targetUser *models.User, pk *models.PasskeyCredential, isNewUser bool) bool {
	if isNewUser {
		if err := h.passkeyStore.CreateUserWithPasskey(ctx, targetUser, pk); err != nil {
			fmt.Printf("[Passkey RegisterVerify ERROR CreateUserWithPasskey] %v\n", err)
			writeAuthError(w, http.StatusConflict, "An account with this email already exists or registration could not be completed.")
			return false
		}
		return true
	}
	if err := h.passkeyStore.SavePasskeyCredential(ctx, pk); err != nil {
		fmt.Printf("[Passkey RegisterVerify ERROR SavePasskeyCredential] %v\n", err)
		writeAuthError(w, http.StatusInternalServerError, fmt.Sprintf("failed to store passkey: %v", err))
		return false
	}
	return true
}

// LoginOptions initiates a discoverable or email-directed passkey assertion.
func (h *PasskeyHandler) LoginOptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Email string `json:"email"`
	}
	_ = decodeJSON(w, r, &req, authBodyLimit)

	var assertion *protocol.CredentialAssertion
	var sessionData *webauthn.SessionData
	var err error

	email := normalizeEmail(req.Email)
	wn := h.getWebAuthn(r)
	if email != "" {
		user, err := h.authStore.GetUserByEmail(ctx, email)
		if err != nil || user == nil || user.Deleting {
			writeAuthError(w, http.StatusUnauthorized, "no user with this email")
			return
		}
		creds, err := h.loadUserWebAuthnCredentials(ctx, user.ID)
		if err != nil || len(creds) == 0 {
			writeAuthError(w, http.StatusUnauthorized, "no passkeys registered for this account")
			return
		}
		wUser := &webauthnUser{
			id:          []byte(user.ID),
			name:        user.Email,
			displayName: user.DisplayName,
			credentials: creds,
		}
		assertion, sessionData, err = wn.BeginLogin(wUser)
		if err != nil {
			writeAuthError(w, http.StatusInternalServerError, "failed to begin passkey login")
			return
		}
	} else {
		// 1-Tap Discoverable Login (Resident Key)
		assertion, sessionData, err = wn.BeginDiscoverableLogin(
			webauthn.WithUserVerification(protocol.VerificationPreferred),
		)
		if err != nil {
			writeAuthError(w, http.StatusInternalServerError, "failed to begin discoverable login")
			return
		}
	}

	sessionBytes, err := json.Marshal(sessionData)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "serialize session")
		return
	}

	sessionID := newRandomToken()
	if err := h.passkeyStore.SaveWebAuthnSession(ctx, &models.WebAuthnSession{
		SessionID:   sessionID,
		UserID:      string(sessionData.UserID),
		Challenge:   sessionData.Challenge,
		SessionData: sessionBytes,
		ExpiresAt:   time.Now().UTC().Add(webAuthnSessionTTL),
	}); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "failed to save login session")
		return
	}

	log.Printf("[PASSKEY] Began login ceremony: session=%s email=%q", sessionID, email)
	writeAuthJSON(w, http.StatusOK, map[string]interface{}{
		"session_id": sessionID,
		"options":    assertion,
	})
}

// LoginVerify verifies assertion and authenticates the user.
func (h *PasskeyHandler) LoginVerify(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sessionID := extractSessionID(r)
	if sessionID == "" {
		writeAuthError(w, http.StatusBadRequest, "session_id required")
		return
	}

	session, err := h.passkeyStore.ConsumeWebAuthnSession(ctx, sessionID)
	if err != nil {
		writeAuthError(w, http.StatusUnauthorized, "invalid or expired login session")
		return
	}

	var sessionData webauthn.SessionData
	if err := json.Unmarshal(session.SessionData, &sessionData); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "corrupted session data")
		return
	}

	parsedResponse, err := protocol.ParseCredentialRequestResponse(r)
	if err != nil {
		log.Printf("[PASSKEY WARN] ParseCredentialRequestResponse failed: %v", err)
		writeAuthError(w, http.StatusBadRequest, fmt.Sprintf("invalid assertion response: %v", err))
		return
	}

	userID := resolveLoginUserID(sessionData, parsedResponse)
	if len(userID) == 0 {
		writeAuthError(w, http.StatusUnauthorized, "user handle required")
		return
	}
	user, ok := h.loadLoginUser(ctx, w, userID)
	if !ok {
		return
	}
	stored, err := h.passkeyStore.ListPasskeyCredentialsForUser(ctx, user.ID)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "failed to load credentials")
		return
	}

	wUser, matched := matchLoginCredential(user, stored, parsedResponse)
	if matched == nil {
		writeAuthError(w, http.StatusUnauthorized, "credential not found")
		return
	}

	credential, err := validatePasskeyAssertion(h.getWebAuthn(r), wUser, sessionData, parsedResponse)
	if err != nil {
		log.Printf("[PASSKEY WARN] Login verification failed: %v", err)
		writeAuthError(w, http.StatusUnauthorized, fmt.Sprintf("passkey verification failed: %v", err))
		return
	}

	err = h.passkeyStore.RecordPasskeyUse(ctx, matched, store.PasskeyUsage{
		SignCount:      credential.Authenticator.SignCount,
		BackupEligible: credential.Flags.BackupEligible,
		BackupState:    credential.Flags.BackupState,
	})
	if errors.Is(err, store.ErrPasskeyChanged) || errors.Is(err, store.ErrAccountDeleting) {
		writeAuthError(w, http.StatusUnauthorized, "passkey is no longer available")
		return
	}
	if err != nil {
		log.Printf("[PASSKEY WARN] Record passkey use failed: %v", err)
		writeAuthError(w, http.StatusInternalServerError, "failed to record passkey login")
		return
	}

	log.Printf("[PASSKEY] Login verified: cred=%s user=%s (%s)", matched.ID, user.ID, user.DisplayName)
	h.authHandler.WriteSession(w, r, user, http.StatusOK)
}

func resolveLoginUserID(sessionData webauthn.SessionData, parsedResponse *protocol.ParsedCredentialAssertionData) []byte {
	if len(sessionData.UserID) > 0 {
		return sessionData.UserID
	}
	return parsedResponse.Response.UserHandle
}

func (h *PasskeyHandler) loadLoginUser(ctx context.Context, w http.ResponseWriter, userID []byte) (*models.User, bool) {
	user, err := h.authStore.GetUser(ctx, string(userID))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		writeAuthError(w, http.StatusInternalServerError, "failed to load user")
		return nil, false
	}
	if err != nil || user == nil || user.Deleting {
		writeAuthError(w, http.StatusUnauthorized, "user not found")
		return nil, false
	}
	return user, true
}

func matchLoginCredential(user *models.User, stored []*models.PasskeyCredential, parsedResponse *protocol.ParsedCredentialAssertionData) (*webauthnUser, *models.PasskeyCredential) {
	var matched *models.PasskeyCredential
	wUser := &webauthnUser{id: []byte(user.ID), name: user.Email, displayName: user.DisplayName}
	for _, record := range stored {
		c := toWebAuthnCredential(record)
		if matched == nil && bytes.Equal(c.ID, parsedResponse.RawID) {
			matched = record
			// Older registrations omitted backup eligibility. Verify the asserted
			// flags in memory before committing them to the same stored record.
			if parsedResponse.Response.AuthenticatorData.Flags.HasBackupEligible() {
				c.Flags.BackupEligible = true
			}
		}
		wUser.credentials = append(wUser.credentials, c)
	}
	return wUser, matched
}

func validatePasskeyAssertion(wn *webauthn.WebAuthn, wUser *webauthnUser, sessionData webauthn.SessionData, parsedResponse *protocol.ParsedCredentialAssertionData) (*webauthn.Credential, error) {
	if len(sessionData.UserID) > 0 {
		return wn.ValidateLogin(wUser, sessionData, parsedResponse)
	}
	_, credential, err := wn.ValidatePasskeyLogin(func(_, _ []byte) (webauthn.User, error) {
		return wUser, nil
	}, sessionData, parsedResponse)
	return credential, err
}

// ListPasskeys lists all registered passkeys for the authenticated user.
func (h *PasskeyHandler) ListPasskeys(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := UserFromContext(ctx)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	creds, err := h.passkeyStore.ListPasskeyCredentialsForUser(ctx, user.ID)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "failed to list passkeys")
		return
	}

	resp := make([]passkeyInfoResponse, 0, len(creds))
	for _, c := range creds {
		lastUsed := ""
		if !c.LastUsedAt.IsZero() {
			lastUsed = c.LastUsedAt.UTC().Format(time.RFC3339)
		}
		resp = append(resp, passkeyInfoResponse{
			ID:           c.ID,
			FriendlyName: c.FriendlyName,
			CreatedAt:    c.CreatedAt.UTC().Format(time.RFC3339),
			LastUsedAt:   lastUsed,
		})
	}

	writeAuthJSON(w, http.StatusOK, map[string]interface{}{"passkeys": resp})
}

// DeletePasskey revokes a passkey credential belonging to the authenticated user.
func (h *PasskeyHandler) DeletePasskey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := UserFromContext(ctx)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	id := r.PathValue("id")
	if id == "" {
		writeAuthError(w, http.StatusBadRequest, "passkey ID required")
		return
	}

	if err := h.passkeyStore.DeletePasskeyCredential(ctx, id, user.ID); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "failed to delete passkey")
		return
	}

	log.Printf("[PASSKEY] Revoked passkey %s for user %s", id, user.ID)
	writeAuthJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (h *PasskeyHandler) loadUserWebAuthnCredentials(ctx context.Context, userID string) ([]webauthn.Credential, error) {
	stored, err := h.passkeyStore.ListPasskeyCredentialsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]webauthn.Credential, 0, len(stored))
	for _, s := range stored {
		out = append(out, toWebAuthnCredential(s))
	}
	return out, nil
}

func toWebAuthnCredential(c *models.PasskeyCredential) webauthn.Credential {
	credID, err := base64.RawURLEncoding.DecodeString(c.ID)
	if err != nil {
		credID, err = base64.URLEncoding.DecodeString(c.ID)
		if err != nil {
			credID = []byte(c.ID)
		}
	}
	transports := make([]protocol.AuthenticatorTransport, 0, len(c.Transports))
	for _, t := range c.Transports {
		transports = append(transports, protocol.AuthenticatorTransport(t))
	}
	return webauthn.Credential{
		ID:              credID,
		PublicKey:       c.PublicKey,
		AttestationType: c.AttestationType,
		Transport:       transports,
		Flags: webauthn.CredentialFlags{
			UserPresent:    c.UserPresent,
			UserVerified:   c.UserVerified,
			BackupEligible: c.BackupEligible,
			BackupState:    c.BackupState,
		},
		Authenticator: webauthn.Authenticator{
			AAGUID:    c.AAGUID,
			SignCount: c.SignCount,
		},
	}
}

func newRandomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
