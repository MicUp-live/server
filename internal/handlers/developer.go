// Package handlers provides HTTP endpoints for the MicUp API. The developer
// handler issues and manages scoped Personal Access Tokens and authenticates
// third-party integrations (Hazz, OpenKJ, custom scripts) that use them.
package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"micup.live/server/internal/models"
	"micup.live/server/internal/store"
)

const (
	// developerKeyPrefix is the human-identifiable prefix of a raw API key.
	developerKeyPrefix = "mk_live_"
	// developerKeySecretLen is the number of random bytes in the key secret.
	developerKeySecretLen = 32
	// developerKeyDisplayLen is the number of characters shown for a key prefix.
	developerKeyDisplayLen = 12
	// DeveloperKeyScopeReport preserves access while logging missing scopes.
	DeveloperKeyScopeReport = "report"
	// DeveloperKeyScopeEnforce returns 403 for missing scopes.
	DeveloperKeyScopeEnforce = "enforce"
	// DeveloperScopeEventsRead authorizes event reads.
	DeveloperScopeEventsRead = "events:read"
	// DeveloperScopeEventsWrite authorizes event controls and mutations.
	DeveloperScopeEventsWrite = "events:write"
	// DeveloperScopeRequestsRead authorizes queue reads.
	DeveloperScopeRequestsRead = "requests:read"
	// DeveloperScopeRequestsWrite authorizes queue mutations.
	DeveloperScopeRequestsWrite = "requests:write"
	// DeveloperScopeCatalogsWrite authorizes catalog and import operations.
	DeveloperScopeCatalogsWrite = "catalogs:write"
)

// DeveloperKeyContextKey carries the resolved developer key into the request context.
type DeveloperKeyContextKey struct{}

type createKeyRequest struct {
	Name          string   `json:"name"`
	Scopes        []string `json:"scopes"`
	ExpiresInDays *int     `json:"expires_in_days"`
}

// DeveloperHandler issues and manages scoped Personal Access Tokens.
type DeveloperHandler struct {
	store     store.DeveloperKeyStore
	authStore store.AuthStore
	// ScopeMode is report by default for a safe staged rollout. Set it to
	// enforce after existing integrations have been inventoried or migrated.
	ScopeMode string
}

// NewDeveloperHandler wires the developer token store and auth store.
func NewDeveloperHandler(keyStore store.DeveloperKeyStore, authStore store.AuthStore) *DeveloperHandler {
	return &DeveloperHandler{store: keyStore, authStore: authStore, ScopeMode: DeveloperKeyScopeReport}
}

// CreateKey issues a new scoped key and returns the raw secret exactly once.
func (h *DeveloperHandler) CreateKey(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	var req createKeyRequest
	if err := decodeJSON(w, r, &req, authBodyLimit); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	scopes := normalizeScopes(req.Scopes)

	rawKey, err := newRawKey()
	if err != nil {
		http.Error(w, "unable to generate key", http.StatusInternalServerError)
		return
	}
	key := &models.DeveloperKey{
		OwnerID:      user.ID,
		Name:         name,
		KeyPrefix:    displayPrefix(rawKey),
		Scopes:       scopes,
		ScopeVersion: 1,
	}
	if req.ExpiresInDays != nil && *req.ExpiresInDays > 0 {
		key.ExpiresAt = time.Now().UTC().Add(time.Duration(*req.ExpiresInDays) * 24 * time.Hour)
	}
	if err := h.store.CreateDeveloperKey(r.Context(), rawKey, key); err != nil {
		log.Printf("[DEVKEY ERROR] failed to create key for user %s: %v", user.ID, err)
		http.Error(w, "unable to create key", http.StatusInternalServerError)
		return
	}
	loaded, err := h.store.GetDeveloperKey(r.Context(), rawKey)
	if err != nil {
		http.Error(w, "unable to load created key", http.StatusInternalServerError)
		return
	}
	log.Printf("[DEVKEY] created key %q for user %s", name, user.ID)
	writeDeveloperJSON(w, http.StatusCreated, models.KeyResponse{DeveloperKey: *loaded, RawKey: rawKey})
}

// ListKeys returns every key issued to the authenticated host, newest first.
func (h *DeveloperHandler) ListKeys(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	keys, err := h.store.ListDeveloperKeys(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "unable to list keys", http.StatusInternalServerError)
		return
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].CreatedAt.After(keys[j].CreatedAt)
	})
	writeDeveloperJSON(w, http.StatusOK, keys)
}

// RevokeKey permanently deletes a key after verifying it belongs to the host.
func (h *DeveloperHandler) RevokeKey(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	if err := h.store.RevokeDeveloperKey(r.Context(), r.PathValue("id"), user.ID); err != nil {
		http.Error(w, "unable to revoke key", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeveloperFromContext returns the resolved developer key added by RequireDeveloperKey.
func DeveloperFromContext(ctx context.Context) *models.DeveloperKey {
	key, _ := ctx.Value(DeveloperKeyContextKey{}).(*models.DeveloperKey)
	return key
}

// RequireDeveloperKey authenticates a Bearer mk_live_ credential, loads its record,
// verifies it is unrevoked and unexpired, and attaches it to the request context.
func (h *DeveloperHandler) RequireDeveloperKey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(r.Header.Get("Authorization"))
		token = strings.TrimPrefix(token, "Bearer ")
		if !strings.HasPrefix(token, developerKeyPrefix) {
			h.fail(w, http.StatusUnauthorized, "developer key required", "")
			return
		}
		key, err := h.store.GetDeveloperKey(r.Context(), token)
		if err != nil || key == nil {
			h.fail(w, http.StatusUnauthorized, "invalid developer key", ref(token))
			return
		}
		if key.Revoked {
			h.fail(w, http.StatusForbidden, "developer key revoked", key.ID)
			return
		}
		if !key.ExpiresAt.IsZero() && time.Now().UTC().After(key.ExpiresAt) {
			h.fail(w, http.StatusForbidden, "developer key expired", key.ID)
			return
		}
		_ = h.store.TouchDeveloperKeyLastUsed(r.Context(), key.ID)
		ctx := r.Context()
		if h.authStore != nil {
			user, err := h.authStore.GetUser(r.Context(), key.OwnerID)
			if err != nil || user == nil || user.Deleting {
				h.fail(w, http.StatusUnauthorized, "developer key owner not found", key.ID)
				return
			}
			if !user.IsHost {
				h.fail(w, http.StatusForbidden, "host access is required", key.ID)
				return
			}
			ctx = context.WithValue(ctx, userContextKey, user)
		} else {
			ctx = context.WithValue(ctx, userContextKey, &models.User{
				ID:     key.OwnerID,
				IsHost: true,
			})
		}
		ctx = context.WithValue(ctx, DeveloperKeyContextKey{}, key)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// RequireScope checks the route scope after RequireDeveloperKey has attached a
// developer key. JWT/session requests pass through unchanged because this
// middleware is only mounted in the developer-key branch of the router.
func (h *DeveloperHandler) RequireScope(scope string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := DeveloperFromContext(r.Context())
		if key == nil || hasDeveloperScope(key, scope) {
			next.ServeHTTP(w, r)
			return
		}
		mode := h.ScopeMode
		if mode == "" {
			mode = DeveloperKeyScopeReport
		}
		if mode == DeveloperKeyScopeEnforce {
			writeDeveloperJSON(w, http.StatusForbidden, map[string]string{
				"error":          "insufficient_scope",
				"detail":         "developer key does not include the required scope",
				"required_scope": scope,
			})
			return
		}
		// Scope reports deliberately contain no raw token, key prefix, or other
		// credential material.
		log.Printf("[DEVKEY SCOPE REPORT] would deny scope=%s owner=%s", scope, key.OwnerID)
		next.ServeHTTP(w, r)
	}
}

func hasDeveloperScope(key *models.DeveloperKey, required string) bool {
	if key.ScopeVersion == 0 || len(key.Scopes) == 0 {
		// Records created before route scope enforcement have no scope version.
		// They retain their intended access while operators migrate them explicitly.
		return true
	}
	for _, scope := range key.Scopes {
		if strings.TrimSpace(scope) == required {
			return true
		}
	}
	return false
}

// fail writes a structured JSON error and logs it with the appropriate severity.
func (h *DeveloperHandler) fail(w http.ResponseWriter, status int, msg, ref string) {
	if status >= 500 {
		log.Printf("[DEVKEY ERROR] %d %s", status, msg)
	} else if status >= 400 {
		log.Printf("[DEVKEY WARN] %d %s ref=%s", status, msg, ref)
	}
	writeDeveloperJSON(w, status, map[string]string{"detail": msg})
}

// newRawKey generates a fresh mk_live_ token with 32 random bytes of entropy.
func newRawKey() (string, error) {
	buf := make([]byte, developerKeySecretLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return developerKeyPrefix + hex.EncodeToString(buf), nil
}

// displayPrefix returns a short, safe-to-log prefix of a raw key.
func displayPrefix(rawKey string) string {
	if len(rawKey) <= developerKeyDisplayLen {
		return rawKey
	}
	return rawKey[:developerKeyDisplayLen]
}

// ref returns a short, non-sensitive reference to a raw key for logging.
func ref(rawKey string) string {
	p := strings.TrimPrefix(rawKey, developerKeyPrefix)
	if len(p) > 4 {
		p = p[:4]
	}
	return developerKeyPrefix + p
}

// normalizeScopes de-duplicates requested scopes, defaulting to a read/write set.
func normalizeScopes(scopes []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		result = append(result, scope)
	}
	if len(result) == 0 {
		return []string{DeveloperScopeEventsRead, DeveloperScopeEventsWrite, DeveloperScopeRequestsRead, DeveloperScopeRequestsWrite, DeveloperScopeCatalogsWrite}
	}
	return result
}

func writeDeveloperJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("[DEVKEY ERROR] failed to encode response: %v", err)
	}
}
