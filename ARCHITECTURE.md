# Architecture: Static + Cloud API (Serverless BFF Pattern)

## Overview
MicUp.live is architected for **Easy Sharing**, **Zero-Friction Singer Onboarding**, and **Low Latency**. It uses a static-first, serverless architecture where a Preact SPA hosted via CloudFront/S3 communicates with an ARM64 Go-based API backed by Amazon Aurora DSQL (PostgreSQL 16 locally).

## Components

### 1. Static Frontend (The UI)
Hosted on AWS S3 & CloudFront (Vite + Preact).
- **Singer Experience:** Optimized for mobile with zero mandatory login. Uses **MiniSearch** and **TanStack Virtual** to provide instant fuzzy search over 50,000+ songs with sub-millisecond local filtering.
- **Universal Repertoire:** Starred favorites and pitch transposition memory (`-3♭` to `+3♯`) persist across venues using normalized `(artist, title)` keys in `localStorage`.
- **Off-main-thread Search:** MiniSearch indexing runs in `web/src/search/catalogWorker.js`, keeping large catalog parsing and search work off the mobile UI thread.
- **Live Rotation Tracking & Stage Alerts:** Displays live position in tonight's queue, requests Screen WakeLock to keep devices awake in dark venues, and fires background Web Notifications ("On Deck" and "Up Next on Stage") with audio fallbacks.
- **Performance History:** Logs "Songs Sung Tonight" with a 1-click `↻ Sing Again` re-request button.
- **Stage & Kiosk Displays:**
  - Fullscreen TV Stage view (`/stage/event/:id`) for bar monitors with visual Stage Shift motion cues, live ticker announcements, now-playing/up-next banners, and large QR codes.
  - Walk-up Kiosk Mode (`/kiosk/event/:id`) with configurable inactivity reset timeouts for iPad stations near the stage.
- **KJ Command Center:** Manages master songbooks, live shows, rotation ordering (drag & bump), request acceptance/pause/resume, and performance history.

### 2. Go Backend (The API)
High-performance Go 1.26 runtime targeting ARM64 AWS Lambda (HTTP API Gateway) or local standalone binary, organized as a modular monolith:
- **Layered Architecture Pattern**: Handlers act solely as transport adapters; business rules, state machines, and concurrency limits are strictly owned by dedicated application services:
  - `internal/queue`: Request intake, delay cooldown, queue limits, stage lock buffers, and multi-mode rotation planning (`round_robin`, `fair_priority`, `fcfs`).
  - `internal/event`: Show creation, lifecycle state transitions (active, paused, ended, restarted), kiosk PIN verification, and retention pruning. Event snapshot writes compare public queue version and an internal metadata version in the PostgreSQL / Aurora DSQL transaction (`FOR UPDATE` fencing with `SQLSTATE 40001` OCC retries); metadata changes without a queue bump remain fenced. See [event settings recovery](RUNBOOK.md#event-settings-snapshot-versions).
  - `internal/catalog`: Manifest compilation, S3 catalog overrides, publication orchestration, and job queues.
  - `internal/songbook`: Songbook metadata, track CRUD, fingerprint deduplication, soft-pruning sync workflows, scoped collaborator invitations/grants, and owner-controlled deletion with foreign-show detach confirmation.
  - `internal/account`: Data export and cascading account purges with partial-failure tracking.
  - `internal/auth`: Password-reset token issuance, delivery, validation, and atomic replacement orchestration. Login, registration, and email verification still live in `AuthHandler`.
- **Protocol Convergence**: Browser routes, developer REST endpoints (`/api/v1/events/...`), and the OpenKJ integration adapter (`internal/handlers/openkj.go`) all converge onto the same application services, preventing duplicate logic or divergent state transitions.
- **Invariants & Contracts**: Behavioral guarantees, acceptance scenarios, and concurrency boundaries are defined in [`docs/CONTRACTS.md`](./docs/CONTRACTS.md) and [`docs/openapi.yaml`](./docs/openapi.yaml).
- **Asynchronous Workers:** Import Worker consumes durable SQS jobs and validates S3 manifests; Catalog Worker compiles manifests into immutable S3 assets and atomically publishes catalog versions. `cmd/catalog-gc` performs operator-run cleanup audits.

### 3. Cloud Storage (S3 + CloudFront + Aurora DSQL)
S3 and CloudFront are the source of truth for mostly-static song catalogs; Amazon Aurora DSQL (PostgreSQL 16 locally) stores operational state via `pgx/v5`:
- **Immutable Catalogs:** Imports and periodic edits produce versioned manifests, letter shards, and compressed search assets in S3. Songbook pointers switch atomically only after a complete build.
- **Pending Changes:** Recent KJ additions and edits may be held temporarily in `catalog_changes` and layered over the active S3 catalog until periodic compaction publishes the next version. Catalog builds currently run event-driven through SQS; a scheduler for batching edits is planned.
- **Decoupled Events:** Shows are decoupled from songbooks, allowing KJs to run multiple weekly gigs sharing one master library.
- **Atomic Queue Ordering:** Queue items are stored individually with rank timestamps and transactional updates, avoiding write-collision bottlenecks.

## Client Subsystems

### 1. Universal Favorites & Pitch Store (`favoritesStore.js`)
- Fingerprints tracks as `normalized(artist) + ":::" + normalized(title)` (lowercase, punctuation stripped).
- Persists song favorites, favorite artists, and preferred pitch keys (`-3` to `+3`) in `localStorage`.
- Request modals automatically pre-populate the singer's preferred key for that song across any MicUp show.

### 2. Stage Alerts & WakeLock Subsystem (`notifications.js`)
- Requests browser Web Notification permissions on user interaction.
- Fires OS-level alerts when the singer is "On Deck" (1 song away) or "Up Next on Stage".
- Synthesizes two-tone Web Audio chimes as instant fallbacks when audio/vibration is enabled.
- Uses the Screen WakeLock API while viewing queue or when active in rotation to prevent phone sleep in venue environments.

### 3. Singer Performance History (`singerHistory`)
- Records completed songs sung during the active event.
- Provides 1-click `↻ Sing Again` re-requesting and quick star toggling directly from the queue drawer.

### 4. Multi-Mode Queue Rotation Engines
MicUp provides three host-selectable queue rotation models with transactional safety:
- **Fair Priority (`fair_priority`)**: Dynamic least-recently-sung smart weave with private host-controlled singer weights. Sorts equal-cycle requests by `(cycle, effectiveSeq, CreatedAt)`, where `effectiveSeq` uses each singer's monotonic completion sequence (`completed_at`, falling back to `updated_at`), credits newcomers with up to 5 completed songs so late arrivals weave in without starving waiting regulars, and applies optional private host-only `singer_weights` (`[-10, +10]`). Features a strict 3-position Stage Lock Buffer (`on_stage`, `on_deck`, `in_the_hole`) so newcomers and weights never displace singers called or about to be called to the stage.
- **Classic Round Robin (`round_robin`, default)**: Organizes requests into tiers. Multiple submissions from the same singer automatically stack across successive rounds (`Round 1`, `Round 2`, `Round 3`), controlled by the event's `current_round`.
- **First-Come, First-Served (`fcfs`) & Manual**: Pure chronological FIFO intake, or host-managed manual staging where requests stay pending until host review.
- **Transactional State Management**: Mode transitions (`TransitionQueueRotation`) and staged request acceptance (`UpdateRequestAndReorder`) execute inside atomic PostgreSQL / Aurora DSQL transactions (`pgx/v5` with `FOR UPDATE` fencing and bounded `SQLSTATE 40001` OCC retries), guaranteeing zero partial mutations, collision-free ranks, and stage lock preservation across host operations.

## Data Models

### Songbook (Master Catalog)
- `id`: Master catalog identifier.
- `owner_id`: KJ host user identifier.
- `name`: Catalog title (e.g., "Main Library", "Country Night").
- `is_public`: Whether the catalog is publicly discoverable.
- `public_id`: Short alphanumeric identifier for public catalog access.

### Song (Track Metadata)
- `id`: Unique track identifier.
- `songbook_id`: Reference to parent catalog.
- `artist`: Normalized artist name.
- `title`: Clean track title.
- `location`: Original file path on the host's drive (stripped from public responses).
- `is_custom`: Flag for songs manually added or edited by the KJ.
- `search_text`: Computed search field included in the compressed S3 search asset.

### Event (Live Show)
Decoupled from songbooks to allow one master catalog across multiple distinct gigs and venues.
- `id`: Event UUID.
- `owner_id`: KJ host identifier.
- `songbook_id`: Reference to master songbook catalog.
- `venue_name`: Live venue (e.g., "O'Malley's Irish Pub").
- `name`: Show title (e.g., "Tuesday Night Live").
- `join_code`: Short, memorable uppercase code for bar singers (e.g., `OMALLEY`).
- `status`: Show lifecycle (`active` 🟢, `paused` ⏸️, `ended` ⏹️).
- `rotation_mode`: Rotation engine (`round_robin` [default], `fair_priority`, `fcfs`, `manual`).
- `current_round`: Current round number for round-based rotation tracking.
- `queue_version`: Monotonic version used for efficient conditional polling.
- `kiosk_pin`, `auto_accept`, `submission_delay`, `allow_key_change`, `singer_weights`: Host display, intake, singer-control, and private host-only `fair_priority` weight settings (`singer_weights` is never exposed on public endpoints).

### SongRequest (Live Queue Item)
Individual request record scoped to an event.
- `id`: Request UUID.
- `event_id`: Event identifier.
- `songbook_id`: Associated songbook identifier.
- `singer_name`: Stage or real name entered by singer.
- `song_title` / `song_artist`: Song requested.
- `key_change`: Pitch adjust integer (semitones: `-3` to `+3`).
- `notes`: Singer note to KJ (e.g., "Duet with Alex", "Birthday!").
- `rank`: Monotonic timestamp or rank for manual drag/bump reordering.
- `round`: Assigned round tier for Round Robin slotting.
- `singer_turn`: Singer turn index for the night (`completed + active_ahead + 1`) for display and analytics.
- `completed_at`: Timestamp recording when a request transitioned to `completed` (used for `fair_priority` completion chronology).
- `status`: `pending`, `accepted`, `up_next`, `completed`, `rejected`, `cancelled`.
- `cancel_token`: HMAC-SHA256 token allowing the submitting singer to cancel without an account.

### CatalogVersion
Immutable normalized catalog metadata stored in `catalog_versions`; its content-addressed
ID points to public manifest, letter-shard, and compressed search assets in S3.

### ImportJob
Durable S3-manifest processing record stored in `import_jobs` with status, progress,
retry/error information, and completion timestamps. It is independent of the
browser session.

### 4. PostgreSQL / Aurora DSQL Table Architecture
Operational state uses a 15-table Aurora DSQL-compatible PostgreSQL schema defined in [`internal/store/postgres/migrations/0001_initial.sql`](./internal/store/postgres/migrations/0001_initial.sql) and applied idempotently by `cmd/pg-migrate` (which rewrites `CREATE [UNIQUE] INDEX` statements to `ASYNC` when `POSTGRES_DSQL_MODE=true`). The backend connects via `pgx/v5`, using `FOR UPDATE` row fencing and bounded `SQLSTATE 40001` optimistic concurrency control (OCC) retries. In production, Lambdas authenticate to Aurora DSQL over TLS using short-lived IAM auth tokens (`aws-sdk-go-v2/feature/dsql/auth`) with zero static database passwords and no VPC required:
1. `users`: Internal host profiles, email (unique partial index `idx_users_email` when non-empty), stage names, password hashes, `auth_epoch`, deletion workflow state, and account favorites (`favorite_songs`, `favorite_artists` JSONB).
2. `auth_sessions`: Refresh session tokens (SHA-256 `token_hash`), `user_id`, `auth_epoch`, and `expires_at` timestamp.
3. `passkey_credentials`: FIDO2 / WebAuthn public key credentials for biometrics.
4. `webauthn_sessions`: Ephemeral WebAuthn challenge tokens with `expires_at`.
5. `developer_keys`: Scoped Personal Access Tokens (SHA-256 `id` hash, `scopes` JSONB, `scope_version`).
6. `catalog_versions`: Content-addressed catalog metadata, build/deletion status, `lease_token`, `lease_expires_at`, `asset_prefix`, and `song_count`.
7. `songbooks`: Master catalogs with unique `public_id` and nullable `slug` indexes, `catalog_version_id`, and generation counters.
8. `songbook_grants`: Per-songbook collaborator permissions (`songbook_id`, `user_id`, `can_edit_catalog`, `can_host_shows`).
9. `songbook_invitations`: Single-use SHA-256 token-hashed email invitations (`token_hash` unique index), delivery status, and partial unique pending slot (`(songbook_id, recipient_email) WHERE status = 'pending'`).
10. `catalog_changes`: Temporary pending songbook additions, edits, and deletions awaiting S3 compaction.
11. `import_jobs`: Durable background import status, `manifest_key`, progress, dispatch claim/lease fields, and errors.
12. `events`: Live shows with unique `join_code` index, kiosk PINs, `queue_version`, `metadata_version`, `singer_weights`, and lifecycle state.
13. `event_requests`: Canonical event-scoped live queue items indexed by `(event_id, rank, created_at)`, notes, pitch changes, singer slots, and status.
14. `idempotency_records`: 24-hour (`expires_at`) replay results for event-scoped status and reorder retries, bound to owner, route, entity, and normalized request hash.
15. `rate_limits`: Distributed fixed-window request counters keyed by `rl#<scope>#<key>#<bucket>` with `expires_at`.

## Security & Privacy
- **Authentication:** Dual-stack supporting FIDO2 / WebAuthn Biometric Passkeys (Touch ID, Face ID, Windows Hello) and standard email/password with bcrypt hashing and email verification. Password resets conditionally replace the password and advance an authentication epoch in the existing user record. Earlier reset links and bearer access/refresh sessions become invalid; existing user reads enforce the epoch without another authentication read. Revoked refresh rows carry an `expires_at` timestamp in `auth_sessions`. See [authentication recovery](RUNBOOK.md#password-reset-authentication-epochs).
- **Distributed Multi-Instance Rate Limiting:** Operational protection across concurrent Lambda microVMs:
  - Backed by PostgreSQL / Aurora DSQL atomic upserts (`INSERT INTO rate_limits ... ON CONFLICT (bucket_key) DO UPDATE SET hits = rate_limits.hits + 1 RETURNING hits`) inside `withTx` with `SQLSTATE 40001` retries, keyed by `rl#<scope>#<key>#<bucket>` with an `expires_at` timestamp.
  - Robust client IP extraction validates `r.RemoteAddr` (set from API Gateway's `requestContext.http.sourceIp` in production) with `net.ParseIP` and deliberately ignores `X-Forwarded-For` / `X-Real-IP` forwarding headers to prevent IP spoofing or rate-limit key pollution.
  - Fail-safe resilience: If PostgreSQL / Aurora DSQL encounters transient latency or connection failures, the rate limiter logs a warning and gracefully falls back to local in-memory token buckets, ensuring uninterrupted availability.
  - Standard compliance: Emits standard `RateLimit-Limit`, `RateLimit-Remaining`, and `Retry-After` HTTP headers on protected endpoints.
- **Queue Authorization:** Requests are issued a cryptographic HMAC-SHA256 cancellation token. Deleting a request requires either a valid cancellation token or authenticated event ownership.
- **CORS & Edge Security:** Restricts API access to approved domains with AWS Managed `SecurityHeadersPolicy` (HSTS, clickjacking protection, nosniff).
- **OpenKJ Isolation:** Requires private Event IDs / keys for all destructive operations; public join codes cannot wipe or modify catalogs.
- **BFF Data Isolation:** The public BFF layer strips local file paths (`location`), source paths, and host metadata before sending responses.
- **Zero Singer Exposure:** Singers do not need passwords or account registration to use full repertoire, favorites, pitch transposition, or notifications.
- **Show Lifecycle Protection:** Request submissions check show status (`409 Conflict` when paused, `410 Gone` when ended).

## Why this Architecture?
1. **Speed & Resilience:** Static catalog browsing operates with zero database hits; MiniSearch indexes locally on the device in <100ms. Large imports avoid one database write per song.
2. **Decoupled Operation:** A KJ maintains their curated master library once, then spins up isolated shows with simple join codes without duplicating song collections.
3. **Singer Delight:** Repertoire, pitch memory, and stage alerts work seamlessly on any mobile phone without requiring an app download or account creation.
4. **Host Control:** KJs retain full live control over rotation with one-click Pause/Resume requests, Up Next marking, bump reordering, and TV stage broadcasts.
