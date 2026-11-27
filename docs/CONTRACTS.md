# MicUp.live refactor contracts

Read [CONCEPTS.md](CONCEPTS.md) for meaning before changing implementation.
This document assigns stable IDs to behavior a refactor must preserve and to
requirements it must not silently weaken. Baseline: code and test inspection on
2026-10-10, not a fresh test run or production certification.

## Authority and how to use this document

- [openapi.yaml](openapi.yaml) owns HTTP payloads, enums, defaults, status codes,
  and headers. Published specs are generated, not independently edited.
- This document owns the refactor checklist and cross-boundary guarantees;
  CONCEPTS.md owns vocabulary and product intent.
- [TASKS.md](../TASKS.md) owns current gaps, acceptance criteria, and dated evidence.
  A requirement marked as a gap below is a target, not an implemented guarantee.
- [ARCHITECTURE.md](../ARCHITECTURE.md) explains implementation;
  [RUNBOOK.md](../RUNBOOK.md) governs operations.

If sources disagree, record the discrepancy and characterize the current public
behavior. Do not silently bless an implementation bug or change a client contract
because an older narrative says otherwise. Separate deliberate behavior changes
from refactoring and update the canonical specification and tests together.

Each contract below supplies observable outcomes and acceptance scenarios.
Test links are starting points, not proof of complete coverage. Future agents
must retain these IDs when moving code/tests and record any revised semantics.

## C01 — Authority and tenant isolation

Every protected operation must establish identity, applicable host status,
resource ownership, and applicable credential scope. A valid credential alone
is insufficient. Developer credentials cannot manage/mint developer keys;
key-management routes require a host session. Revoked, expired, wrong-owner,
and insufficient-scope credentials must retain their denied behavior.

Public show identifiers grant only documented public access. Cancellation tokens
must not authorize other requests. Public projections must omit secrets and
host file locations. Preserve the configured scope rollout behavior rather than
silently replacing enforcement with report-only mode.

**Acceptance:** exercise each applicable credential through the real router,
including cross-tenant access, denied key management, public projections, and
OpenKJ destructive actions with public identifiers. Starting evidence:
[router tests](../internal/app/router_test.go),
[TestRouterDeveloperKeyManagementRequiresHostSession](../internal/app/developer_keys_test.go),
and [handler tests](../internal/handlers).
Passkey login records the verified assertion against the existing credential
before issuing a session. Removing a passkey while an assertion is being
verified must prevent a later login metadata write from restoring it.
See the [passkey login commit tests](../internal/store/dynamo/passkey_login_test.go).
Password-reset authorization is consumed with password replacement in one
conditional user update. Success invalidates earlier reset links and bearer
access/refresh sessions, including tokens without an epoch on legacy accounts.
Passkeys and integration credentials retain their separate authority. Replay
returns 400; storage failure returns 500. See M10 and
[TestDynamoHTTPPasswordResetRejectsReplayAndRevokesSessions](../internal/store/dynamo/password_reset_replay_test.go)
and [TestDynamoHTTPPasswordResetCommitBoundary](../internal/store/dynamo/password_reset_test.go).

Account deletion (`DELETE /api/v1/auth/me`) immediately fences the account in
`deleting = true` with an assign-once allocated `deletion_epoch`, captures `deletion_auth_epoch`,
rotates `auth_epoch`, and clears `password_hash` before cascading. In-flight and concurrent
credential/resource writers (`CreateAuthSession`, `SavePasskeyCredential`, `CreateDeveloperKey`,
`CreateSongbook`, `CreateEvent`) are fenced at commit time in DynamoDB transactions against accounts
undergoing deletion (`store.ErrAccountDeleting`); `CreateSongbook` and `CreateEvent` require an existing,
non-deleting owner. Child writers (`PutCatalogChange`, `CreateImportJob`, `upsertSong`, `CreateRequest`)
enforce parent existence condition checks at commit time, rejecting delayed writes after parent/user
deletion with `os.ErrNotExist`. Once `deleting = true` commits, all normal
authentication surfaces (`RequireAuth`, `OptionalAuth`, login, refresh, password reset,
email verification, passkey login/registration, and `mk_live_` developer keys) reject the
account (`401`/`400`) and never restore revoked credentials on partial failure. On
`DELETE /api/v1/auth/me`, fallback access tokens must validate explicitly captured initiating
authority (`claims.AuthEpoch == user.DeletionAuthEpoch`), strictly rejecting tokens revoked
before deletion (`401`). Dedicated retry uses the scoped `account_deletion_recovery` JWT
(`X-Deletion-Token` or `Bearer`) bound to the account's `user_id` and `deletion_epoch`; cross-account
token combinations return `403`. See M7 and
[TestDynamoAccountDeletionRecovery_PasswordAndPasskeyAndSharedCatalog](../internal/store/dynamo/account_deletion_recovery_test.go).

## C02 — Admission is one decision at commit time

A submission must respect event availability, cooldown, normalized singer limits,
and rotation placement using a complete current queue. A read failure is not an
empty queue. Request creation, singer claim, displaced placements, and queue
version must commit together. A concurrent pause, end, restart, reorder, or
submission must invalidate a stale plan where relevant. Conflict retry reloads
and recomputes with a bound; it does not repeat an obsolete write plan.

**Acceptance:** concurrent same-singer requests with/without a limit, pending and
auto-accepted intake, pause/end/reorder between planning and commit, read failure,
and failed transaction writes. Failures leave no partial request/claim/version.
See `TestSubmitRequestRecomputesAfterAdmissionConflict`,
`TestSubmitRequestStopsAfterBoundedConflicts`, and read-failure tests in
[queue service tests](../internal/queue/service_test.go); admission races in
[queue transaction tests](../internal/store/dynamo/queue_transactions_test.go).

## C03 — Queue order, lifecycle, and stage meaning

Pending intake is separate from live positions. Preserve round robin, fair
priority, FCFS, and supported manual behavior, including deterministic ties,
round movement, and singer-history effects. Automatic placement and rotation
switches preserve the first three live positions; explicit host operations
retain their documented permissions. Status aliases are translated at adapters:
`playing` to `up_next`, `declined` to `rejected`. No new playback states appear
in persisted/public request statuses as a side effect of refactoring.

**Acceptance:** stage buffer with fewer/exactly/more than three items, newcomer
insertion, repeat singers, pending acceptance, mode/round changes, host reorder,
and alias inputs. Verify rollback when displacement fails and transaction-size
rejection without partial writes. See [domain tests](../internal/queue/domain_test.go),
[transaction tests](../internal/store/dynamo/queue_transactions_test.go), and
[queue adapter tests](../internal/handlers/queue_test.go). Do not replace
operation-specific transaction limits with an arbitrary larger batch size.

## C04 — Synchronization and retry identity

An event queue mutation and its externally visible version commit together,
including supported compatibility mutations and deletion. Event-scoped status
and reorder APIs support optional `expected_queue_version` and `Idempotency-Key`;
existing clients omitting them remain supported. A supplied stale precondition
returns the specified conflict with version information. A matching replay
returns its stored result without another mutation/version bump; reuse for a
different identity/payload is rejected. Replay records have a bounded lifetime
(current API: 24 hours), not an eternal exactly-once guarantee.

Host queue responses retain `{requests, queue_version}`; reorder input retains
`{order: [...]}`. Conditional polling retains its specified 200/204 behavior,
empty 204 body, and `X-Queue-Version`. Public and host projections are distinct.
Browser CORS must allow required mutation headers and expose the version header;
HTTP success alone is insufficient if the browser cannot read it.

**Acceptance:** first success, unchanged poll, stale write, ambiguous-response
retry, changed-payload/tenant reuse, expiry, repeated delete, and compatibility
writers. See [transaction tests](../internal/store/dynamo/queue_transactions_test.go),
[queue adapter tests](../internal/handlers/queue_test.go),
[CORS tests](../internal/app/http_test.go), and canonical P2 contract fixture tests
in [contract_fixtures_test.go](../internal/app/contract_fixtures_test.go) consuming
[testdata/contracts/](../testdata/contracts/README.md). Cross-repository P2 closure
remains open until the external Rust `micup-client` suite also consumes these shared
fixtures; server-only tests do not certify desktop client compatibility.

## C05 — Event recovery and retention

Clearing restart closes admission before archival. Each bounded archival batch
commits its request changes and version together; interrupted restart stays
unavailable and can resume. Reopening requires authoritative empty-state checks
and a conditional finalization. Retrying retention after partial failure must
not extend the original ended-at/deadline, recreate deleted requests, or apply
stale expiry to an active/resuming show. Errors propagate to callers.

**Acceptance:** multi-batch failure/retry, submit/status/reorder/resume races,
finalization conflict, retention enumeration/write failure and retry, concurrent
delete, and stale retention after resume. See
[restart tests](../internal/store/dynamo/restart_test.go),
[event service tests](../internal/event/service_test.go), and M3b/M3c evidence in
TASKS.md. Unrelated event settings must not overwrite newer lifecycle, rotation,
retention, or integration-key state. Snapshot updates check the current queue
version plus an internal metadata version; nonempty rotation transitions check
both in the same transaction as their request placements. All snapshot writes
advance metadata version, including key rotation without a public queue bump.
A conflict changes no event/rotation state and does not start retention work;
settings, pause/resume, non-clearing restart, and key rotation return structured
409 with `X-Queue-Version`. No server retry reuses a stale snapshot. Reload and
resubmit intended fields; private-key changes can conflict with equal expected
and current queue versions. Event-mutation read/commit failures remain errors.

M11's local evidence maps these guarantees to
[TestEventSettingsRejectStaleServiceSnapshots](../internal/store/dynamo/event_settings_test.go),
[TestEventMutationHTTPConflicts](../internal/store/dynamo/event_settings_http_test.go),
[TestEventSnapshotVersionPersistence](../internal/store/dynamo/event_settings_versions_test.go),
and `TestEventMutationConflictDoesNotApplyRetention` /
`TestEventMutationReadFailurePropagates` in the event service tests.

## C06 — Catalog integrity and publication

Published assets are immutable and complete before becoming active. Preserve
catalog/search formats and identity normalization consumed by browsers, including
favorites compatibility and private/public field separation. Pending overrides
remain visible until safely incorporated. Additive import must retain prior
content; pruning/replacement must follow its explicit input semantics, not be
silently selected on a failed read. Manual/custom metadata and provider matching
must retain their established behavior.

**Acceptance:** deterministic compilation, merge versus pruning, unavailable
prior catalog, overrides changed during compaction, private data exclusion,
custom/unmatched tracks, and failed upload leaving the old usable publication.
See [compiler tests](../internal/catalog/compiler_test.go),
[runner tests](../internal/catalog/runner_test.go),
[builder tests](../internal/catalog/builder_test.go), and
[favorites tests](../web/src/utils/favoritesStore.test.js).

**Catalog recovery guarantees (M12):** failed and abandoned builds are exclusively
reclaimed via time-bounded leases (`LeaseExpiresAt`) with atomic state transitions back to
building. Identical-content retry succeeds after storage recovery; concurrent builds for
identical content publish each songbook to the active catalog; worker crashes and
failure-status recording errors have an automatic lease-expiration recovery path. Stale
workers holding expired lease tokens cannot fail or mark ready a newly reclaimed build.
See `TestReviewRetryCatalogAfterTransientUploadFailure`, `TestBuilderReclaimsAbandonedBuild`,
`TestBuilderConcurrentReusePublishesAllSongbooks`, `TestBuilderFailureStatusWriteErrorRecovery`,
and DynamoDB Local tests in `catalog_versions_recovery_test.go`.

**Catalog publication fencing guarantees (M13):** Competing imports, compactions, and direct builds are fenced via DynamoDB TransactWriteItems conditions using monotonic sequential job generations (`JobGeneration`, tracked via `catalog_generation` and allocated atomically on songbooks via `NextCatalogGeneration`) and expected base catalog versions (`ExpectedBaseVersion`).
- Generation allocation failures propagate immediately; timestamps (`UnixNano()`) are never substituted for sequential counters. Legacy jobs lacking generations are assigned sequential monotonic generations upon processing via durable assign-once `AssignImportJobGeneration` and persisted to DynamoDB; concurrent deliveries converge on the same persisted generation. Stale whole-job updates (`UpdateImportJob`) cannot clear or replace an already assigned generation. Job-read failures (`GetImportJob`) propagate immediately across both workers before allocating or publishing; workers never publish using unpersisted replacement generations.
- Additive imports (`pruneMissing = false`), compaction, and direct songbook builds (`BuildSongbook`) fence on `catalog_version_id = expected_base_version`. When a concurrent publication changes the active catalog during compilation, publication is rejected with `store.ErrPublicationBaseConflict`; runners reload the newly active catalog and songs, re-merge/recompute, and retry boundedly (up to 3 attempts).
- Replacement imports (`pruneMissing = true`) and builds with `JobGeneration` fence on `catalog_generation <= :job_gen`. Stale publications from delayed older jobs are rejected with `store.ErrPublicationSuperseded`. Both `ErrPublicationSuperseded` and `ErrPublicationBaseConflict` wrap `store.ErrStalePublication` for backward compatibility.
- `cmd/catalog-worker` distinguishes `ErrPublicationSuperseded` from `ErrPublicationBaseConflict`. On transient base conflicts or storage errors, manifests and SQS retryability are preserved. For confirmed supersession, the worker marks the job failed as `ImportJobFailed` and deletes the source manifest only after the durable failure status write succeeds; if the status update fails, the manifest is preserved and the error returns to SQS for redelivery.
- Non-manifest `BuildSongbook` callers (including worker and CLI paths) read the current catalog version, fence on `ExpectedBaseVersion` and generation, and recompute boundedly upon concurrent modification.
- Song overrides are retired only after verified publication success, and `DeleteCatalogChange` enforces conditional deletion (`attribute_not_exists(updated_at) OR updated_at = :updated_at`) to preserve concurrent additions and edits made during compaction.
See `catalog_publication_fencing_test.go` (`TestStore_Publication_CompetingImportsFinishingOutOfOrder`, `TestStore_Publication_ImportVersusCompaction`, `TestStore_Publication_OverridesAddedOrChangedDuringCompaction`, `TestStore_Publication_StaleRejectionAndSafeRetry`, `TestStore_Publication_ReadyVersionReuseAndM12RecoveryFencing`, `TestStore_Publication_DistinguishesSupersededFromBaseConflict`, `TestStore_Publication_LegacyJobAndAllocationFailureDoNotPoisonGenerations`, `TestCatalogPublicationFencing_LegacyJobDurableAssignOnceInterleaving`, `TestStore_UpdateImportJob_PreservesAndProtectsGeneration`), `runner_test.go`, `cmd/catalog-worker/main_test.go`, and `cmd/import-worker/main_test.go`.

**Catalog cleanup fencing guarantees (M14):** Garbage collection (`cmd/catalog-gc`) and user account cascading deletion (`internal/account/service.go`) coordinate through a shared two-phase deletion reservation protocol in `store.CatalogStore` (`ReserveCatalogVersionForDeletion`, `IsCatalogVersionReferenced`, `ReleaseCatalogVersionReservation`, `PurgeCatalogVersion`, `PurgeOrphanCatalogPrefix`, and lease-fenced `DeleteCatalogVersion`):
- **Physically disjoint epoch namespaces:** New catalog builds upload to disjoint epoch namespaces (`catalogs/epochs/<catalog-id>/<epoch-token>`, derived from unique build/reclaim lease tokens). A stale legacy cleanup worker resuming deletion of `catalogs/<catalog-id>` or an expired epoch worker deleting `catalogs/epochs/<catalog-id>/<old-epoch>` cannot reach or delete newly published assets because the namespaces are physically disjoint prefixes in S3. All newly published assets survive intact.
- **Layout-aware discovery & error handling:** `cmd/catalog-gc` implements layout-aware S3 prefix discovery (`listCatalogPrefixes`), explicitly handling pagination across continuation tokens and propagating listing errors immediately without swallowing. It distinguishes the `catalogs/epochs/` namespace from legacy `catalogs/<id>/` structures, treating `catalogs/<id>` as the catalog prefix and never treating internal legacy shard directories (such as `catalogs/<id>/letters/`) as independent orphan prefixes.
- **Active asset descendant protection:** `PurgeOrphanCatalogPrefix` inspects both exact matches and hierarchical relationships (`isDescendant` and `isAncestor`) against known catalog records. If a catalog is referenced by any live songbook, any deletion targeting its active asset prefix or descendant shards (such as `letters/`) is rejected with `store.ErrCatalogVersionInUse`, ensuring manifests, search indexes, and letter shards remain fully intact.
- **Reservation barrier:** Cleanup transitions a catalog version from `ready` (or `failed`) to `#status = :deleting` with a unique UUID lease token and time-bounded lease (`LeaseExpiresAt`). While in `deleting` status, all publishers (`PublishCatalogVersion` via `TransactWriteItems`) fail DynamoDB condition check `#status = :ready`, preventing any concurrent songbook from pointing to a catalog undergoing deletion.
- **Orphan-prefix exclusion protocol:** When `catalog-gc` encounters unmapped or orphan S3 prefixes, `PurgeOrphanCatalogPrefix` claims an atomic reservation placeholder (`PutItem` with `attribute_not_exists(id)` and `status = :deleting`) in DynamoDB before deleting S3 objects. Concurrent builders attempting to create (`CreateCatalogVersion`) or publish (`PublishCatalogVersion`) are fenced by `os.ErrExist` / condition check failure. Active in-progress builds reject orphan prefix deletion with `ErrCatalogDeletionConflict`.
- **Durable songbook detachment before asset purge:** In user account deletion cascades, all owned songbooks are durably deleted in DynamoDB (`DeleteSongbook`) *before* unreferenced catalog versions are purged. If songbook deletion fails on any book, the cascade aborts immediately with `CascadeError{Step: "delete_songbooks"}` without touching any catalog version or S3 assets.
- **Strongly consistent dependency check:** While the catalog is locked in `deleting` status, `IsCatalogVersionReferenced` performs a strongly consistent scan (`ConsistentRead: true`) on the `songbooks` base table. If any songbook references the version, the reservation is immediately released via `ReleaseCatalogVersionReservation` back to `ready` status with lease cleared, and `PurgeCatalogVersion` returns `store.ErrCatalogVersionInUse` without invoking S3 asset purging.
- **Shared catalog preservation:** In account deletion, once user songbooks are durably removed, any remaining reference detected by `IsCatalogVersionReferenced` belongs to another tenant. `PurgeCatalogVersion` returns `ErrCatalogVersionInUse`, preserving shared S3 assets and catalog metadata for other tenants.
- **Interrupted cleanup & partial S3 failure recovery:** If S3 asset deletion fails partially or a worker crashes while in `deleting` status, the version remains safely quarantined in `deleting` status with its lease token. Concurrent publications remain rejected. Once the lease expires (`lease_expires_at <= :now`), a subsequent GC run reclaims the reservation via `ReserveCatalogVersionForDeletion` and idempotently completes S3 asset purging and metadata removal. If a builder compiles identical content for an abandoned deleting version with an expired lease, `ReclaimCatalogVersion` resets the status to `building`, re-uploads fresh assets under a new epoch prefix, and marks ready. Stale zombie workers holding expired lease tokens cannot commit `DeleteCatalogVersion`.
See permanent DynamoDB Local tests in `catalog_cleanup_fencing_test.go` (`TestCatalogCleanupSuite`), `cmd/catalog-gc/main_test.go`, and `internal/account/service_test.go`.

## C07 — Durable background work

Job acceptance, dispatch, processing, and publication are distinct outcomes.
Browser closure must not lose accepted work. A queue message can be redelivered;
terminal replay must not regress completed work or require an already-deleted
source manifest. Enqueue failure must have an honest error and a recovery path,
including when recording that failure also fails.

**Acceptance & Guarantees (M8):**
- **Durable dispatch intent & expiring claims:** Presigned upload jobs become dispatchable only after completion is accepted (`MarkImportJobDispatchReady`); direct imports become dispatchable once their manifest and job record are durable. SQS dispatch uses an expiring dispatch claim token (`ClaimImportJobDispatch`, default 5-minute lease), recording bounded attempts (`dispatch_attempts`). Missing `dispatched_at` or `dispatched=false` is never treated as terminal failure; reads remain read-only and never fabricate terminal failure.
- **Ambiguous enqueue & duplicate delivery safety:** Reaching maximum dispatch attempts does NOT prove no message was enqueued (network timeout can follow successful SQS receipt). An exhausted job remains in `pending`/`queued` status intact with its manifest preserved, discoverable via `ListExhaustedImportJobs` and recoverable via operator reset `ResetImportJobDispatch`. Stale dispatch confirmations (`ConfirmImportJobDispatch`) or error recordings (`RecordDispatchError`) require matching claim tokens and active states and cannot overwrite newer claims or regress processing/completed status.
- **Worker progress fencing:** In-flight worker updates (`UpdateImportJobProgress`) are strictly conditioned on `attribute_exists(id) AND status = :processing`. They cannot mutate job identity, generation, or lifecycle state, and cannot revive terminal failed jobs. If a terminal failure commits between processing claim and progress update, progress updates fail with `store.ErrImportJobTerminal`.
- **Atomic publication/completion transaction:** `PublishCatalogVersion` commits catalog pointer update on `songbooks` and job completion on `importJobs` in a single DynamoDB `TransactWriteItems`. It conditions on `generation = :job_gen` with exact generation equality and active state `status IN (:processing, :queued)`. If publication encounters a canceled transaction, it reloads the durable job receipt before deciding between completed replay (`store.ErrImportJobAlreadyCompleted`), terminal failure (`store.ErrImportJobTerminal`), supersession, or base conflict. Completed replay succeeds even if the original catalog version was already purged or newer versions published.
- **Manifest deletion retryability:** S3 source manifest deletion in `cmd/catalog-worker` occurs only after durable completion or supersession commits. In completed or failed replay branches, manifest deletion failures (`client.DeleteObject`) are propagated back to SQS, ensuring redelivery for deletion retry without leaving orphaned manifests.
- **Unattended background reconciliation (`cmd/import-reconciler`):** Discovers unconfirmed dispatch-ready jobs with no claim or expired claims (`ListDispatchableImportJobs`), claims them, and enqueues them independently of user API reads. Wired to AWS Lambda and scheduled EventBridge rule (`rate(5 minutes)`) in OpenTofu and SAM local runtime.
See `internal/store/dynamo/import_dispatch_test.go`, `cmd/catalog-worker/main_test.go`, `cmd/import-worker/main_test.go`, `cmd/import-reconciler/main_test.go`, and `internal/catalog/service_test.go`. Do not describe this entire workflow as atomic or exactly-once.


## C08 — Account export and deletion

A required export read failure must produce failure, not a successful empty or
“complete” partial archive. Successful export must include the documented data
and omit credential secrets. Account deletion is a multi-step cascade, not one
atomic transaction; failed progress must be surfaced and recoverable without
cross-tenant effects. Shared catalog assets cannot be deleted solely because the
current account referenced them.

The browser retains the scoped recovery token returned by an interrupted
deletion, including when the DELETE was retried after a session refresh. The
next deletion attempt in that client sends the token.

**Acceptance:** every required export-source failure and successful aggregation;
see `TestExportData_FailurePropagation` and `TestExportData_FullSuccessCoverage`
in [account tests](../internal/account/service_test.go) and
[privacy adapter tests](../internal/handlers/privacy_test.go).

**Account deletion & credential-revocation recovery guarantees (M7 & M14):**
- **Durable tombstone, assign-once initialization, and commit-time fencing (`BeginAccountDeletion`):** Assign-once initialization converges concurrent requests on one `deletion_epoch`. Atomically sets `deleting = true`, captures `deletion_auth_epoch`, rotates `auth_epoch`, and clears `password_hash`. Profile/email updates (`UpdateUser`, `SetEmailVerified`), password resets (`CompletePasswordReset`), auth sessions (`CreateAuthSession`), passkeys (`SavePasskeyCredential`), developer keys (`CreateDeveloperKey`), songbooks (`CreateSongbook`), and events (`CreateEvent`) condition in DynamoDB transactions against deleting accounts, rejecting concurrent mutations with `store.ErrAccountDeleting`. `CreateSongbook` and `CreateEvent` require an existing non-deleting owner. Parent deletion barriers (`BeginSongbookDeletion`, `BeginEventDeletion`) are durably committed before child enumeration, marking `deleting = true` (and `status = ended` on events); all applicable child writers (`PutCatalogChange`, `CreateImportJob`, `CreateSong`, `UpdateSongbook`, `NextCatalogGeneration`, `CreateRequest`, `UpdateRequestWithQueueVersion`, `UpdateRequestAndReorder`, `BumpQueueVersion`, `BeginQueueRestart`, `TransitionQueueRotation`) enforce parent existence and non-deleting conditions at commit time, fencing delayed writes while parents still exist (`store.ErrEventDeleting` / `store.ErrSongbookDeleting` wrapping `store.ErrAccountDeleting`) and after parent/user deletion (`os.ErrNotExist`). In songbook deletion, `BeginSongbookDeletion` establishes the deletion barrier before capturing the authoritative catalog pointer (`catalog_version_id`), returning the atomic non-nil frozen state (treating nil as an explicit error before deleting children with no stale-snapshot fallback), which is durably persisted to `user.pending_catalog_versions` via `BeginAccountDeletion` (`record_catalog_versions`) before deleting children or the songbook, with failures propagated; subsequent publication commits after the barrier are rejected (`store.ErrAccountDeleting`).
- **Complete credential sweep & authoritative base-table enumeration:** Cascade steps revoke refresh sessions (`DeleteAuthSessionsForUser`), passkey credentials (`DeletePasskeyCredential`), and developer PATs (`RevokeDeveloperKey`) before deleting events, requests, songbooks, import jobs, and unreferenced catalog versions. All cleanup enumeration queries use strongly consistent base-table scans or queries (`ConsistentRead: true`, `dynamodb.NewQueryPaginator` for catalog changes), ensuring GSI lag cannot hide uncommitted or newly written items from deletion and multi-page catalog change sets exceeding 1MB are completely discovered. Revoked credentials are never restored if a later step fails.
- **Strict initiating authority validation on retry (`RequireAccountDeletionAuth`):** Fallback access tokens must validate explicitly captured initiating authority (`claims.AuthEpoch == user.DeletionAuthEpoch`), strictly rejecting tokens revoked before deletion (`401`). When a cascade step fails after `BeginAccountDeletion`, `DELETE /api/v1/auth/me` returns HTTP 500 with `X-Deletion-Token` (and JSON `{detail, step, deleting: true, deletion_token}`). The token is a signed JWT with `typ: "account_deletion_recovery"`, `sub: userID`, and `del_epoch: deletionEpoch`. It is rejected by `ParseAccessToken` on all other routes (`401`), and rejected (`403`) if paired with a different user's bearer token. Both password-backed and passkey-only accounts can retry `DELETE /api/v1/auth/me` using `X-Deletion-Token` after their sessions and passkeys are gone; repeated calls after user deletion return `204 No Content`.
- **Crash-safe catalog version tracking, unique lease tokens, and authoritative finalization:** Owned songbook `catalog_version_id`s are tracked via CAS retry loops in `pending_catalog_versions` (set-union on discover, set-difference on resolve, never lost updates). Durable progress writes (`RecordAccountDeletionProgress`) propagate failures immediately. Deterministic lease-token reuse is eliminated: every catalog deletion reservation allocates a unique UUID lease token and time-bounded lease (`LeaseExpiresAt`). Concurrent retries or stale cleanup attempts never share execution authority. M14's fencing across reclamation and catalog recreation is preserved; stale workers with expired leases cannot commit metadata deletions. Finalization (`DeleteUser`) is conditioned on the authoritative deletion workflow state (`deleting = true`, matching `deletion_epoch`, and zero `pending_catalog_versions`). Finalization retry exhaustion returns an explicit error; success requires confirmed user deletion.
- **Runnable operator recovery entry point (`cmd/catalog-gc`):** Accounts stuck in `deleting = true` (discoverable via `ListDeletingUsers`) are recorded with `deletion_step` and `deletion_error` via `RecordAccountDeletionProgress` and can be inspected or resumed without user credentials via `cmd/catalog-gc` CLI flags (`-list-deleting-accounts`, `-recover-deleting-accounts`, `-recover-account <user-id>`) and `Service.RecoverDeletingAccounts` / `Service.ResumeAccountDeletion` without adding a scheduled Lambda service.
See [account deletion recovery tests](../internal/store/dynamo/account_deletion_recovery_test.go) (`TestDynamoAccountDeletionRecovery_PasswordAndPasskeyAndSharedCatalog`, `TestDynamoAccountDeletion_AllDestructiveBoundariesAndOperatorRecovery`, `TestDynamoAccountDeletion_ConcurrentInitializationAndProgress`, `TestDynamoAccountDeletion_InFlightWriterCommitTimeFence`, `TestDynamoAccountDeletion_PreResetTokenDenial`, `TestDynamoAccountDeletion_LostResponseOperatorRecovery`, `TestDynamoAccountDeletion_StaleCatalogCleanupAttempt`, `TestDynamoAccountDeletion_DelayedChildCommitsFencedAfterParentDeletion`, `TestDynamoAccountDeletion_PauseAfterEnumeration_FencesDelayedChildCommits_LeavesNoOrphans`, `TestDynamoAccountDeletion_FinalizationRetryExhaustionFailsExplicitly`, `TestDynamoAccountDeletion_RacingPublicationBeforeBarrier_TrackedSurvivesAndPurgedOrPreserved`), [catalog cleanup fencing tests](../internal/store/dynamo/catalog_cleanup_fencing_test.go) (`TestCatalogChanges_ConsistentPagination_MultiPage`), [account service tests](../internal/account/service_test.go), [auth/privacy handler tests](../internal/handlers/auth_test.go), [cmd/catalog-gc tests](../cmd/catalog-gc/main_test.go), and [frontend API client tests](../web/src/api/client.test.js).

**Songbook collaboration boundaries (SC1):** A songbook has one permanent owner (`songbook.owner_id`) and each event is owned by its initiating host (`event.owner_id`). Accepted grants (`songbook-grants` keyed by `(songbook_id, user_id)`) and single-use invitations (`songbook-invitations` storing SHA-256 token digests) are managed exclusively via authenticated host sessions. Protected catalog writes, import upload completion, OpenKJ catalog actions, and event create/reassign/reactivate operations check current authority and fence against grant revocation and `songbook.deleting` at DynamoDB commit time. Owner deletion preserves `409` for owner events, requires an explicit dependency-bound confirmation token (`confirm_detach_token`) re-verified after the deletion barrier to end and detach collaborator shows while preserving their event and queue history under retention, and supports resumable cleanup. See [songbook collaboration DynamoDB tests](../internal/store/dynamo/songbook_collaboration_test.go).

## C09 — Browser continuity

Singer, host, stage, kiosk, and modal views must interpret the same queue
contract. Polling must handle unchanged responses, backoff, visibility recovery,
and cleanup without duplicate timers or stale event results. Browser-local
favorites/pitch must survive compatible refactors, including existing stored
identities. Kiosk inactivity must clear the previous patron's session state.
Keep error/loading/empty states distinct and preserve accessible interactions.
Selecting a different show in the host queue immediately removes the previous
show's requests and request actions from view. Delayed responses belong to
the show that initiated them. See the [host queue regression](../web/src/pages/kj/LiveQueue.test.js).

**Acceptance:** [polling tests](../web/src/hooks/useQueuePolling.test.js),
[favorites tests](../web/src/utils/favoritesStore.test.js), search and singer queue
behavioral tests, plus browser checks for changed interactions. A pure helper
test alone does not establish page behavior or storage migration safety.

## C10 — Persisted data and operational compatibility

A replacement must read existing records/assets and preserve identifiers,
references, TTL meaning, and client storage. Any changed representation needs
an explicit migration, concurrency boundary, reconciliation, and rollback plan.
Never treat a passing empty-database test as migration evidence. Current local
code uses event-keyed request storage; historical dual-write descriptions are
not instructions to reinstate a second authority.

**Acceptance:** representative old data, interrupted migration, concurrent writer
fencing, repeat execution, and restoration of matching code/data/assets. Consult
the [runbook](../RUNBOOK.md) and verify commands against the intended revision.
Event `metadata_version` is an additive internal snapshot fence, distinct from
public queue version. Missing values read as zero; no bulk backfill is required.
All snapshot writers and rotation transactions must preserve, compare, and
advance it. Readers and writers must release together; old unconditional event
writers do not enforce this boundary. See
[event settings recovery](../RUNBOOK.md#event-settings-snapshot-versions).

Production cutover/rollback rehearsal remains M9. This document authorizes no
production migration, reset, deployment, or persistent-volume removal.

Authentication epochs and account-deletion state (`deleting`, `deletion_epoch`,
`deletion_auth_epoch`, `deletion_started_at`, `deletion_step`, `deletion_error`,
`pending_catalog_versions`) are additive user/session attributes and signed token
claims. Missing values mean the legacy empty/non-deleting state; new code preserves existing
sessions until reset or account deletion advances that account's state. Profile/email writers fence
stale epochs and deleting accounts. Deploy readers and writers together; older code neither enforces
nor preserves epochs or deleting tombstones and is unsafe after resets or interrupted deletions. See the
[authentication recovery boundary](../RUNBOOK.md#password-reset-authentication-epochs) and
[interrupted account deletion recovery](../RUNBOOK.md#recover-interrupted-account-deletion).

## C11 — Configurable show policies (Tier 1 scoped 2026-11-26)

The Tier 1 subset of [CONCEPTS.md](CONCEPTS.md#accepted-configurable-hosting-policies)
is tracked under HP1 in TASKS.md. C01–C10 still apply to every policy choice.
Empty singer queue retention, strict stage-override locking, and separate
participant identity / duet turn accounting were explicitly marked **Won't Do**
on 2026-11-26.

**Configuration boundary:** `allow_song_replacement` belongs to the event and is
changed only by an authorized host operation under M11's conditional snapshot
boundary (`queue_version` + `metadata_version`). New shows default to `true`;
existing shows migrate with `FALSE` so prior shows are not silently reinterpreted.

**Acceptance scenarios required:**

- **Replacement:** an authorized singer holding the domain-separated `edit_token`
  changes their queued song (`PATCH /api/v1/events/{id}/requests/{rid}/song`)
  while `pending` or `accepted` without losing request identity, rank, round,
  status, or consuming another slot. Concurrent call-to-stage (`up_next`) or
  policy disable is fenced atomically at PostgreSQL commit time (`409`/`403`).
  Host-only mode (`allow_song_replacement = false`) denies singer replacement
  while host edits remain available. Respect `allow_key_change` and
  `allow_write_ins`. Cancellation tokens (`cancel_token`) must not authorize
  replacement, and edit tokens (`edit_token`) must not authorize cancellation;
  wrong-request or wrong-event tokens return `403`.
- **Absence deferral:** explicit host action (`POST /api/v1/events/{id}/requests/{rid}/defer`)
  bumps an `accepted` or `up_next` request down 3 active spots (or to the end of
  the active queue if fewer remain) and transitions `up_next` back to `accepted`
  without setting `completed_at` or incrementing completed-performance history.
  Pending/terminal requests return `409`. Permanent removal remains distinct;
  `Idempotency-Key` replay ensures retries never defer twice, and optional
  `expected_queue_version` enforces stale-snapshot conflict detection.
- **Closing:** closing admission (`paused`) rejects new submissions while
  queued performances remain actionable; reopening does not clear the queue,
  and `ended` remains distinct (already live).

## Refactor procedure and completion evidence

1. Name the affected concept and contract IDs. Inventory all callers: browser,
   singer/kiosk/stage, developer API, OpenKJ, compatibility routes, workers,
   operator tools, and stored/client data as applicable.
2. Record intended behavior, current discrepancies/TASKS IDs, transaction boundary,
   failure response, and explicit non-goals. Characterize existing supported
   behavior before replacement; write regression scenarios for deliberate fixes.
3. Replace one coherent capability and migrate its callers. Preserve public
   contracts; remove the superseded implementation or document compatibility
   ownership and its removal condition. Do not build another mutation authority.
4. Map every affected contract to named tests and results. Use real DynamoDB
   Local with `-count=1` for persistence, deterministic barriers for concurrency,
   and real middleware for authorization. A fake or race detector is insufficient.
5. Follow [AGENTS.md](../AGENTS.md)'s validation table: Go tests plus fitness for
   Go changes; frontend tests/build and applicable tokens/browser checks; OpenAPI
   regeneration/check for wire changes; `mise run ci:verify` for full integration
   readiness. Use `mise exec --` when tool versions/environment are not loaded.
6. Inspect the diff and run `git diff --check`. Record skipped/blocked checks and
   remaining gaps explicitly. Update TASKS evidence only for satisfied criteria.

A useful handoff record is:

```text
Scope and contract IDs:
Behavior preserved / intentional changes:
Callers and persisted formats checked:
Atomic boundary, conflict/retry and partial-failure behavior:
Named tests and exact commands/results:
Migration/rollback evidence (if applicable):
Known gaps / TASKS IDs / next step:
```

## Documentation authority

Use current code, canonical [openapi.yaml](openapi.yaml), and active [TASKS.md](../TASKS.md)
to resolve any narrative ambiguity; documentation alone never proves deployment state.
