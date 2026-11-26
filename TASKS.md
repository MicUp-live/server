# MicUp.live — Active Tasks & Roadmap

> Historical maintenance logs (M1–M14, Stages A–D, W1–W3, and the 2026-11 pruning pass P0–P6) are complete, deployed to production, and preserved in git history. This file tracks only active priorities, open roadmap items, and explicit non-goals.

## Current State (2026-11-24)

- **Production baseline (`main` / `v1.23.0`):** S3-first immutable catalog architecture on 16 DynamoDB tables and 5 active GSIs. The legacy `requests`, `songs`, and `fingerprint-claims` tables, 9 unused GSIs, and 10 deprecated compatibility routes have been retired. Recent releases added kiosk exit PIN controls, private Fair Priority singer weights, account-synced favorites (`GET/PUT /api/v1/auth/favorites`), manual songbook overlays with background compaction, fixed wait estimates, and Discord support links.
- **OpenKJ support:** Upgraded to `pkircher29/OpenKJ` (`src/okjsongbookapi.cpp`) wire protocol and migrated onto `CatalogChanges` + S3 compaction.
- **Verification & fitness:** All Go packages + 4 parallel DynamoDB Local shards (`mise run test`), frontend tests/build, OpenAPI sync, design token check, OpenTofu validation, and enforced maintainability hotspot ceilings (`scripts/hotspot-baseline.json`) are passing. See [vignette coverage](scripts/vignettes/COVERAGE.md) for multi-actor browser checks.
- **Local Python tooling (2026-11-25):** Mise creates a Python 3.14 `.venv`; `mise run python:install` installs pinned PyYAML. `mise run check:fitness` and `mise run openapi:check` passed locally. These checks do not establish production deployment state.

---

## Active Priorities

### 1. Hosting, Queue & Songbook Capabilities

- [ ] **HP1 — Implement configurable show policies with accepted defaults**
  - Accepted product defaults documented in [CONCEPTS.md](docs/CONCEPTS.md#accepted-configurable-hosting-policies) and [CONTRACTS.md](docs/CONTRACTS.md) (C11).
  - Scope: retain empty participants by default, preserve place on authorized pre-call song replacement, explicit absence deferral, distinct participant identity and configurable duet accounting, and explicit host stage override (`paused` vs `ended` intake separation is already live).
  - Acceptance: map every C11 scenario to named tests covering defaults and alternatives, supported rotations/adapters, settings races (M11), transaction rollback, tenant denial, OpenAPI sync, real DynamoDB Local, and browser behavior.

- [ ] **Host Queue Version Resolution & Playback Stamping**
  - In `LiveQueue.jsx`, display singer-selected versions (`SC8101-02 • Singer Selected`) or an inline version dropdown for host's choice.
  - Stamp `played_disc` and `played_brand` onto `SongRequest` upon completion for deterministic performance history.

- [ ] **Track Quality Intelligence & "Bad Track Radar"**
  - Optional post-performance track quality ratings attributed to the stamped manufacturer/disc, surfacing warnings to KJs in the queue.

- [ ] **Songbook Catalog Comparison**
  - Let hosts compare their songbook with one or more selected catalogs and see which songs are missing, with the source catalogs shown for each result.
  - Distinguish missing songs from missing versions of songs already in the songbook.
  - Possible extension: a **Browse Catalogs** view for exploring and searching catalogs without starting a comparison.

- [ ] **Reference catalog compilation runtime**
  - Reference compilation currently runs from a scheduled GitHub Actions workflow. Some sources are not reachable from hosted runners, so those sources are compiled locally or left out until a runtime is chosen.
  - Evaluate moving compilation and publication to an AWS scheduled job (Lambda or Fargate): `pdftotext` packaging, runtime and memory limits, IAM, S3/CloudFront publication, failure recovery, concurrency, and cost against the $5/month budget. Verify source access from the proposed runtime before adding any source that needs it.
  - Preserve existing source freshness and shrinkage guardrails, and test that a failing source cannot block publication of the other catalogs.

### 2. Staging, Vignette Coverage & Edge Hardening

- [ ] **Vignette Boundary and Recovery Follow-up (VB1)**
  - Browser vignettes should cover cross-boundary journeys: replacement-import confirmation and resulting catalog, and recovery after interruption, plus isolated account export/deletion and show restart. Existing reconnect coverage is one recovery case, not exhaustive. Invitation acceptance and changed permissions are a [manual vignette](scripts/vignettes/COVERAGE.md#manual-vignette-songbook-collaboration), not an automation or mail-capture task.
  - Keep concurrency, transaction conflicts, migrations, and publication fencing primarily in deterministic backend/DynamoDB tests; browser journeys may illustrate outcomes but do not prove these guarantees. Add vignettes for configurable show policies and catalog comparison only when those features exist.
  - Use temporary, dedicated accounts and resources; never reset local data or use production. Record local report paths and unresolved gaps in [COVERAGE.md](scripts/vignettes/COVERAGE.md).
  - Working invariant: a replacement import changes the catalog only after confirmed worker publication; callers are the importer UI and managed/public catalog readers. A canceled warning must not dispatch; failure must remain visible and not appear as success. Browser assertions cover cancel/confirm and resulting catalog; concurrency and retry fencing remain backend evidence.
  - **Local progress, 2026-11-25:** `songbook-import-management` now asserts cancel without dispatch and confirmed replacement (5 beats, one expected informational no-prior-job finding); `host-reorder-and-reconnection` asserts end/restart without clearing (4 beats, zero findings); `account-privacy` creates a disposable host, exports its book and show, deletes the account and checks login/show denial (3 beats, zero findings). Report paths and evidence boundaries are in [COVERAGE.md](scripts/vignettes/COVERAGE.md). The collaboration vignette is documented for manual execution and has not been run. Changed files: `scripts/vignettes/run.mjs`, `lib/runner.mjs`, `test/runner.test.mjs`, `vignettes/account-privacy.mjs`, `vignettes/host-reorder-and-reconnection.mjs`, `vignettes/songbook-import-management.mjs`, `README.md`, and `COVERAGE.md`. Next: cover interruption recovery beyond a clean restart.
  - **Checks:** `npm --prefix scripts/vignettes test` passed 12 checks, including nonlocal-request blocking, deletion recovery-token cleanup and report redaction; the three updated scenarios passed against local services. `git diff --check` passed. No production observations or local-data reset.

- [ ] **Standing `dev.micup.live` AWS Environment (Local-Only Deploy)**
  - Parameterize `infra/` so `environment = "dev"` deploys an isolated serverless stack at `dev.micup.live` (separate state key, prod singletons gated, `production` plan = no-op) with a local `mise` deploy task.

- [x] **Expand Playwright Vignette Coverage** ([COVERAGE.md](scripts/vignettes/COVERAGE.md))
  - `fair-priority-rotation` passed locally on 2026-11-24 with five beats and zero findings. It checks least-recently-sung placement, submission order for equally ranked newcomers, the three protected positions, positive host-only weights, auto-accept, and synchronized host, stage, and singer views. `mise exec -- go test -count=1 ./internal/queue -run FairPriority` also passed.
  - **Completed locally, 2026-11-25:** Re-ran `queue-lifecycle` (10 beats), `favorites-and-edge-cases` (8), and `fair-priority-rotation` (5), each with zero findings. Added and passed `kiosk-patron-session` (3 beats, zero findings), `round-robin-and-admission` (3 beats, two informational expected rejections; zero high/medium findings), and `host-reorder-and-reconnection` (2 beats, zero findings). Report paths and per-run finding counts are recorded in [COVERAGE.md](scripts/vignettes/COVERAGE.md). All targets were `http://localhost:3000`; verification used local emulators and authorized `mise run local:workers`, with no production resources or local data reset.
  - **Songbook management follow-up, completed locally, 2026-11-25:** Added `songbook-catalog-management` (5 beats, zero findings) for owner book details/visibility, track add/edit/delete, CSV export, and pending invitation create/cancel; added `songbook-import-management` (3 beats, zero high/medium findings) for CSV audit and additive worker-backed import preserving existing tracks. Reports are recorded in [COVERAGE.md](scripts/vignettes/COVERAGE.md). LocalStack S3 browser CORS is now provisioned idempotently for loopback origins to exercise presigned imports; only local settings were touched. At that point invitation acceptance/grant enforcement and replacement/pruning were uncovered; see VB1 for subsequent progress.
  - `npm --prefix scripts/vignettes test` passes eight helper/runner checks. The favorites fixture now searches across the catalog for an unsaved track and never toggles a previously saved track; cleanup navigates live views without losing host auth, and expected admission errors are informational rather than run failures. C02 concurrency/atomicity and broader C09 polling races remain backend or future browser evidence.

- [ ] **Bot Submission Heuristics**
  - Add subtle client-side challenge/honeypot fields to public song request submissions to curb automated spam without degrading singer UX.

- [ ] **P2 — Cross-Repository Contract Fixtures (`micup-hoster`)**
  - Server-side canonical JSON fixtures in [testdata/contracts/](testdata/contracts/README.md) and router/DynamoDB Local contract tests (`internal/app/contract_fixtures_test.go`) are complete and passing.
  - Remaining external work: consume `testdata/contracts/*.json` in Rust `micup-client` (`micup-hoster` repository) and verify its cargo test suite.

- [ ] **External: `micup-store` (`micup-hoster`) & YouTube Player Handoff (`micup-player`)**
  - External repository work tracked for cross-client coordination; not a blocker on server maintenance.

- [x] **Player production API origin**
  - On 2026-11-26 UTC, production queue GET and PATCH preflights returned `204` for `https://player.micup.live` with the expected CORS headers. The shared GitHub Pages origin returned `403`. The API Lambda deployment changed only `CORS_ALLOW_ORIGIN`.

### 3. Persistence Migration (PostgreSQL / Aurora DSQL)

- [ ] **PG1 — Migrate operational persistence from DynamoDB to Aurora DSQL-compatible PostgreSQL**
  - **Intent & scope:** Keep immutable S3/CloudFront song catalogs and client-side MiniSearch unchanged; replace only the operational state layer behind [internal/store/store.go](internal/store/store.go). Develop and test locally against **PostgreSQL 16** (Docker) with a schema and transaction model compatible with **Amazon Aurora DSQL** (`us-east-2`, \$0 idle compute, IAM auth, no VPC).
  - **Cutover priority:** Prioritize 1:1 behavioral parity first (retain `rate_limits` and `JSONB` favorites on `users` for a smooth cutover). Future KJ and Karaoke Producer metrics can query `event_requests` directly in SQL and normalize favorites later if needed.
  - **Planned schema (15 tables, DSQL-compatible — app-generated IDs, no triggers, no GIN indexes on `JSONB`, single-statement DDL migrations):**
    - *Identity & Auth (5):* `users` (`UNIQUE(email)` replacing `identity_claims`), `auth_sessions`, `passkey_credentials`, `webauthn_sessions`, `developer_keys`.
    - *Songbooks & Catalogs (6):* `catalog_versions`, `songbooks` (`catalog_version_id` FK `ON DELETE RESTRICT`), `songbook_grants`, `songbook_invitations` (partial unique `(songbook_id, recipient_email) WHERE status = 'pending'` replacing invitation slot items), `catalog_changes`, `import_jobs`.
    - *Live Shows & Queues (3):* `events`, `event_requests` (global PK `id`, eliminating `request-id-index` and restart slot items), `idempotency_records`.
    - *Rate Limiting (1):* `rate_limits`.
  - **Phased plan:**
    1. **Local Postgres + schema:** Add a local `postgres:16` service and `.mise.toml` tasks, plus DSQL-safe SQL migrations (`internal/store/postgres/migrations`).
    2. **Store implementation:** Implement `internal/store/postgres` (`pgx/v5`) covering all interfaces in [internal/store/store.go](internal/store/store.go), with `SQLSTATE 40001` retry handling for Aurora DSQL OCC and `expires_at > now()` filtering plus cleanup to replace DynamoDB TTL.
    3. **Test parity:** Port behavioral, concurrency, rollback, and fencing tests to run against local Postgres, add `40001` retry tests, and verify IAM auth + OCC conflicts against a free-tier `us-east-2` Aurora DSQL dev cluster before cloud cutover.
    4. **Wiring & cutover:** Add a `STORE_BACKEND=postgres|dynamo` switch in `cmd/` and `internal/app`, verify locally end-to-end, and retire `internal/store/dynamo` once cutover is confirmed.
  - **Local progress, 2026-11-26:** Implemented local PostgreSQL 16 service (`docker-compose.yml`, `.mise.toml` `pg:up`/`pg:down`/`test:postgres`), DSQL-safe 15-table schema (`internal/store/postgres/migrations/0001_initial.sql`), full `internal/store/postgres` (`pgx/v5`, `SQLSTATE 40001` OCC retries, `FOR UPDATE` fencing), unified `internal/store/backend` factory (`STORE_BACKEND=dynamo|postgres`, `DATABASE_URL`, `POSTGRES_DSQL_MODE`), wiring across `main.go` and all 5 worker `cmd/` entrypoints, and 16 PostgreSQL integration tests (including a 110-request atomic reorder/rotation transition test). Zero-context code review caught and resolved `FOR UPDATE` fencing, `25P02` aborted-tx error classification, and standalone write OCC retry gaps.
  - **Local vignette verification, 2026-11-26:** Ran all 9 Playwright vignettes end-to-end against `http://localhost:3000` backed by `STORE_BACKEND=postgres` + local `import-worker`/`catalog-worker` + LocalStack S3/SQS — all 9 passed with zero high/medium findings (`queue-lifecycle` 10 beats, `fair-priority-rotation` 5, `favorites-and-edge-cases` 8, `kiosk-patron-session` 3, `round-robin-and-admission` 3 [2 expected info], `host-reorder-and-reconnection` 4, `songbook-catalog-management` 5, `songbook-import-management` 5 [1 expected info], `account-privacy` 3).
  - **DSQL IAM auth & OpenTofu, 2026-11-26:** Added Aurora DSQL IAM token signing (`internal/store/postgres/dsql_auth.go` via `aws-sdk-go-v2/feature/dsql/auth` + `BeforeConnect` + 45m `MaxConnLifetime`), `cmd/pg-migrate`, `cmd/dynamo-to-pg` (idempotent FK-ordered DynamoDB→Postgres/DSQL migration CLI with `--dry-run`, expired-TTL/slot filtering, `40001` retries, and `TestMigrateEndToEnd` verified locally between DynamoDB Local and PostgreSQL 16), and `infra/dsql.tf` (`enable_dsql = true`, `store_backend = "postgres"`, `dsql:DbConnectAdmin`/`dsql:DbConnect` IAM policies on all 6 Lambda roles).
  - **Live Aurora DSQL migration & cutover, 2026-11-26:** Provisioned `aws_dsql_cluster.main[0]` (`wruimmfjrsxf6eq2g4cxgeyp44.dsql.us-east-2.on.aws`) via GitHub Actions (`Deploy production` run 45866337299). Fixed two live DSQL DDL constraints (`auth_sessions.token_hash TEXT PRIMARY KEY` and non-`DESC` index on `import_jobs`), ran `cmd/dynamo-to-pg --dry-run=false` against `micup-production-` DynamoDB (75 scanned, 72 migrated & verified, 3 orphaned records skipped), and switched `store_backend = "postgres"` for production cutover. Remaining: verify live production traffic on Aurora DSQL, run a final delta sync if needed, and retire DynamoDB tables/code once soaked.

### 4. Pro & White-Glove Feature Roadmap

Maintained privately and not tracked in this repository (local `.private/PRO_ROADMAP.md`, gitignored).

---

## Explicit Architectural Non-Goals

- **No outbound webhooks for live queue synchronization:** Venue hosters run behind NAT/firewalls; monotonic conditional polling (`?since=v` returning `204 No Content`) is resilient and <\$0.01/show.
- **No always-on provisioned SQL instances for idle workloads:** Operational relational persistence targets scale-to-zero / consumption-priced AWS-native serverless SQL (Aurora DSQL, see PG1) rather than 24/7 provisioned RDS compute.
- **No OpenSearch:** Client-side Web Worker search (`search.json.gz` + MiniSearch) handles 50,000+ songs off the main thread at \$0 server cost.
- **No serving full catalogs from Lambda:** Large catalogs remain immutable, versioned S3 assets delivered via CloudFront.
- **No AWS WAF at current scale:** Rate limits and validation remain in application code for cost efficiency.
