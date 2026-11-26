# Repository review — 2026-09-12 (Archived)

**Status:** Archived. All 15 findings (Critical, High, Medium, Low) were remediated, verified with regression tests, and deployed to production prior to the initial release on 2026-09-13.

## Scope and verification

This review covered the Go API, DynamoDB store, catalog/import workers, frontend, infrastructure, and repository documentation. The following checks passed:

- `go test ./...`
- `go vet ./...`
- `go test ./internal/store/dynamo -count=1` against DynamoDB Local
- frontend tests and production build
- `tofu fmt -check -recursive infra`

`tofu validate` was blocked by an incompatible installed AWS provider plugin. Race and coverage runs also selected a Go 1.26 toolchain while the repository's mise configuration selects Go 1.25; this is recorded as a toolchain mismatch below.

Severity is based on exploitability and potential impact: **Critical** can expose or destroy another tenant's data; **High** can compromise account/data integrity or cause destructive production behavior; **Medium** can cause material operational or security defects; **Low** is correctness or release friction.

## Findings

### Critical

1. **OpenKJ actions are authorized by a public event UUID.** `resolveEvent` treats the event ID as the private integration key, while public event and queue responses expose that ID. Destructive OpenKJ routes are mounted without authentication, and `DeleteRequest` does not even resolve the event before deleting by request ID. An attacker who learns public identifiers can mutate or delete another event's queue. See `internal/handlers/openkj.go`, `internal/handlers/event.go`, `internal/handlers/queue.go`, and `internal/app/router.go`.

   **Remediation:** issue a distinct, revocable integration credential; enforce event scope and authorization on every OpenKJ action; make mutations POST-only; validate request ownership before deletion.

### High

2. **Passkey registration can pre-claim an email account.** Registration creates the user and email identity before WebAuthn completion is verified. An attacker can begin registration for a victim's email and leave an unusable claimed account.

   **Remediation:** keep registration state transient, verify the ceremony first, then atomically persist the user and credential with a conditional identity claim.

3. **Changing an email leaves the identity index stale.** The user record is updated without moving the identity claim. Login can continue resolving the old address, while the new address may be claimed by another account; deletion can then remove the wrong claim.

   **Remediation:** implement email change as a transactional old-claim delete/new-claim put/user update with conditional checks.

4. **Request reordering is not tenant-scoped.** The handler passes an event scope, but DynamoDB ignores it and updates arbitrary request IDs without conditions. A host can reorder requests belonging to another event if IDs are supplied.

   **Remediation:** load and validate every request's event ID, then perform conditional transactional updates.

5. **Catalog/import garbage collection can delete live data after a truncated scan.** Metadata listing methods perform a single DynamoDB scan with no pagination. GC treats the incomplete result as authoritative and can remove S3 prefixes for versions or imports that were simply on later pages.

   **Remediation:** paginate all scans and add a grace period/manifest existence check before destructive deletion.

6. **Production imports ignore `prune_missing=false`.** The API records the option, but the import worker drops it and the catalog worker rebuilds the catalog from only the incoming manifest. A non-pruning import can therefore remove existing songs.

   **Remediation:** carry the option through the job payload and merge against the current catalog when pruning is disabled; add an end-to-end regression test.

7. **Queue-version invalidation is incomplete and races with full event writes.** Public event changes and OpenKJ accepting-state changes do not consistently bump `queue_version`. `UpdateEvent` performs a full put with a possibly stale version and can overwrite a concurrent atomic bump, leaving clients with stale queues.

   **Remediation:** use partial conditional updates, atomically increment the version for every queue-visible change, and test concurrent updates.

8. **Account deletion is not a complete privacy purge.** The handler removes only selected sessions, passkeys, songbooks, and events, ignores requests/catalog/import records and media assets, and discards most errors. Event deletion also does not remove request PII.

   **Remediation:** implement a durable, auditable cascade (including S3 assets and all derived records), surface failures, and verify completion before reporting success.

### Medium

9. **Catalog compaction can delete a newer change.** The runner snapshots changes, publishes the result, then unconditionally deletes by key. A same-song update arriving during the build can be lost.

   **Remediation:** delete only the exact revision/timestamp that was compacted, using a conditional write.

10. **Rotation policy fields are accepted but not enforced.** Event creation hardcodes FCFS and queue creation does not enforce `MaxActivePerSinger`.

   **Remediation:** persist and apply the requested rotation mode and enforce singer limits atomically when enqueueing.

11. **Configured kiosk PINs are ignored.** The public event intentionally omits the PIN, while the kiosk client falls back to a hard-coded `1234`.

   **Remediation:** verify the PIN server-side through a scoped endpoint; never expose the configured secret in public event data.

12. **User display names are interpolated into HTML email.** Registration and reset messages use string formatting rather than HTML escaping, allowing user-controlled markup to be sent from the service domain.

   **Remediation:** render with `html/template` or escape every interpolated value, and test hostile display names.

13. **Queue modal hooks are conditional and effects can leak.** Several always-mounted Preact modals return before calling hooks when closed. Polling, timers, listeners, and wake-lock cleanup can remain active or violate hook ordering when visibility toggles.

   **Remediation:** call hooks unconditionally and gate work inside effects, or mount/unmount the modal components at the parent.

### Low

14. **OpenKJ endpoint advertised by the UI is missing `/v1`.** The UI copies `/api/openkj`, but the router mounts `/api/v1/openkj`; copied production URLs return 404.

   **Remediation:** centralize the API base path and update UI/docs and route tests together.

15. **Go toolchain declarations disagree.** `go.mod` requires Go 1.26, while `.mise.toml` and repository docs select Go 1.25. Local commands can fail before tests start, and some commands silently auto-download another toolchain.

   **Remediation:** choose one supported version and align `go.mod`, mise, CI, Docker, and documentation.

## Suggested order of work

1. Block public/pre-invite rollout until the OpenKJ authorization issue is fixed.
2. Fix the account, tenant-isolation, deletion, GC, import-pruning, and queue-version findings (2–8).
3. Address policy, kiosk, email, and frontend lifecycle issues (9–13).
4. Clean up endpoint and toolchain drift (14–15), then rerun the full verification suite including `tofu validate` in a clean provider environment.
