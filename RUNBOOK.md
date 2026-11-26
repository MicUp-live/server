# MicUp Pre-Invite Runbook

Use this runbook while the product is limited to invited KJs.

## Production deployment

If API Gateway still has a `$default` route, follow the
[two-step route migration](#migrate-production-from-the-catch-all-route)
before running a full production apply. This lets the new routes become
active before removing the catch-all.

Preview the production OpenTofu plan from GitHub Actions with
`gh workflow run deploy.yml --ref main -f plan_only=true`. Review the completed
run before applying with `gh workflow run deploy.yml --ref main -f plan_only=false`.

## Production smoke flow

Record the date, account, event code, and result after each release.

1. Register or sign in as a KJ, create a songbook, and complete an import.
2. Wait for the catalog worker to publish the manifest, then open the public
   songbook and confirm title search, Artist browse, and favorites work.
3. Create an event and join it on a separate browser/device using its short code.
4. Submit a request, cancel one request with its cancellation token, then use
   the host queue to accept, reorder, and complete a request.
5. Confirm the singer queue reflects each host change and the stage view loads.

## Recovery

### Roll back a release

#### 1. Writer Stop and Drain Matrix

Before executing any cross-revision rollback, schema reconciliation, or emergency state repair in AWS, stop all active writers and verify that in-flight invocations have drained. **Fixed timeout waits alone do not prove that all writers have drained**: Lambda function timeouts bound a single execution after it starts, but disabling an SQS `EventSourceMapping` or EventBridge rule is eventually consistent, and batches already polled by the Lambda poller can still invoke the function shortly after the disable API call returns. Always verify both that the trigger/concurrency state has transitioned (`Disabled` / `0`) and that active executions have dropped to zero in CloudWatch Metrics (`ConcurrentExecutions == 0`) and CloudWatch Logs (`REPORT RequestId:` emitted for every `START RequestId:`).

| Writer Surface | AWS Resource | Stop Command | Drain Verification (Fixed Waits Alone Are Insufficient) |
| --- | --- | --- | --- |
| HTTP API (browser, developer API, OpenKJ, compatibility routes) | Lambda `micup-production-api` (`timeout = 29s`) | Record current setting with `aws lambda get-function-concurrency --function-name micup-production-api`, then set `aws lambda put-function-concurrency --function-name micup-production-api --reserved-concurrent-executions 0` | Wait at least the $29\text{s}$ timeout ceiling **and** confirm `ConcurrentExecutions == 0` in CloudWatch Metrics plus matching `REPORT RequestId:` lines in `/aws/lambda/micup-production-api` (note: `tofu apply` clears out-of-band reserved concurrency unless set in `infra/lambda.tf`) |
| Scheduled import reconciler | EventBridge rule `micup-production-import-reconciler` (`rate(5 minutes)`) -> Lambda `micup-production-import-reconciler` (`timeout = 60s`) | `aws events disable-rule --name micup-production-import-reconciler` | Confirm rule `State == "DISABLED"` via `aws events describe-rule`, then verify `ConcurrentExecutions == 0` and no unfinished `START RequestId:` in `/aws/lambda/micup-production-import-reconciler` (up to $60\text{s}$ execution ceiling) |
| Scheduled catalog compaction | EventBridge rule `micup-production-catalog-compaction` (`rate(1 hour)`) -> Lambda `micup-production-compaction-dispatcher` (`timeout = 60s`) | `aws events disable-rule --name micup-production-catalog-compaction` | Confirm rule `State == "DISABLED"` via `aws events describe-rule`, then verify `ConcurrentExecutions == 0` and no unfinished `START RequestId:` in `/aws/lambda/micup-production-compaction-dispatcher` (up to $60\text{s}$ execution ceiling) |
| Scheduled catalog GC | EventBridge rule `micup-production-catalog-gc` (`rate(7 days)`) -> Lambda `micup-production-catalog-gc` (`timeout = 120s`) | `aws events disable-rule --name micup-production-catalog-gc` | Confirm rule `State == "DISABLED"` via `aws events describe-rule`, then verify `ConcurrentExecutions == 0` and no unfinished `START RequestId:` in `/aws/lambda/micup-production-catalog-gc` (up to $120\text{s}$ execution ceiling) |
| Background import worker | SQS `micup-production-import-jobs` EventSourceMapping -> Lambda `micup-production-import-worker` (`timeout = 900s`) | `aws lambda list-event-source-mappings --function-name micup-production-import-worker` then `aws lambda update-event-source-mapping --uuid <uuid> --no-enabled` | Poll `aws lambda get-event-source-mapping --uuid <uuid>` until `State == "Disabled"`, then verify `ConcurrentExecutions == 0`, `ApproximateNumberOfMessagesNotVisible == 0` on the queue, and matching `REPORT RequestId:` lines in `/aws/lambda/micup-production-import-worker` (up to $900\text{s}$ max timeout) |
| Background catalog builder | SQS `micup-production-catalog-build` EventSourceMapping -> Lambda `micup-production-catalog-worker` (`timeout = 900s`) | `aws lambda list-event-source-mappings --function-name micup-production-catalog-worker` then `aws lambda update-event-source-mapping --uuid <uuid> --no-enabled` | Poll `aws lambda get-event-source-mapping --uuid <uuid>` until `State == "Disabled"`, then verify `ConcurrentExecutions == 0`, `ApproximateNumberOfMessagesNotVisible == 0` on the queue, and matching `REPORT RequestId:` lines in `/aws/lambda/micup-production-catalog-worker` (up to $900\text{s}$ max timeout) |

#### 2. Rollback and Forward-Recovery Procedures

> **Important (`.github/workflows/deploy.yml` constraint)**: `.github/workflows/deploy.yml` hardcodes `actions/checkout@v5` with `ref: main`. Dispatching `deploy.yml` with `--ref <tag>` or from a non-`main` branch still checks out and deploys `main`—it **cannot** deploy a historical tag directly.

- **Rolling back to a compatible revision**:
  1. Confirm that no incompatible persisted-state, IAM, or schema boundary was crossed between the target and current release. Prefer forward recovery whenever a release introduced new DynamoDB tables, attributes, or S3 layout invariants.
  2. Stop and verify drain of writers using the matrix above if in-flight writes could conflict with the target revision.
  3. Because `deploy.yml` always checks out `main`, either:
     - **Local operator apply from the target tag**: check out the target git tag locally (`git checkout <tag>`), rebuild all artifacts from that working tree (`mise run build:all`; OpenTofu packages `dist/*.zip` directly from the workspace), inspect `tofu -chdir=infra plan`, apply the reviewed plan (`tofu -chdir=infra apply`), and run `mise run deploy:web` to sync `web/dist` to S3 and invalidate CloudFront (`/*`); or
     - **Advance `main` to a revert commit**: commit a revert on `main` and dispatch `deploy.yml` from `main`.
  4. Re-enable EventBridge rules, SQS event-source mappings, and `micup-production-api` concurrency, then execute the production smoke flow.

- **Forward-Recovery Procedure (Required Across Any Incompatible State Boundary)**:
  When a regression cannot be safely rolled back without crossing an incompatible persisted-state or schema boundary:
  1. **Contain**: If an active code path is corrupting data, pause the affected writer(s) and verify drain using the matrix above (`put-function-concurrency --reserved-concurrent-executions 0` on `micup-production-api`, `disable-rule` on EventBridge schedules, or `--no-enabled` on SQS event-source mappings).
  2. **Patch & Verify Forward**: Author a forward fix on top of `HEAD` that preserves all persisted attributes (`deleting*`, `pending_catalog_versions`, `dispatch_*`, `catalog_generation`, `metadata_version`, `auth_epoch`, `catalogs/epochs/*`, and single-table `event-requests`). Verify locally with `mise run ci:verify`.
  3. **Deploy Forward**: Merge the fix to `main` (and tag a new release), preview via `gh workflow run deploy.yml --ref main -f plan_only=true`, and apply via `gh workflow run deploy.yml --ref main -f plan_only=false` (or reviewed local `mise run build:all`, `tofu -chdir=infra apply`, and `mise run deploy:web`).
  4. **Reconcile & Resume**: Run the applicable idempotent operator recovery commands below (`cmd/import-reconciler`, `cmd/catalog-gc`) against `DYNAMO_TABLE_PREFIX=micup-production-`, re-enable any paused EventBridge/SQS/Lambda triggers, and verify with the production smoke flow.

  For local end-to-end recovery rehearsal against DynamoDB Local and LocalStack, run `python3 scripts/local/rehearse-m9-recovery.py`.

### Rebuild a failed catalog

Use the KJ songbook import/sync flow again. It enqueues a new catalog build;
the songbook keeps serving its prior ready catalog until the replacement is
fully published. Content-addressed catalog builds use exclusive, time-bounded leases
(`LeaseExpiresAt`, default 10 minutes) with random lease tokens:
- **Failed build retry**: If a build fails (e.g., transient S3 upload failure), the version is marked `failed`. Re-importing or retrying with identical content exclusively reclaims the failed version, uploads the immutable assets, marks it ready, and points the songbook to it.
- **Abandoned build reclamation**: If a worker crashes or fails while recording failure status, the build remains in `building` until its lease expires, after which any retry or subsequent build exclusively reclaims the abandoned version.
- **Concurrent reuse**: When multiple songbooks share identical content, workers wait boundedly for active builds and publish each songbook to the resulting catalog; unfinished builds propagate `ErrBuildInProgress` to SQS for redelivery without dropping jobs.
- **Publication fencing & supersession**: Competing imports and compactions are fenced with DynamoDB transaction conditions (`ExpectedBaseVersion`, `JobGeneration`). If an older delayed import finishes after a newer one has published, publication rolls back and the worker marks the import failed (`import superseded by newer catalog publication`) to prevent catalog regression. Additive imports and compactions reload the newer active catalog, re-merge, and retry safely (up to 3 attempts).

Inspect `/aws/lambda/micup-production-catalog-worker` for specific worker errors.

### Recover from interrupted catalog cleanup or partial asset deletion

Catalog garbage collection (`catalog-gc`) and user account deletion cascades (`internal/account/service.go`) use a two-phase deletion protocol in `store.CatalogStore` to prevent asset deletion races with concurrent publication:
- **Physically disjoint epoch namespaces**: Builders write new catalog assets to disjoint epoch namespaces (`catalogs/epochs/<id>/<epochToken>`). If a legacy cleanup worker is delayed during S3 asset deletion of `catalogs/<id>` and its lease expires, a builder reclaims and publishes to the disjoint epoch namespace. When the stale legacy worker resumes, its S3 deletion of `catalogs/<id>` cannot reach `catalogs/epochs/<id>/<epochToken>`. All new assets remain 100% available and intact.
- **Layout-aware discovery & letter shard protection**: `catalog-gc` prefix discovery distinguishes `catalogs/epochs/` from legacy `catalogs/<id>/` layouts. Internal legacy shard folders like `catalogs/<id>/letters/` are never treated as independent orphan prefixes, and `PurgeOrphanCatalogPrefix` protects active asset descendants, ensuring letter shards of referenced legacy catalogs are preserved.
- **Reservation lock (`status = deleting`)**: The version is locked in `deleting` status with a time-bounded lease (`LeaseExpiresAt`, default 10 minutes) and unique lease token. Concurrent publications (`PublishCatalogVersion`) are rejected by DynamoDB condition check `#status = :ready`, preventing any live songbook from pointing to assets being deleted.
- **Orphan prefix exclusion protocol**: For unmapped S3 prefixes without DynamoDB rows, `PurgeOrphanCatalogPrefix` claims an atomic reservation placeholder (`status = deleting`, `attribute_not_exists(id)`) in DynamoDB before deleting S3 objects. This fences concurrent builders (`CreateCatalogVersion` / `PublishCatalogVersion`) from racing with S3 deletion.
- **Durable detachment in account deletion**: User songbooks are durably deleted in DynamoDB (`DeleteSongbook`) *before* unreferenced catalog versions are purged. If songbook deletion fails on any book, the cascade aborts immediately, leaving all catalog assets and versions intact.
- **Strongly consistent verification**: Before any S3 objects are deleted, `IsCatalogVersionReferenced` performs a strongly consistent scan on the `songbooks` base table. If any active songbook references the version, the reservation is automatically rolled back to `ready` and S3 assets remain untouched.
- **Partial S3 failure or worker interruption**: If S3 object deletion encounters transient failures (network timeouts, AWS errors) or the worker crashes mid-cleanup, the catalog version remains safely quarantined in `deleting` status with its lease token. No live songbook references it, and no publisher can attach to it.
- **Automatic recovery paths**:
  1. *Subsequent GC run*: The next scheduled `catalog-gc` invocation scans for candidate versions, including those in `deleting` status with expired leases (`lease_expires_at <= now`). `ReserveCatalogVersionForDeletion` atomically reclaims the lease, re-verifies references, idempotently completes prefix asset purging (S3 `DeleteObjects` is idempotent), and deletes the DynamoDB record.
  2. *Builder reclamation*: If a host re-imports or builds a songbook matching the identical content hash, `catalog.Builder` detects the abandoned `deleting` version with expired lease, reclaims it via `ReclaimCatalogVersion` (resetting status to `building`), rebuilds and re-uploads all assets fresh to a new disjoint epoch prefix (`catalogs/epochs/<id>/<token>`), and publishes the songbook.
  3. *Stale worker fencing*: A delayed or zombie worker holding an old lease token cannot delete the DynamoDB record once another worker has reclaimed the reservation.
- **Operational diagnosis**:
  - Filter `/aws/lambda/micup-production-catalog-gc` logs for `[GC ERROR]` or candidate unreferenced catalogs.
  - To inspect candidates without deletion, run `go run ./cmd/catalog-gc --dry-run=true`.
  - To trigger cleanup after verifying storage health, run `go run ./cmd/catalog-gc --dry-run=false`.

### Recover stuck or exhausted import jobs

Songbook imports follow a durable dispatch and worker processing protocol. If an import appears stuck in pending or queued status, or encounters transient infrastructure outages:

1. **Unattended Background Reconciliation (`cmd/import-reconciler`)**:
   - The scheduled reconciler Lambda (`micup-production-import-reconciler`) runs every 5 minutes.
   - It queries DynamoDB for unconfirmed dispatch-ready jobs (`ListDispatchableImportJobs`) with no claim or an expired lease (>5m), acquires an exclusive claim, and dispatches them to SQS without requiring user API reads.
2. **Ambiguous Delivery & Attempt Exhaustion**:
   - Reaching the attempt limit (`maxAttempts = 5`) does NOT delete the job or manifest. Reaching an attempt limit cannot prove no message was delivered (e.g. SQS accepted the message before network timeout).
   - The job remains safely in `pending` or `queued` status with its source manifest preserved in S3 and its record intact, omitted from normal dispatch to prevent runaway SQS loops.
   - The reconciler logs high-priority alert warnings in `/aws/lambda/micup-production-import-reconciler`:
     `ALERT: <N> jobs have reached attempt limit (5) and require operator attention`.
3. **Operator Inspection & Recovery**:
   - **List exhausted jobs**: Run `go run ./cmd/import-reconciler --list-exhausted` to inspect all jobs that reached the attempt limit, showing job IDs, songbook IDs, attempt counts, and recorded error strings.
   - **Reset exhausted job for retry**: Run `go run ./cmd/import-reconciler --reset-job <job-id>` to clear the error, reset dispatch attempts to 0, and clear claim tokens. The next reconciler run (or direct invocation) will re-attempt dispatch cleanly.
   - **Terminal failed jobs**: If manifest parsing failed or an import was superseded by a newer publication, the job is in terminal `failed` status (`ImportJobFailed`). Terminal failed jobs cannot be resumed or reset; hosts must upload a fresh manifest.

### Recover interrupted account deletion

Account deletion (`DELETE /api/v1/auth/me`) is a multi-step cascade that immediately fences the user record (`deleting = true`, allocated `deletion_epoch`), captures initiating authority in `deletion_auth_epoch`, rotates `auth_epoch`, and clears `password_hash` before revoking sessions, passkeys, and developer keys and purging events, requests, songbooks, import jobs, and unreferenced catalog versions.

1. **Immediate Credential Revocation & Isolation**:
   - Once `BeginAccountDeletion` commits, all normal authentication surfaces (`RequireAuth`, login, session refresh, password reset, email verification, passkey login/registration, and `mk_live_` developer keys) reject the account (`401`/`400`).
   - Resource and credential writers (auth sessions, passkeys, developer keys, songbooks, events, import jobs, catalog changes) are fenced at commit time in DynamoDB against accounts undergoing deletion.
   - Never clear `deleting = true` or restore revoked credentials (`password_hash`, `auth_epoch`, passkeys, or sessions) to recover an interrupted deletion.
   - Any interrupted-request access token fallback must validate explicitly captured initiating authority (`claims.AuthEpoch == user.DeletionAuthEpoch`). Access tokens revoked before deletion (such as pre-password-reset tokens) are strictly rejected (`401`).
2. **Caller-Driven Retry (`X-Deletion-Token`)**:
   - When `DELETE /api/v1/auth/me` fails after `BeginAccountDeletion`, the API logs `[PRIVACY ERROR]` with the failing step and returns HTTP 500 with `X-Deletion-Token` and JSON `{detail, step, deleting: true, deletion_token}`.
   - The browser SPA (`web/src/api/client.js`) or API caller can immediately retry `DELETE /api/v1/auth/me` (`{"confirm":"DELETE"}`) with `X-Deletion-Token` (or `Authorization: Bearer <deletion_token>`) without needing an active session, password, or passkey.
   - Owned songbook `catalog_version_id`s are persisted on the user record in `pending_catalog_versions` before any songbook is deleted. Each catalog reservation allocates an exclusive per-attempt lease token and time-bounded lease (`LeaseExpiresAt`). Concurrent retries or stale workers never share execution authority; shared catalog versions referenced by other hosts remain protected (`ErrCatalogVersionInUse`), and active builds or reclaimed catalogs remain safe from stale deleters.
   - Repeated `DELETE /api/v1/auth/me` requests after completion return `204 No Content`.
3. **Operator Inspection & Entry-Point Recovery**:
   - Filter `/aws/lambda/micup-production-api` logs for `[PRIVACY ERROR]` to inspect the `user_id` and failing `step`.
   - Incomplete deletions remain marked in the `users` table with `deleting = true`, `deletion_started_at`, `deletion_step`, `deletion_error`, and `pending_catalog_versions` (discoverable via strongly consistent scan `AuthStore.ListDeletingUsers`).
   - The operator tool `cmd/catalog-gc` provides concrete CLI entry points for inspection and recovery:
     - **Inspect deleting accounts**:
       `go run ./cmd/catalog-gc -list-deleting-accounts`
       Outputs user IDs, emails, failing steps, error messages, start timestamps, and pending catalog counts.
     - **Recover all deleting accounts**:
       `go run ./cmd/catalog-gc -recover-deleting-accounts`
       Resumes cascading deletion for all accounts marked as deleting, purging remaining resources and catalog versions before finalizing each account.
     - **Recover a specific account**:
       `go run ./cmd/catalog-gc -recover-account <user-id>`
       Drives cascading deletion to completion for the targeted user ID.
   - Account finalization (`DeleteUser`) is strictly conditioned on the authoritative deletion workflow state (`deleting = true`, matching `deletion_epoch`, and zero pending catalog versions remaining).

### Remove a bad event or request

Use the KJ Live Queue to reject, complete, or delete a request. End an event
from its event controls; ended events and their queue history expire after the
configured retention period. Delete a test event from the host event controls.

## Logs and cost budget

- API logs: `/aws/lambda/micup-production-api`
- Catalog worker logs: `/aws/lambda/micup-production-catalog-worker`
- Cost budget: `micup-production-cost-guardrail`

For a failed import, filter the worker log group by the songbook ID. For an API
failure, filter the API log group by the request path or event ID. API Gateway
returns 404 for unmatched methods and paths before invoking Lambda, so those
requests do not appear in the Lambda log.

### Migrate production from the catch-all route

On the first rollout, apply a reviewed plan targeted at
`aws_apigatewayv2_route.api` to create the explicit routes while the old
`$default` route is still active. Confirm that the plan only creates API
routes. Verify the route keys in API Gateway and smoke-test health, CORS, an
authenticated-route denial, and the OpenKJ aliases before applying a second
reviewed plan targeted at `aws_apigatewayv2_route.default` to remove the
catch-all. Allow the stage to auto-deploy after each apply. After removal,
repeat the smoke tests and confirm the following responses.

| Request | Expected response |
| --- | --- |
| `GET` and `HEAD /api/v1/healthz` | 204 through CloudFront |
| `OPTIONS /api/v1/events/example/requests/reorder` | 204 with the configured Origin and `Idempotency-Key` in allowed headers, 403 with a disallowed Origin |
| `GET /api/v1/events/example/requests` without credentials | Application 401 |
| `GET /api/v1/.env` | API Gateway 404 with JSON `{"message":"Not Found"}` |

Check `/healthz` and the legacy `/openkj/api.php` alias directly against the
API Gateway endpoint. The stage itself keeps the name `$default` throughout.

If the cutover needs to be rolled back, use the previous infrastructure
revision to plan and apply only `aws_apigatewayv2_route.default`, then verify
health and the affected route.

## Operational Rate Limiting & Troubleshooting 429s

MicUp enforces distributed multi-instance rate limits across API Lambda containers via the DynamoDB rate limits table:

- **Authentication Endpoints** (`/api/v1/auth/*`): 10 requests / minute per client IP.
- **Public Endpoints** (Singer requests `/api/v1/events/{id}/requests`, Kiosk PIN check `/api/v1/events/{id}/kiosk/verify-pin`, Join code lookup `/api/v1/events/code/{code}`): 12 requests / minute per client IP.

### Investigating HTTP 429 (Too Many Requests)

1. Filter API logs (`/aws/lambda/micup-production-api`) for `"status": 429` or `"rate limit exceeded"`.
2. Inspect the caller IP and requested path. Responses include standard rate limit headers:
   - `RateLimit-Limit`: Configured threshold (e.g., 10 or 12).
   - `RateLimit-Remaining`: Remaining request allowance (0 when limited).
   - `Retry-After`: Seconds until the 1-minute window resets.
3. Check the DynamoDB table (`micup-production-rate-limits`):
   - Key format: `rl:<bucket>:<ip>:<epoch_minute>`.
   - Items are automatically purged via DynamoDB native TTL on the `ttl` attribute (Unix epoch timestamp).
4. **Fallback Mode**: If DynamoDB is unreachable, the rate limiter automatically falls back to an in-memory token bucket and logs a warning (`rate limiter falling back to in-memory`). Legitimate traffic is not dropped due to datastore outages.

### Tuning Limits for High-Density Venues

If a high-density venue operates all singer devices through a single shared NAT/Wi-Fi router IP and legitimate patrons encounter 429s during peak submission rushes:
1. Update Lambda environment variables in OpenTofu (`infra/lambda.tf` / `infra/variables.tf`) or Lambda runtime configuration:
   - `AUTH_RATE_LIMIT`: default `10/m`
   - `PUBLIC_REQUEST_LIMIT`: default `12/m`
2. Apply changes via normal deployment flow (`tofu apply`).

### Developer-Key Scope Rollout

`DEVELOPER_KEY_SCOPE_MODE` controls route-level authorization for `mk_live_`
keys. It defaults to `enforce`, where missing scopes return structured
`403 insufficient_scope` responses. Set the OpenTofu variable
`developer_key_scope_mode` to `report` when auditing an established deployment;
report mode logs missing scopes without including tokens or key prefixes and
preserves access. Pre-A6 records without a scope-version marker (including
empty-scope records) are grandfathered with full access so they can be migrated
deliberately.

## Password-reset authentication epochs

Password resets use a conditional update on the existing user record: matching
user ID, email, and prior `auth_epoch` are required to replace `password_hash`
and `auth_epoch` together. A successful reset invalidates all earlier reset links
and bearer access/refresh sessions for that account. Passkeys, developer keys,
and OpenKJ credentials remain separate authorities. Revoked refresh records
remain unusable and expire through the existing sessions-table TTL; no session
scan or new table is required.

The epoch is an additive optional attribute on users and refresh sessions and an
optional signed claim on access/reset tokens. Missing epochs on existing records
and tokens mean the empty epoch. These accounts retain their existing sessions
until their first reset. New profile and email writes condition on the epoch so
an in-flight snapshot cannot undo reset authority. New sessions issued from a
snapshot taken before reset carry the old epoch and cannot authenticate.

Release all authentication readers and user writers together. Mixed old/new
writers can drop the epoch, and old middleware ignores revocation. After an
account has reset, rollback to code without epoch support is unsafe. Roll back
only to an epoch-aware revision or forward-fix. Do not remove epochs or restore
pre-reset authentication data to regain compatibility; that would restore revoked
authority. M9 still owns a release/data recovery rehearsal; this is a code and
compatibility boundary, not evidence of a deployment drill.

A reset replay or expired/identity-mismatched link returns HTTP 400. A storage
failure returns HTTP 500. Retrying after a definitively failed commit preserves
reset authority; after an ambiguous response, the first attempt may have committed
and a retry may return 400. Sign in with the proposed new password or request a
fresh reset link. The reset-email request always returns a neutral response for
valid email input, including unknown accounts and delivery failures; inspect
application/email logs for delivery failures. Do not interpret HTTP 200 as proof
of delivery.


## Event settings snapshot versions

Event snapshot updates compare both `queue_version` and `metadata_version`, and
reject an in-progress clearing restart. Every successful snapshot write advances
`metadata_version`. Queue-visible settings/status changes also advance the public
queue version; private OpenKJ key rotation preserves its existing no-poll-bump
behavior. Nonempty rotation transitions check and advance the same metadata
version in the transaction that updates request placement and queue version.
Queue-only and specialized restart commands still fence snapshots through their
queue-version changes. No additional table, service, or normal-success read is
needed; a rejected conditional write reads the current event to report the conflict.

The internal metadata version is optional on old event records and defaults to
zero, so no bulk backfill is required. It is omitted from host/public JSON and
is not a browser/developer precondition. Deploy all event snapshot writers and
rotation transactions together. Old unconditional writers can still overwrite
newer state, so rollback must retain this conditional boundary. Keep metadata
versions when restoring event data. A production migration/rollback drill remains
M9; local legacy-record tests are not evidence of that drill.

A stale server snapshot returns HTTP 409 with `queue_version_conflict` and
`X-Queue-Version`, including expected/current public queue versions. Private
metadata changes can cause a conflict even when those public versions are equal.
Reload event/queue state and submit only the fields/action the host still intends;
do not blindly replay a cached whole event. Storage errors return failure, and
conditional conflicts do not run subsequent retention work. These endpoints do
not gain an idempotency/replay guarantee. Multi-step retention and clearing restart
retain their existing partial-failure/recovery behavior; a failure after an event
commit does not imply that the event commit rolled back.

## Songbook collaboration rollout and rollback

1. **Additive storage first:** Apply `infra/dynamodb.tf` to create `micup-<env>-songbook-grants` and `micup-<env>-songbook-invitations` before deploying application code that reads or writes them. Existing songbooks and events require no backfill; missing `permissions`, `deletion_confirmed`, and `detached_songbook_name` attributes default to legacy owner-only behavior.
2. **Deploy API, workers, and web together:** Deploy the Go API, background workers, API Gateway routes (`infra/api-routes.json`), and Preact bundle together so every writer, OpenKJ action, compatibility queue route, and account/songbook deletion cascade enforces cross-owner boundaries before invitations are sent.
3. **Rollback preserving existing shared shows and grants:**
   - Do **not** drop `songbook-grants` or `songbook-invitations` during rollback; keep both tables intact so accepted grants and detached-event metadata are preserved.
   - To halt new invitations without disrupting active collaborator shows, disable or remove only the `POST /api/v1/songbooks/{id}/invitations` and `POST /api/v1/songbook-invitations/accept` gateway routes (or revert to a forward-fix build that disables invitation creation while retaining cross-owner event/queue/deletion support).
   - Avoid rolling back to pre-collaboration code once cross-owner events exist, because older `DeleteAccount` and songbook compatibility queue routes assume `event.owner_id == songbook.owner_id`. If an interrupted songbook deletion leaves `deleting = true` and `deletion_confirmed = true`, re-issuing `DELETE /api/v1/songbooks/{id}` as the owner resumes cleanup idempotently.
