-- Initial relational schema for MicUp.live (PG1).
-- Designed for 1:1 compatibility between local PostgreSQL 16 and Amazon Aurora DSQL:
-- - App-generated TEXT primary keys (UUIDs / SHA-256 hashes), no SERIAL/IDENTITY.
-- - No triggers, no GIN indexes on JSONB, single DDL statement per execution.

CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    avatar_url TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL DEFAULT '',
    auth_epoch TEXT NOT NULL DEFAULT '',
    is_host BOOLEAN NOT NULL DEFAULT FALSE,
    email_verified BOOLEAN NOT NULL DEFAULT FALSE,
    deleting BOOLEAN NOT NULL DEFAULT FALSE,
    deletion_epoch TEXT NOT NULL DEFAULT '',
    deletion_auth_epoch TEXT NOT NULL DEFAULT '',
    deletion_started_at TIMESTAMPTZ,
    deletion_step TEXT NOT NULL DEFAULT '',
    deletion_error TEXT NOT NULL DEFAULT '',
    pending_catalog_versions JSONB NOT NULL DEFAULT '[]'::jsonb,
    favorite_songs JSONB NOT NULL DEFAULT '[]'::jsonb,
    favorite_artists JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email ON users (email) WHERE email <> '';

CREATE TABLE IF NOT EXISTS auth_sessions (
    token_hash BYTEA PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    auth_epoch TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_auth_sessions_user_id ON auth_sessions (user_id);

CREATE INDEX IF NOT EXISTS idx_auth_sessions_expires_at ON auth_sessions (expires_at);

CREATE TABLE IF NOT EXISTS passkey_credentials (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    public_key BYTEA NOT NULL,
    attestation_type TEXT NOT NULL DEFAULT '',
    aaguid BYTEA,
    sign_count BIGINT NOT NULL DEFAULT 0,
    transports JSONB NOT NULL DEFAULT '[]'::jsonb,
    backup_eligible BOOLEAN NOT NULL DEFAULT FALSE,
    backup_state BOOLEAN NOT NULL DEFAULT FALSE,
    user_present BOOLEAN NOT NULL DEFAULT FALSE,
    user_verified BOOLEAN NOT NULL DEFAULT FALSE,
    friendly_name TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_passkey_credentials_user_id ON passkey_credentials (user_id);

CREATE TABLE IF NOT EXISTS webauthn_sessions (
    session_id TEXT PRIMARY KEY,
    user_id TEXT REFERENCES users(id) ON DELETE CASCADE,
    challenge TEXT NOT NULL,
    session_data BYTEA NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_webauthn_sessions_expires_at ON webauthn_sessions (expires_at);

CREATE TABLE IF NOT EXISTS developer_keys (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    key_prefix TEXT NOT NULL,
    scopes JSONB NOT NULL DEFAULT '[]'::jsonb,
    scope_version INTEGER NOT NULL DEFAULT 1,
    revoked BOOLEAN NOT NULL DEFAULT FALSE,
    revoked_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_developer_keys_owner_id ON developer_keys (owner_id);

CREATE TABLE IF NOT EXISTS catalog_versions (
    id TEXT PRIMARY KEY,
    schema_version INTEGER NOT NULL DEFAULT 1,
    status TEXT NOT NULL,
    song_count INTEGER NOT NULL DEFAULT 0,
    asset_prefix TEXT NOT NULL DEFAULT '',
    failure_reason TEXT NOT NULL DEFAULT '',
    lease_token TEXT NOT NULL DEFAULT '',
    lease_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    ready_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS songbooks (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    public_id TEXT NOT NULL,
    slug TEXT,
    venue_id TEXT NOT NULL DEFAULT '',
    catalog_version_id TEXT REFERENCES catalog_versions(id) ON DELETE RESTRICT,
    name TEXT NOT NULL,
    is_public BOOLEAN NOT NULL DEFAULT FALSE,
    song_count INTEGER NOT NULL DEFAULT 0,
    catalog_generation BIGINT NOT NULL DEFAULT 0,
    last_import_generation BIGINT NOT NULL DEFAULT 0,
    deleting BOOLEAN NOT NULL DEFAULT FALSE,
    deletion_confirmed BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_songbooks_public_id ON songbooks (public_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_songbooks_slug ON songbooks (slug) WHERE slug IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_songbooks_owner_id ON songbooks (owner_id);

CREATE INDEX IF NOT EXISTS idx_songbooks_catalog_version_id ON songbooks (catalog_version_id);

CREATE TABLE IF NOT EXISTS songbook_grants (
    songbook_id TEXT NOT NULL REFERENCES songbooks(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    can_edit_catalog BOOLEAN NOT NULL DEFAULT FALSE,
    can_host_shows BOOLEAN NOT NULL DEFAULT FALSE,
    granted_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (songbook_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_songbook_grants_user_id ON songbook_grants (user_id);

CREATE TABLE IF NOT EXISTS songbook_invitations (
    id TEXT PRIMARY KEY,
    songbook_id TEXT NOT NULL REFERENCES songbooks(id) ON DELETE CASCADE,
    owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    recipient_email TEXT NOT NULL,
    can_edit_catalog BOOLEAN NOT NULL DEFAULT FALSE,
    can_host_shows BOOLEAN NOT NULL DEFAULT FALSE,
    token_hash TEXT NOT NULL,
    status TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    last_sent_at TIMESTAMPTZ,
    send_error TEXT NOT NULL DEFAULT '',
    accepted_by_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    accepted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_songbook_invitations_token_hash ON songbook_invitations (token_hash);

CREATE UNIQUE INDEX IF NOT EXISTS idx_songbook_invitations_pending ON songbook_invitations (songbook_id, recipient_email) WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_songbook_invitations_songbook_id ON songbook_invitations (songbook_id);

CREATE TABLE IF NOT EXISTS catalog_changes (
    songbook_id TEXT NOT NULL REFERENCES songbooks(id) ON DELETE CASCADE,
    song_id TEXT NOT NULL,
    song JSONB,
    deleted BOOLEAN NOT NULL DEFAULT FALSE,
    clear BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (songbook_id, song_id)
);

CREATE TABLE IF NOT EXISTS import_jobs (
    id TEXT PRIMARY KEY,
    songbook_id TEXT NOT NULL REFERENCES songbooks(id) ON DELETE CASCADE,
    manifest_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    total_songs INTEGER NOT NULL DEFAULT 0,
    processed_songs INTEGER NOT NULL DEFAULT 0,
    prune_missing BOOLEAN NOT NULL DEFAULT FALSE,
    generation BIGINT NOT NULL DEFAULT 0,
    catalog_version_id TEXT REFERENCES catalog_versions(id) ON DELETE SET NULL,
    dispatch_ready BOOLEAN NOT NULL DEFAULT FALSE,
    dispatch_claim_token TEXT NOT NULL DEFAULT '',
    dispatch_claim_expires_at TIMESTAMPTZ,
    dispatch_attempts INTEGER NOT NULL DEFAULT 0,
    dispatched_at TIMESTAMPTZ,
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_import_jobs_songbook_created ON import_jobs (songbook_id, created_at DESC);

CREATE TABLE IF NOT EXISTS events (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    songbook_id TEXT REFERENCES songbooks(id) ON DELETE SET NULL,
    detached_songbook_name TEXT NOT NULL DEFAULT '',
    venue_name TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL,
    join_code TEXT NOT NULL,
    status TEXT NOT NULL,
    recurring BOOLEAN NOT NULL DEFAULT FALSE,
    tip_url TEXT NOT NULL DEFAULT '',
    announcement TEXT NOT NULL DEFAULT '',
    kiosk_pin TEXT NOT NULL DEFAULT '',
    rotation_mode TEXT NOT NULL DEFAULT 'round_robin',
    current_round INTEGER NOT NULL DEFAULT 1,
    auto_accept BOOLEAN NOT NULL DEFAULT TRUE,
    submission_delay INTEGER NOT NULL DEFAULT 60,
    max_active_per_singer INTEGER NOT NULL DEFAULT 1,
    allow_key_change BOOLEAN NOT NULL DEFAULT TRUE,
    allow_write_ins BOOLEAN NOT NULL DEFAULT FALSE,
    singer_weights JSONB NOT NULL DEFAULT '{}'::jsonb,
    queue_version BIGINT NOT NULL DEFAULT 0,
    metadata_version BIGINT NOT NULL DEFAULT 0,
    restarting BOOLEAN NOT NULL DEFAULT FALSE,
    detaching BOOLEAN NOT NULL DEFAULT FALSE,
    pending_detach_expires_at TIMESTAMPTZ,
    deleting BOOLEAN NOT NULL DEFAULT FALSE,
    openkj_key TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_events_join_code ON events (join_code);

CREATE INDEX IF NOT EXISTS idx_events_owner_id ON events (owner_id);

CREATE INDEX IF NOT EXISTS idx_events_songbook_id ON events (songbook_id);

CREATE TABLE IF NOT EXISTS event_requests (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    songbook_id TEXT NOT NULL DEFAULT '',
    song_id TEXT NOT NULL DEFAULT '',
    is_write_in BOOLEAN NOT NULL DEFAULT FALSE,
    song_title TEXT NOT NULL,
    song_artist TEXT NOT NULL,
    singer_name TEXT NOT NULL,
    key_change INTEGER NOT NULL DEFAULT 0,
    notes TEXT NOT NULL DEFAULT '',
    decline_reason TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    rank BIGINT NOT NULL DEFAULT 0,
    round INTEGER NOT NULL DEFAULT 1,
    singer_turn INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_event_requests_event_rank ON event_requests (event_id, rank, created_at);

CREATE TABLE IF NOT EXISTS idempotency_records (
    idempotency_key TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    route TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    status_code INTEGER NOT NULL,
    response_body BYTEA NOT NULL,
    queue_version BIGINT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_idempotency_records_expires_at ON idempotency_records (expires_at);

CREATE TABLE IF NOT EXISTS rate_limits (
    bucket_key TEXT PRIMARY KEY,
    hits INTEGER NOT NULL DEFAULT 1,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_rate_limits_expires_at ON rate_limits (expires_at);
