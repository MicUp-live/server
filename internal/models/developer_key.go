package models

import "time"

// DeveloperKey is a scoped Personal Access Token issued to a host user for
// third-party integration (e.g. Hazz, OpenKJ, custom scripts). The raw key is
// shown once at creation; only its SHA-256 hash is persisted (ID = hex hash).
type DeveloperKey struct {
	ID        string   `json:"id"`
	OwnerID   string   `json:"owner_id"`
	Name      string   `json:"name"`
	KeyPrefix string   `json:"key_prefix"`
	Scopes    []string `json:"scopes"`
	// ScopeVersion distinguishes newly issued keys from records created before
	// route enforcement. It is intentionally omitted from the public response.
	ScopeVersion int       `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	LastUsedAt   time.Time `json:"last_used_at,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	Revoked      bool      `json:"revoked"`
	RevokedAt    time.Time `json:"revoked_at,omitempty"`
}

// KeyResponse is the public shape returned after a key is created, including the
// raw secret which is only available at creation time.
type KeyResponse struct {
	DeveloperKey
	RawKey string `json:"raw_key"`
}
