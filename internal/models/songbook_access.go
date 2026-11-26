package models

import "time"

// SongbookInvitationStatus represents the lifecycle state of a songbook collaboration invitation.
type SongbookInvitationStatus string

const (
	InvitationStatusPending  SongbookInvitationStatus = "pending"
	InvitationStatusAccepted SongbookInvitationStatus = "accepted"
	InvitationStatusCanceled SongbookInvitationStatus = "canceled"
	InvitationStatusReplaced SongbookInvitationStatus = "replaced"
)

// SongbookPermissions describes effective permissions for a caller on a songbook.
type SongbookPermissions struct {
	IsOwner         bool `json:"is_owner"`
	CanEditCatalog  bool `json:"can_edit_catalog"`
	CanHostShows    bool `json:"can_host_shows"`
	CanManageBook   bool `json:"can_manage_book"`
	CanManageAccess bool `json:"can_manage_access"`
}

// SongbookGrant represents an accepted collaborator's permissions on a songbook.
type SongbookGrant struct {
	SongbookID     string    `json:"songbook_id"`
	UserID         string    `json:"user_id"`
	CanEditCatalog bool      `json:"can_edit_catalog"`
	CanHostShows   bool      `json:"can_host_shows"`
	GrantedBy      string    `json:"granted_by"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// SongbookMember is the owner-facing projection of an accepted collaborator grant.
type SongbookMember struct {
	UserID         string    `json:"user_id"`
	Email          string    `json:"email"`
	DisplayName    string    `json:"display_name"`
	CanEditCatalog bool      `json:"can_edit_catalog"`
	CanHostShows   bool      `json:"can_host_shows"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// SongbookInvitation represents an invitation to collaborate on a songbook.
// TokenHash stores the SHA-256 hex digest of the single-use acceptance token;
// the raw token is never persisted.
type SongbookInvitation struct {
	ID               string                   `json:"id"`
	SongbookID       string                   `json:"songbook_id"`
	OwnerID          string                   `json:"-"`
	RecipientEmail   string                   `json:"recipient_email"`
	CanEditCatalog   bool                     `json:"can_edit_catalog"`
	CanHostShows     bool                     `json:"can_host_shows"`
	TokenHash        string                   `json:"-"`
	Status           SongbookInvitationStatus `json:"status"`
	ExpiresAt        time.Time                `json:"expires_at"`
	LastSentAt       *time.Time               `json:"last_sent_at,omitempty"`
	SendError        string                   `json:"send_error,omitempty"`
	AcceptedByUserID string                   `json:"-"`
	AcceptedAt       *time.Time               `json:"accepted_at,omitempty"`
	CreatedAt        time.Time                `json:"created_at"`
	UpdatedAt        time.Time                `json:"updated_at"`
}

// SongbookInvitationPreview is the recipient-facing metadata shown before acceptance.
type SongbookInvitationPreview struct {
	SongbookID     string                   `json:"songbook_id"`
	SongbookName   string                   `json:"songbook_name"`
	InviterName    string                   `json:"inviter_name"`
	RecipientEmail string                   `json:"recipient_email"`
	CanEditCatalog bool                     `json:"can_edit_catalog"`
	CanHostShows   bool                     `json:"can_host_shows"`
	Status         SongbookInvitationStatus `json:"status"`
	ExpiresAt      time.Time                `json:"expires_at"`
	Expired        bool                     `json:"expired"`
	Authenticated  bool                     `json:"authenticated"`
	EmailMatches   bool                     `json:"email_matches"`
	EmailVerified  bool                     `json:"email_verified"`
	IsHost         bool                     `json:"is_host"`
}

// SongbookAccessOverview aggregates accepted members and pending invitations for the owner.
type SongbookAccessOverview struct {
	Members     []*SongbookMember     `json:"members"`
	Invitations []*SongbookInvitation `json:"invitations"`
}
