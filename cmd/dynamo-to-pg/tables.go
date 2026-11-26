package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"micup.live/server/internal/models"
)

type dynamoPasskeyItem struct {
	ID              string   `dynamodbav:"id"`
	UserID          string   `dynamodbav:"user_id"`
	PublicKey       []byte   `dynamodbav:"public_key"`
	AttestationType string   `dynamodbav:"attestation_type"`
	AAGUID          []byte   `dynamodbav:"aaguid"`
	SignCount       uint32   `dynamodbav:"sign_count"`
	Transports      []string `dynamodbav:"transports"`
	BackupEligible  bool     `dynamodbav:"backup_eligible"`
	BackupState     bool     `dynamodbav:"backup_state"`
	UserPresent     bool     `dynamodbav:"user_present"`
	UserVerified    bool     `dynamodbav:"user_verified"`
	FriendlyName    string   `dynamodbav:"friendly_name"`
	CreatedAt       string   `dynamodbav:"created_at"`
	LastUsedAt      string   `dynamodbav:"last_used_at"`
}

func migratePasskeys(ctx context.Context, st *migrationState) (TableStats, error) {
	q := upsertSQL("passkey_credentials", []string{"id", "user_id", "public_key", "attestation_type", "aaguid", "sign_count", "transports", "backup_eligible", "backup_state", "user_present", "user_verified", "friendly_name", "created_at", "last_used_at"}, "id")
	return scanAndMigrate(ctx, st, "passkey_credentials", "passkeys", func(it dynamoPasskeyItem) (preparedRow, bool, string, error) {
		if it.ID == "" || !st.users[it.UserID] {
			return skipRow(fmt.Sprintf("orphaned passkey %q for missing user %q", it.ID, it.UserID))
		}
		created, _, opt, err := parseTimestamps(it.CreatedAt, "", st.now, it.LastUsedAt)
		if err != nil {
			return errRow(err)
		}
		transportsJSON, err := marshalJSONSlice(it.Transports)
		if err != nil {
			return errRow(err)
		}
		var aaguid []byte
		if len(it.AAGUID) > 0 {
			aaguid = it.AAGUID
		}
		return okRow(it.ID, q, []any{it.ID, it.UserID, it.PublicKey, it.AttestationType, aaguid, int64(it.SignCount), transportsJSON, it.BackupEligible, it.BackupState, it.UserPresent, it.UserVerified, it.FriendlyName, created, opt[0]}, nil)
	})
}

type dynamoWebAuthnSessionItem struct {
	SessionID   string `dynamodbav:"session_id"`
	UserID      string `dynamodbav:"user_id"`
	Challenge   string `dynamodbav:"challenge"`
	SessionData []byte `dynamodbav:"session_data"`
	ExpiresAt   string `dynamodbav:"expires_at"`
	TTL         int64  `dynamodbav:"ttl"`
}

func migrateWebAuthnSessions(ctx context.Context, st *migrationState) (TableStats, error) {
	if st.skipEphemeral {
		return skipEphemeralTable(st, "webauthn_sessions", "webauthn-sessions"), nil
	}
	q := upsertSQL("webauthn_sessions", []string{"session_id", "user_id", "challenge", "session_data", "expires_at"}, "session_id")
	return scanAndMigrate(ctx, st, "webauthn_sessions", "webauthn-sessions", func(it dynamoWebAuthnSessionItem) (preparedRow, bool, string, error) {
		if it.SessionID == "" {
			return skipRow("")
		}
		exp, ok, err := parseSessionExpiry(it.ExpiresAt, it.TTL, st.now)
		if err != nil {
			return errRow(err)
		}
		if !ok {
			return skipRow("")
		}
		uid := nullifyMissing(st, "webauthn_sessions", "user_id", it.SessionID, it.UserID, st.users)
		return okRow(it.SessionID, q, []any{it.SessionID, uid, it.Challenge, it.SessionData, exp}, nil)
	})
}

type dynamoDeveloperKeyItem struct {
	KeyHash      string   `dynamodbav:"key_hash"`
	OwnerID      string   `dynamodbav:"owner_id"`
	Name         string   `dynamodbav:"name"`
	KeyPrefix    string   `dynamodbav:"key_prefix"`
	Scopes       []string `dynamodbav:"scopes"`
	ScopeVersion int      `dynamodbav:"scope_version"`
	CreatedAt    string   `dynamodbav:"created_at"`
	LastUsedAt   string   `dynamodbav:"last_used_at"`
	ExpiresAt    string   `dynamodbav:"expires_at"`
	Revoked      bool     `dynamodbav:"revoked"`
	RevokedAt    string   `dynamodbav:"revoked_at"`
}

func migrateDeveloperKeys(ctx context.Context, st *migrationState) (TableStats, error) {
	q := upsertSQL("developer_keys", []string{"id", "owner_id", "name", "key_prefix", "scopes", "scope_version", "revoked", "revoked_at", "last_used_at", "expires_at", "created_at"}, "id")
	return scanAndMigrate(ctx, st, "developer_keys", "developer-keys", func(it dynamoDeveloperKeyItem) (preparedRow, bool, string, error) {
		if it.KeyHash == "" || !st.users[it.OwnerID] {
			return skipRow(fmt.Sprintf("orphaned developer_key %q for missing owner %q", it.KeyHash, it.OwnerID))
		}
		created, _, opt, err := parseTimestamps(it.CreatedAt, "", st.now, it.RevokedAt, it.LastUsedAt, it.ExpiresAt)
		if err != nil {
			return errRow(err)
		}
		scopesJSON, err := marshalJSONSlice(it.Scopes)
		if err != nil {
			return errRow(err)
		}
		return okRow(it.KeyHash, q, []any{it.KeyHash, it.OwnerID, it.Name, it.KeyPrefix, scopesJSON, it.ScopeVersion, it.Revoked, opt[0], opt[1], opt[2], created}, nil)
	})
}

type dynamoCatalogVersionItem struct {
	ID             string `dynamodbav:"id"`
	SchemaVersion  int    `dynamodbav:"schema_version"`
	Status         string `dynamodbav:"status"`
	SongCount      int    `dynamodbav:"song_count"`
	AssetPrefix    string `dynamodbav:"asset_prefix"`
	FailureReason  string `dynamodbav:"failure_reason"`
	LeaseToken     string `dynamodbav:"lease_token"`
	LeaseExpiresAt string `dynamodbav:"lease_expires_at"`
	CreatedAt      string `dynamodbav:"created_at"`
	ReadyAt        string `dynamodbav:"ready_at"`
}

func migrateCatalogVersions(ctx context.Context, st *migrationState) (TableStats, error) {
	q := upsertSQL("catalog_versions", []string{"id", "schema_version", "status", "song_count", "asset_prefix", "failure_reason", "lease_token", "lease_expires_at", "created_at", "ready_at"}, "id")
	return scanAndMigrate(ctx, st, "catalog_versions", "catalog-versions", func(it dynamoCatalogVersionItem) (preparedRow, bool, string, error) {
		if it.ID == "" {
			return skipRow("missing catalog_version id")
		}
		created, _, opt, err := parseTimestamps(it.CreatedAt, "", st.now, it.LeaseExpiresAt, it.ReadyAt)
		if err != nil {
			return errRow(err)
		}
		return okRow(it.ID, q, []any{it.ID, it.SchemaVersion, it.Status, it.SongCount, it.AssetPrefix, it.FailureReason, it.LeaseToken, opt[0], created, opt[1]}, func() {
			st.catalogVersions[it.ID] = true
		})
	})
}

type dynamoSongbookItem struct {
	ID                   string `dynamodbav:"id"`
	OwnerID              string `dynamodbav:"owner_id"`
	PublicID             string `dynamodbav:"public_id"`
	Slug                 string `dynamodbav:"slug"`
	VenueID              string `dynamodbav:"venue_id"`
	CatalogVersionID     string `dynamodbav:"catalog_version_id"`
	Name                 string `dynamodbav:"name"`
	IsPublic             bool   `dynamodbav:"is_public"`
	SongCount            int    `dynamodbav:"song_count"`
	CatalogGeneration    int64  `dynamodbav:"catalog_generation"`
	LastImportGeneration int64  `dynamodbav:"last_import_generation"`
	Deleting             bool   `dynamodbav:"deleting"`
	DeletionConfirmed    bool   `dynamodbav:"deletion_confirmed"`
	CreatedAt            string `dynamodbav:"created_at"`
	UpdatedAt            string `dynamodbav:"updated_at"`
}

func migrateSongbooks(ctx context.Context, st *migrationState) (TableStats, error) {
	q := upsertSQL("songbooks", []string{"id", "owner_id", "public_id", "slug", "venue_id", "catalog_version_id", "name", "is_public", "song_count", "catalog_generation", "last_import_generation", "deleting", "deletion_confirmed", "created_at", "updated_at"}, "id")
	return scanAndMigrate(ctx, st, "songbooks", "songbooks", func(it dynamoSongbookItem) (preparedRow, bool, string, error) {
		if it.ID == "" || !st.users[it.OwnerID] {
			return skipRow(fmt.Sprintf("orphaned songbook %q for missing owner %q", it.ID, it.OwnerID))
		}
		created, updated, _, err := parseTimestamps(it.CreatedAt, it.UpdatedAt, st.now)
		if err != nil {
			return errRow(err)
		}
		catalogVer := nullifyMissing(st, "songbooks", "catalog_version_id", it.ID, it.CatalogVersionID, st.catalogVersions)
		return okRow(it.ID, q, []any{it.ID, it.OwnerID, it.PublicID, nullString(models.NormalizeSlug(it.Slug)), it.VenueID, catalogVer, it.Name, it.IsPublic, it.SongCount, it.CatalogGeneration, it.LastImportGeneration, it.Deleting, it.DeletionConfirmed, created, updated}, func() {
			st.songbooks[it.ID] = true
		})
	})
}

type dynamoSongbookGrantItem struct {
	SongbookID     string `dynamodbav:"songbook_id"`
	UserID         string `dynamodbav:"user_id"`
	CanEditCatalog bool   `dynamodbav:"can_edit_catalog"`
	CanHostShows   bool   `dynamodbav:"can_host_shows"`
	GrantedBy      string `dynamodbav:"granted_by"`
	CreatedAt      string `dynamodbav:"created_at"`
	UpdatedAt      string `dynamodbav:"updated_at"`
}

func migrateSongbookGrants(ctx context.Context, st *migrationState) (TableStats, error) {
	q := upsertSQL("songbook_grants", []string{"songbook_id", "user_id", "can_edit_catalog", "can_host_shows", "granted_by", "created_at", "updated_at"}, "songbook_id", "user_id")
	return scanAndMigrate(ctx, st, "songbook_grants", "songbook-grants", func(it dynamoSongbookGrantItem) (preparedRow, bool, string, error) {
		if !st.songbooks[it.SongbookID] || !st.users[it.UserID] {
			return skipRow(fmt.Sprintf("orphaned grant (%q, %q)", it.SongbookID, it.UserID))
		}
		created, updated, _, err := parseTimestamps(it.CreatedAt, it.UpdatedAt, st.now)
		if err != nil {
			return errRow(err)
		}
		grantedBy := nullifyMissing(st, "songbook_grants", "granted_by", it.SongbookID+"/"+it.UserID, it.GrantedBy, st.users)
		return okRow(it.SongbookID+"/"+it.UserID, q, []any{it.SongbookID, it.UserID, it.CanEditCatalog, it.CanHostShows, grantedBy, created, updated}, nil)
	})
}

type dynamoSongbookInvitationItem struct {
	ID               string `dynamodbav:"id"`
	RecordType       string `dynamodbav:"record_type"`
	SongbookID       string `dynamodbav:"songbook_id"`
	OwnerID          string `dynamodbav:"owner_id"`
	RecipientEmail   string `dynamodbav:"recipient_email"`
	CanEditCatalog   bool   `dynamodbav:"can_edit_catalog"`
	CanHostShows     bool   `dynamodbav:"can_host_shows"`
	TokenHash        string `dynamodbav:"token_hash"`
	Status           string `dynamodbav:"status"`
	ExpiresAt        string `dynamodbav:"expires_at"`
	LastSentAt       string `dynamodbav:"last_sent_at"`
	SendError        string `dynamodbav:"send_error"`
	AcceptedByUserID string `dynamodbav:"accepted_by_user_id"`
	AcceptedAt       string `dynamodbav:"accepted_at"`
	CreatedAt        string `dynamodbav:"created_at"`
	UpdatedAt        string `dynamodbav:"updated_at"`
}

func migrateSongbookInvitations(ctx context.Context, st *migrationState) (TableStats, error) {
	q := upsertSQL("songbook_invitations", []string{"id", "songbook_id", "owner_id", "recipient_email", "can_edit_catalog", "can_host_shows", "token_hash", "status", "expires_at", "last_sent_at", "send_error", "accepted_by_user_id", "accepted_at", "created_at", "updated_at"}, "id")
	return scanAndMigrate(ctx, st, "songbook_invitations", "songbook-invitations", func(it dynamoSongbookInvitationItem) (preparedRow, bool, string, error) {
		if strings.HasPrefix(it.ID, "pending#") || it.RecordType == "pending_slot" {
			return skipRow("")
		}
		if it.ID == "" || !st.songbooks[it.SongbookID] || !st.users[it.OwnerID] {
			return skipRow(fmt.Sprintf("orphaned invitation %q (songbook=%q, owner=%q)", it.ID, it.SongbookID, it.OwnerID))
		}
		created, updated, opt, err := parseTimestamps(it.CreatedAt, it.UpdatedAt, st.now, it.LastSentAt, it.AcceptedAt)
		if err != nil {
			return errRow(err)
		}
		expiresAt, err := parseTimeOrFallback(it.ExpiresAt, created)
		if err != nil {
			return errRow(err)
		}
		tokenHash := strings.TrimSpace(it.TokenHash)
		if tokenHash == "" {
			tokenHash = "revoked:" + it.ID
		}
		acceptedBy := nullifyMissing(st, "songbook_invitations", "accepted_by_user_id", it.ID, it.AcceptedByUserID, st.users)
		return okRow(it.ID, q, []any{it.ID, it.SongbookID, it.OwnerID, strings.ToLower(strings.TrimSpace(it.RecipientEmail)), it.CanEditCatalog, it.CanHostShows, tokenHash, it.Status, expiresAt, opt[0], it.SendError, acceptedBy, opt[1], created, updated}, nil)
	})
}

type dynamoCatalogChangeItem struct {
	SongbookID string       `dynamodbav:"songbook_id"`
	SongID     string       `dynamodbav:"song_id"`
	Song       *models.Song `dynamodbav:"song"`
	Deleted    bool         `dynamodbav:"deleted"`
	Clear      bool         `dynamodbav:"clear"`
	UpdatedAt  string       `dynamodbav:"updated_at"`
}

func migrateCatalogChanges(ctx context.Context, st *migrationState) (TableStats, error) {
	q := upsertSQL("catalog_changes", []string{"songbook_id", "song_id", "song", "deleted", "clear", "updated_at"}, "songbook_id", "song_id")
	return scanAndMigrate(ctx, st, "catalog_changes", "catalog-changes", func(it dynamoCatalogChangeItem) (preparedRow, bool, string, error) {
		if !st.songbooks[it.SongbookID] || it.SongID == "" {
			return skipRow(fmt.Sprintf("orphaned catalog_change (%q, %q)", it.SongbookID, it.SongID))
		}
		updated, err := parseTimeOrFallback(it.UpdatedAt, st.now)
		if err != nil {
			return errRow(err)
		}
		var songJSON []byte
		if it.Song != nil {
			if songJSON, err = json.Marshal(it.Song); err != nil {
				return errRow(err)
			}
		}
		return okRow(it.SongbookID+"/"+it.SongID, q, []any{it.SongbookID, it.SongID, songJSON, it.Deleted, it.Clear, updated}, nil)
	})
}

type dynamoImportJobItem struct {
	ID                     string `dynamodbav:"id"`
	SongbookID             string `dynamodbav:"songbook_id"`
	ManifestKey            string `dynamodbav:"manifest_key"`
	Status                 string `dynamodbav:"status"`
	TotalSongs             int    `dynamodbav:"total_songs"`
	ProcessedSongs         int    `dynamodbav:"processed_songs"`
	PruneMissing           bool   `dynamodbav:"prune_missing"`
	Generation             int64  `dynamodbav:"generation"`
	CatalogVersionID       string `dynamodbav:"catalog_version_id"`
	DispatchReady          bool   `dynamodbav:"dispatch_ready"`
	DispatchClaimToken     string `dynamodbav:"dispatch_claim_token"`
	DispatchClaimExpiresAt string `dynamodbav:"dispatch_claim_expires_at"`
	DispatchAttempts       int    `dynamodbav:"dispatch_attempts"`
	DispatchedAt           string `dynamodbav:"dispatched_at"`
	Error                  string `dynamodbav:"error"`
	CreatedAt              string `dynamodbav:"created_at"`
	UpdatedAt              string `dynamodbav:"updated_at"`
	CompletedAt            string `dynamodbav:"completed_at"`
}

func migrateImportJobs(ctx context.Context, st *migrationState) (TableStats, error) {
	q := upsertSQL("import_jobs", []string{"id", "songbook_id", "manifest_key", "status", "total_songs", "processed_songs", "prune_missing", "generation", "catalog_version_id", "dispatch_ready", "dispatch_claim_token", "dispatch_claim_expires_at", "dispatch_attempts", "dispatched_at", "error", "created_at", "updated_at", "completed_at"}, "id")
	return scanAndMigrate(ctx, st, "import_jobs", "import-jobs", func(it dynamoImportJobItem) (preparedRow, bool, string, error) {
		if it.ID == "" || !st.songbooks[it.SongbookID] {
			return skipRow(fmt.Sprintf("orphaned import_job %q for missing songbook %q", it.ID, it.SongbookID))
		}
		created, updated, opt, err := parseTimestamps(it.CreatedAt, it.UpdatedAt, st.now, it.DispatchClaimExpiresAt, it.DispatchedAt, it.CompletedAt)
		if err != nil {
			return errRow(err)
		}
		catalogVer := nullifyMissing(st, "import_jobs", "catalog_version_id", it.ID, it.CatalogVersionID, st.catalogVersions)
		return okRow(it.ID, q, []any{it.ID, it.SongbookID, it.ManifestKey, it.Status, it.TotalSongs, it.ProcessedSongs, it.PruneMissing, it.Generation, catalogVer, it.DispatchReady, it.DispatchClaimToken, opt[0], it.DispatchAttempts, opt[1], it.Error, created, updated, opt[2]}, nil)
	})
}

type dynamoEventItem struct {
	ID                   string         `dynamodbav:"id"`
	OwnerID              string         `dynamodbav:"owner_id"`
	SongbookID           string         `dynamodbav:"songbook_id"`
	DetachedSongbookName string         `dynamodbav:"detached_songbook_name"`
	VenueName            string         `dynamodbav:"venue_name"`
	Name                 string         `dynamodbav:"name"`
	JoinCode             string         `dynamodbav:"join_code"`
	Status               string         `dynamodbav:"status"`
	Recurring            bool           `dynamodbav:"recurring"`
	TipURL               string         `dynamodbav:"tip_url"`
	Announcement         string         `dynamodbav:"announcement"`
	KioskPIN             string         `dynamodbav:"kiosk_pin"`
	RotationMode         string         `dynamodbav:"rotation_mode"`
	CurrentRound         int            `dynamodbav:"current_round"`
	AutoAccept           bool           `dynamodbav:"auto_accept"`
	SubmissionDelay      int            `dynamodbav:"submission_delay"`
	MaxActivePerSinger   *int           `dynamodbav:"max_active_per_singer"`
	AllowKeyChange       *bool          `dynamodbav:"allow_key_change"`
	AllowWriteIns        bool           `dynamodbav:"allow_write_ins"`
	SingerWeights        map[string]int `dynamodbav:"singer_weights"`
	QueueVersion         int64          `dynamodbav:"queue_version"`
	MetadataVersion      int64          `dynamodbav:"metadata_version"`
	Restarting           bool           `dynamodbav:"restart_in_progress"`
	Detaching            bool           `dynamodbav:"detach_in_progress"`
	PendingDetachTTL     int64          `dynamodbav:"pending_detach_ttl"`
	Deleting             bool           `dynamodbav:"deleting"`
	OpenKJKey            string         `dynamodbav:"openkj_key"`
	CreatedAt            string         `dynamodbav:"created_at"`
	UpdatedAt            string         `dynamodbav:"updated_at"`
	EndedAt              string         `dynamodbav:"ended_at"`
	TTL                  int64          `dynamodbav:"ttl"`
}

func migrateEvents(ctx context.Context, st *migrationState) (TableStats, error) {
	q := upsertSQL("events", []string{"id", "owner_id", "songbook_id", "detached_songbook_name", "venue_name", "name", "join_code", "status", "recurring", "tip_url", "announcement", "kiosk_pin", "rotation_mode", "current_round", "auto_accept", "submission_delay", "max_active_per_singer", "allow_key_change", "allow_write_ins", "singer_weights", "queue_version", "metadata_version", "restarting", "detaching", "pending_detach_expires_at", "deleting", "openkj_key", "created_at", "updated_at", "ended_at", "expires_at"}, "id")
	return scanAndMigrate(ctx, st, "events", "events", func(it dynamoEventItem) (preparedRow, bool, string, error) {
		if isExpiredTTL(it.TTL, st.now) {
			return skipRow("")
		}
		if it.ID == "" || !st.users[it.OwnerID] {
			return skipRow(fmt.Sprintf("orphaned event %q for missing owner %q", it.ID, it.OwnerID))
		}
		created, updated, opt, err := parseTimestamps(it.CreatedAt, it.UpdatedAt, st.now, it.EndedAt)
		if err != nil {
			return errRow(err)
		}
		songbookID := nullifyMissing(st, "events", "songbook_id", it.ID, it.SongbookID, st.songbooks)
		rotMode, autoAccept, subDelay, maxActive, allowKey, curRound := normalizeEventLegacyDefaults(it)
		weights := models.NormalizeSingerWeights(it.SingerWeights)
		if weights == nil {
			weights = map[string]int{}
		}
		weightsJSON, err := json.Marshal(weights)
		if err != nil {
			return errRow(err)
		}
		return okRow(it.ID, q, []any{it.ID, it.OwnerID, songbookID, it.DetachedSongbookName, it.VenueName, it.Name, strings.ToUpper(strings.TrimSpace(it.JoinCode)), it.Status, it.Recurring, it.TipURL, it.Announcement, it.KioskPIN, rotMode, curRound, autoAccept, subDelay, maxActive, allowKey, it.AllowWriteIns, weightsJSON, it.QueueVersion, it.MetadataVersion, it.Restarting, it.Detaching, unixToNullTime(it.PendingDetachTTL), it.Deleting, it.OpenKJKey, created, updated, opt[0], unixToNullTime(it.TTL)}, func() {
			st.events[it.ID] = true
		})
	})
}

func normalizeEventLegacyDefaults(it dynamoEventItem) (string, bool, int, int, bool, int) {
	rotMode := it.RotationMode
	if rotMode == "" {
		rotMode = models.RotationModeRoundRobin
	}
	autoAccept := it.AutoAccept
	if it.RotationMode == "" && !it.AutoAccept {
		autoAccept = true
	}
	subDelay := it.SubmissionDelay
	if subDelay == 0 && it.RotationMode == "" {
		subDelay = 60
	}
	maxActive := 1
	if it.MaxActivePerSinger != nil {
		maxActive = max(0, *it.MaxActivePerSinger)
	}
	allowKey := true
	if it.AllowKeyChange != nil {
		allowKey = *it.AllowKeyChange
	}
	curRound := it.CurrentRound
	if curRound <= 0 {
		curRound = 1
	}
	return rotMode, autoAccept, subDelay, maxActive, allowKey, curRound
}

type dynamoRequestItem struct {
	ID            string `dynamodbav:"id"`
	EventID       string `dynamodbav:"event_id"`
	SongbookID    string `dynamodbav:"songbook_id"`
	SongID        string `dynamodbav:"song_id"`
	IsWriteIn     bool   `dynamodbav:"is_write_in"`
	SongTitle     string `dynamodbav:"song_title"`
	SongArtist    string `dynamodbav:"song_artist"`
	SingerName    string `dynamodbav:"singer_name"`
	KeyChange     int    `dynamodbav:"key_change"`
	Notes         string `dynamodbav:"notes"`
	DeclineReason string `dynamodbav:"decline_reason"`
	Status        string `dynamodbav:"status"`
	Rank          int64  `dynamodbav:"rank"`
	Round         int    `dynamodbav:"round"`
	SingerTurn    int    `dynamodbav:"singer_turn"`
	CreatedAt     string `dynamodbav:"created_at"`
	UpdatedAt     string `dynamodbav:"updated_at"`
	CompletedAt   string `dynamodbav:"completed_at"`
	TTL           int64  `dynamodbav:"ttl"`
}

func migrateEventRequests(ctx context.Context, st *migrationState) (TableStats, error) {
	q := upsertSQL("event_requests", []string{"id", "event_id", "songbook_id", "song_id", "is_write_in", "song_title", "song_artist", "singer_name", "key_change", "notes", "decline_reason", "status", "rank", "round", "singer_turn", "created_at", "updated_at", "completed_at", "expires_at"}, "id")
	return scanAndMigrate(ctx, st, "event_requests", "event-requests", func(it dynamoRequestItem) (preparedRow, bool, string, error) {
		if strings.HasPrefix(it.EventID, "slots#") || strings.HasPrefix(it.ID, "slot#") || strings.HasPrefix(it.ID, "restart#") || strings.HasPrefix(it.EventID, "restart#") {
			return skipRow("")
		}
		if isExpiredTTL(it.TTL, st.now) {
			return skipRow("")
		}
		if it.ID == "" || !st.events[it.EventID] {
			return skipRow(fmt.Sprintf("orphaned event_request %q for missing event %q", it.ID, it.EventID))
		}
		created, updated, opt, err := parseTimestamps(it.CreatedAt, it.UpdatedAt, st.now, it.CompletedAt)
		if err != nil {
			return errRow(err)
		}
		return okRow(it.ID, q, []any{it.ID, it.EventID, it.SongbookID, it.SongID, it.IsWriteIn, it.SongTitle, it.SongArtist, it.SingerName, it.KeyChange, it.Notes, it.DeclineReason, it.Status, it.Rank, it.Round, it.SingerTurn, created, updated, opt[0], unixToNullTime(it.TTL)}, nil)
	})
}

type dynamoIdempotencyItem struct {
	IdempotencyKey string `dynamodbav:"idempotency_key"`
	OwnerID        string `dynamodbav:"owner_id"`
	Route          string `dynamodbav:"route"`
	EntityID       string `dynamodbav:"entity_id"`
	RequestHash    string `dynamodbav:"request_hash"`
	StatusCode     int    `dynamodbav:"status_code"`
	ResponseBody   string `dynamodbav:"response_body"`
	QueueVersion   int64  `dynamodbav:"queue_version"`
	TTL            int64  `dynamodbav:"ttl"`
}

func migrateIdempotencyRecords(ctx context.Context, st *migrationState) (TableStats, error) {
	q := upsertSQL("idempotency_records", []string{"idempotency_key", "owner_id", "route", "entity_id", "request_hash", "status_code", "response_body", "queue_version", "expires_at"}, "idempotency_key")
	return scanAndMigrate(ctx, st, "idempotency_records", "idempotency", func(it dynamoIdempotencyItem) (preparedRow, bool, string, error) {
		if it.IdempotencyKey == "" || isExpiredTTL(it.TTL, st.now) {
			return skipRow("")
		}
		exp := st.now.Add(24 * time.Hour)
		if it.TTL > 0 {
			exp = time.Unix(it.TTL, 0).UTC()
		}
		return okRow(it.IdempotencyKey, q, []any{it.IdempotencyKey, it.OwnerID, it.Route, it.EntityID, it.RequestHash, it.StatusCode, []byte(it.ResponseBody), it.QueueVersion, exp}, nil)
	})
}

type dynamoRateLimitItem struct {
	Key  string `dynamodbav:"key"`
	Hits int    `dynamodbav:"hits"`
	TTL  int64  `dynamodbav:"ttl"`
}

func migrateRateLimits(ctx context.Context, st *migrationState) (TableStats, error) {
	if st.skipEphemeral {
		return skipEphemeralTable(st, "rate_limits", "rate-limits"), nil
	}
	q := upsertSQL("rate_limits", []string{"bucket_key", "hits", "expires_at"}, "bucket_key")
	return scanAndMigrate(ctx, st, "rate_limits", "rate-limits", func(it dynamoRateLimitItem) (preparedRow, bool, string, error) {
		if it.Key == "" || it.TTL <= 0 || isExpiredTTL(it.TTL, st.now) {
			return skipRow("")
		}
		return okRow(it.Key, q, []any{it.Key, it.Hits, time.Unix(it.TTL, 0).UTC()}, nil)
	})
}
