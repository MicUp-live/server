// Package models defines the API and persistence data structures.
package models

import "time"

// User is an authenticated account.
type User struct {
	ID                     string           `json:"id"`
	Email                  string           `json:"email"`
	DisplayName            string           `json:"display_name"`
	AvatarURL              string           `json:"avatar_url,omitempty"`
	PasswordHash           string           `json:"-"`
	AuthEpoch              string           `json:"-"` // Empty for legacy accounts; reset advances it.
	IsHost                 bool             `json:"is_host"`
	EmailVerified          bool             `json:"email_verified"`
	Deleting               bool             `json:"-"`
	DeletionEpoch          string           `json:"-"`
	DeletionAuthEpoch      string           `json:"-"`
	DeletionStartedAt      time.Time        `json:"-"`
	DeletionStep           string           `json:"-"`
	DeletionError          string           `json:"-"`
	PendingCatalogVersions []string         `json:"-"`
	FavoriteSongs          []FavoriteSong   `json:"favorite_songs,omitempty" dynamodbav:"favorite_songs,omitempty"`
	FavoriteArtists        []FavoriteArtist `json:"favorite_artists,omitempty" dynamodbav:"favorite_artists,omitempty"`
	CreatedAt              time.Time        `json:"created_at"`
	UpdatedAt              time.Time        `json:"updated_at"`
}

// AuthSession is a revocable refresh-token session.
type AuthSession struct {
	TokenHash []byte
	UserID    string
	AuthEpoch string
	ExpiresAt time.Time
}

// PasskeyCredential represents a registered WebAuthn / FIDO2 credential.
type PasskeyCredential struct {
	ID              string    `json:"id"`
	UserID          string    `json:"user_id"`
	PublicKey       []byte    `json:"-"`
	AttestationType string    `json:"attestation_type"`
	AAGUID          []byte    `json:"-"`
	SignCount       uint32    `json:"sign_count"`
	Transports      []string  `json:"transports,omitempty"`
	BackupEligible  bool      `json:"backup_eligible"`
	BackupState     bool      `json:"backup_state"`
	UserPresent     bool      `json:"user_present"`
	UserVerified    bool      `json:"user_verified"`
	FriendlyName    string    `json:"friendly_name"`
	CreatedAt       time.Time `json:"created_at"`
	LastUsedAt      time.Time `json:"last_used_at,omitempty"`
}

// WebAuthnSession temporarily stores challenge data during registration/login ceremonies.
type WebAuthnSession struct {
	SessionID   string    `json:"session_id"`
	UserID      string    `json:"user_id,omitempty"`
	Challenge   string    `json:"challenge"`
	SessionData []byte    `json:"session_data"`
	ExpiresAt   time.Time `json:"expires_at"`
}
