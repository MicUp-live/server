# MicUp.live Playwright Vignette Coverage Matrix

This document maps MicUp.live core behavioral contracts ([docs/CONTRACTS.md](../../docs/CONTRACTS.md)), product concepts ([docs/CONCEPTS.md](../../docs/CONCEPTS.md)), and user-facing workflows to end-to-end multi-actor Playwright vignettes.

Vignettes exercise multi-role browser workflows using Host (Desktop), Stage Display (TV 720p), and Mobile Singers (Guest and Authenticated). They create temporary shows and may change the test account's favorites. Screenshots and beat descriptions are observations, not assertions; inspect findings and storyboards separately.

---

## 1. Contract Traceability Matrix (C01 – C11)

| Contract ID | Contract Title | Covered by Vignettes | Primary Vignette | Verification Details |
|---|---|:---:|---|---|
| **C01** | Authority and Tenant Isolation | Partial | `queue-lifecycle`, `favorites-and-edge-cases` | Guest join and self-cancellation are exercised; wrong-owner cancellation and cross-tenant denial are not vignette assertions. Go router tests cover additional authority boundaries. |
| **C02** | Admission is One Decision at Commit Time | Partial | `round-robin-and-admission` | Browser submissions verify max-active (`409 max_active_limit`) and cooldown (`429 submission_cooldown`) rejections do not enter the public queue. Concurrent admission conflicts, atomicity, and read failures remain backend-test evidence. |
| **C03** | Queue Order, Lifecycle, and Stage Meaning | Partial | `queue-lifecycle`, `fair-priority-rotation`, `round-robin-and-admission`, `host-reorder-and-reconnection` | Fair Priority placement and three-position protection, Round Robin rounds one and two, and a host reorder that keeps the called performer fixed. |
| **C04** | Synchronization and Retry Identity | Partial | `queue-lifecycle`, `favorites-and-edge-cases`, `fair-priority-rotation`, `host-reorder-and-reconnection` | HTTP polling synchronization verified across Host, Stage, and Singer; reconnect forces a full sync and updates the singer's queue position after a host reorder. Idempotent mutation replay remains backend/API evidence. |
| **C05** | Event Recovery and Retention | Partial | `queue-lifecycle`, `host-reorder-and-reconnection` | Host pause, resume, end, and restart without clearing the queue are exercised. Interrupted clearing restart, retention, and other recovery boundaries remain backend evidence. |
| **C06** | Catalog Integrity and Publication | Partial | `queue-lifecycle`, `favorites-and-edge-cases`, `songbook-catalog-management`, `songbook-import-management` | Public browser catalog indexing, songbook details and visibility, manual song CRUD/export, additive CSV import, and confirmed replacement of an imported catalog. Other merge/pruning paths and publication fencing remain backend evidence. |
| **C07** | Durable Background Work | Partial | `songbook-import-management` | UI import uploads a manifest, dispatches a job, and waits for worker completion. Recovery, retries, and publication fencing remain covered by backend tests. |
| **C08** | Account Export and Deletion | Partial | `account-privacy` | A disposable local host exports its account, songbook, and show, confirms deletion, and loses login and public show access. Partial-failure/recovery and tenant isolation remain backend evidence. |
| **C09** | Browser Continuity | Partial | `queue-lifecycle`, `favorites-and-edge-cases`, `fair-priority-rotation`, `kiosk-patron-session`, `host-reorder-and-reconnection` | Kiosk clears a patron's search/form after 45 seconds idle; reconnect after offline recovery refreshes queue position. Unchanged responses, long backoff, visibility lifecycle, and stale-event races remain broader polling gaps. |
| **C10** | Persisted Data & Operational Compatibility | No | - | Vignettes do not verify old data, interrupted migrations, rollback, or internal `metadata_version`. |
| **C11** | Configurable Show Policies (accepted target; not implemented) | No | - | HP1 remains open. `favorites-and-edge-cases` exercises existing write-in and manual-staging options, not C11 policies; neither vignette verifies C11 acceptance scenarios. |

---

## 2. User-Facing Workflow Coverage Matrix

| User Workflow | `queue-lifecycle` | `favorites-and-edge-cases` | Status | Notes |
|---|:---:|:---:|:---:|---|
| **Host Show Creation** | Covered | Covered | Covered | Temporary show with random join code; checks manual staging approval vs auto-accept, write-ins toggle. |
| **Songbook Details & Visibility** | - | - | Covered by `songbook-catalog-management` | Owner creates and renames a book, assigns a slug, publishes it, then returns it to private visibility. |
| **Manual Songbook Track Management** | - | - | Covered by `songbook-catalog-management` | Add, edit, delete, and CSV-export managed songs; export is checked against the resulting catalog. |
| **Assistant CSV Import** | - | - | Covered by `songbook-import-management` | Review CSV, verify additive preservation, cancel and confirm a replacement warning, then inspect the replacement catalog. |
| **Songbook Invitation Management** | - | - | Partial: `songbook-catalog-management` | Owner sends and cancels a pending invitation. Acceptance and permission changes are specified as a [manual vignette](#manual-vignette-songbook-collaboration), not yet executed. |
| **Public Show Join** | Covered | Covered | Covered | Singers join via `/e/<code>` without account creation. |
| **Catalog Search & Filtering** | Covered | Covered | Covered | Search by song/artist; instant search worker indexing. |
| **Logged-In Singer Prefill** | Covered | Covered | Covered | Logged-in singer screen name prefilled in request modal; guest prompted for screen name. |
| **Key Pitch Adjustments** | +1 response assertion | - | Partial | `queue-lifecycle` selects +1 and asserts the persisted request response; stage display, other values, and persistence across reloads remain gaps. |
| **Host Staging Inbox & Triage** | Covered | - | Covered | Accept incoming requests, Decline with custom reason. |
| **Host History Tab & Badges** | Covered | - | Covered | Shows completed and declined requests with explicit badges and decline reasons. |
| **Stage TV Display** | Covered | - | Covered | Displays On Stage (`up_next`), On Deck, QR code for bar patrons, live announcement banner. |
| **Live Broadcast Announcement** | Covered | - | Covered | Host broadcasts shout-out banner to Stage TV and Singer phones via `PATCH /api/v1/events/:id`. |
| **Show Pause & Resume** | Covered | - | Covered | Host pauses incoming requests; verifies singer request buttons are disabled. |
| **Call to Stage & Mark Done** | Covered | - | Covered | Host calls singer to stage (`CALLED TO STAGE` / `up_next`), singer marks done, rotation advances. |
| **Host End Show via UI** | Covered | - | Covered | Host opens Show Settings modal, clicks End Show, accepts browser confirm dialog, verifies ended state. |
| **Cross-Device Account Favorites** | - | Covered | Covered | Singer 1 stars song on Device A; logs into isolated Device B and verifies favorite syncs; logs out on Device A to verify local favorites cleared. |
| **Custom Write-In Requests** | - | Covered | Covered | Host enables `allow_write_ins`; singer searches for missing song and submits custom write-in request. |
| **Singer Self-Cancellation** | - | Covered | Covered | Singer submits request, opens mobile Queue modal, cancels pending request via cancel token, verifies host staging removal. |
| **Venue Tablet Kiosk Mode** | - | - | Covered by `kiosk-patron-session` | Patron search/submission, success/reset, 45-second idle cleanup, and host PIN exit. |
| **Fair Priority Rotation** | - | - | Covered by `fair-priority-rotation` | Returning singers, newcomer placement, stage protection, and host-only weights. `round-robin-and-admission` verifies two rounds. |
| **Host Queue Reordering** | - | - | Covered by `host-reorder-and-reconnection` | Host moves a request ahead while the called performer remains first; Stage and reconnecting Singer views converge. |
| **Show Restart** | - | - | Partial: `host-reorder-and-reconnection` | End and reopen without clearing; the public queue and singer position remain. Interrupted clearing and retention need backend tests. |
| **Account Export & Deletion** | - | - | Partial: `account-privacy` | A local disposable host exports profile, songbook, and show and deletes the account; sign-in and public show access are denied afterward. |

### Additional Local Browser Assertions

| Scenario | Vignette | Assertions |
|---|---|---|
| Multi-round Round Robin | `round-robin-and-admission` | Two singers' first requests stay in round one; repeat requests are placed in round two in submission order. |
| Active singer limit | `round-robin-and-admission` | A second active request receives `409 max_active_limit` and is absent from the public queue. |
| Singer cooldown | `round-robin-and-admission` | An immediate second request receives `429 submission_cooldown` with remaining seconds and is absent from the public queue. |
| Host queue reorder | `host-reorder-and-reconnection` | Host moves Casey ahead of Blake while Avery remains `up_next`; public and Stage order are checked. |
| Browser reconnection | `host-reorder-and-reconnection` | Blake goes offline while the host reorders, then returns online; visibility recovery refreshes Blake's displayed queue position. |
| Kiosk patron session | `kiosk-patron-session` | Search/submission/success, next-patron reset, 45-second inactivity reset, and configured PIN exit are asserted. |
| Songbook catalog management | `songbook-catalog-management` | Owner creates and renames a book, sets a slug and visibility, adds/edits/deletes songs, verifies CSV export, and manages a pending invitation. |
| Songbook import management | `songbook-import-management` | CSV preview and additive retention are asserted; canceling a replacement warning does not dispatch, while confirming it publishes the replacement. |
| Show restart | `host-reorder-and-reconnection` | Singer sees the ended state; restart without clearing restores the existing public queue and singer position. |
| Account privacy | `account-privacy` | A disposable local account exports its book/show without password material, confirms deletion, and loses login and public show access. |

### Local End-to-End Evidence (2026-11-25)

All reports target `http://localhost:3000`; no production resources were used.

| Vignette | Report directory | Beats | High | Medium | Info |
|---|---|---:|---:|---:|---:|
| `queue-lifecycle` | `output/2026-11-25T16-14-56-queue-lifecycle` | 10 | 0 | 0 | 0 |
| `favorites-and-edge-cases` | `output/2026-11-25T16-18-46-favorites-and-edge-cases` | 8 | 0 | 0 | 0 |
| `fair-priority-rotation` | `output/2026-11-25T16-19-14-fair-priority-rotation` | 5 | 0 | 0 | 0 |
| `kiosk-patron-session` | `output/2026-11-25T16-13-45-kiosk-patron-session` | 3 | 0 | 0 | 0 |
| `round-robin-and-admission` | `output/2026-11-25T16-11-56-round-robin-and-admission` | 3 | 0 | 0 | 2 |
| `host-reorder-and-reconnection` | `output/2026-11-25T16-14-41-host-reorder-and-reconnection` | 2 | 0 | 0 | 0 |
| `songbook-catalog-management` | `output/2026-11-25T16-35-16-songbook-catalog-management` | 5 | 0 | 0 | 0 |
| `songbook-import-management` | `output/2026-11-25T16-34-38-songbook-import-management` | 3 | 0 | 0 | 1 |
| `songbook-import-management` (replacement follow-up) | `output/2026-11-25T17-01-45-songbook-import-management` | 5 | 0 | 0 | 1 |
| `host-reorder-and-reconnection` (restart follow-up) | `output/2026-11-25T16-58-56-host-reorder-and-reconnection` | 4 | 0 | 0 | 0 |
| `account-privacy` | `output/2026-11-25T17-00-51-account-privacy` | 3 | 0 | 0 | 0 |
| `account-privacy` (local request guard) | `output/2026-11-25T17-04-39-account-privacy` | 3 | 0 | 0 | 0 |
| `account-privacy` (cleanup follow-up) | `output/2026-11-25T17-05-26-account-privacy` | 3 | 0 | 0 | 0 |

The two informational admission findings are the expected `409 max_active_limit`
and `429 submission_cooldown` responses; assertions confirm neither request was
added to the queue. The import vignette's informational finding is the expected
empty `GET /import-jobs/latest` lookup before the first import.

---

## 3. Vignette Specifications

### Vignette A: `queue-lifecycle`
- **Focus**: Standard show operational lifecycle with mixed actor roles.
- **Actors**:
  1. `host` — Host (KJ Desktop viewport, 1440x900, authenticated)
  2. `stage` — Stage TV Display (1280x720, unauthenticated)
  3. `singer-auth` — Singer 1 (Logged-In Mobile viewport, 390x844)
  4. `singer-guest-1` — Singer 2 (Guest Mobile viewport, 390x844)
  5. `singer-guest-2` — Singer 3 (Guest Mobile viewport, 390x844)
- **Beats**:
  1. `show-created`: Host launches temporary show with manual staging approval.
  2. `singers-joined`: Mobile singers and Stage TV load `/e/<code>` and `/stage/event/<id>`.
  3. `requests-in-staging`: All 3 singers submit songs; Host inspects Staging Inbox.
  4. `after-triage`: Host accepts 2 singers, declines 1 with reason ("Already sung recently — pick another!").
  5. `called-to-stage`: Host calls Singer 1 to stage; Singer 1 sees CALLED TO STAGE alert; Singer 2 sees On Deck.
  6. `announcement-live`: Host broadcasts live announcement shout-out banner.
  7. `show-paused`: Host pauses show; mobile singers verify disabled submission state.
  8. `rotation-advanced`: Host resumes show, marks Singer 1 Done, calls Singer 2 to stage.
  9. `host-history`: Host inspects History tab for completed and declined badges.
  10. `show-ended`: Host ends show via Show Settings modal with confirmation.

### Vignette B: `favorites-and-edge-cases`
- **Focus**: Account-synced cross-device favorites, custom write-ins, and singer self-cancellation.
- **Actors**:
  1. `host` — Host (KJ Desktop viewport, 1440x900, authenticated)
  2. `singer-auth-a` — Singer 1 Device A (Mobile viewport, 390x844, authenticated)
  3. `singer-auth-b` — Singer 1 Device B (Mobile viewport, 390x844, fresh isolated context)
  4. `singer-guest` — Singer 2 Guest (Mobile viewport, 390x844, unauthenticated)
- **Beats**:
  1. `show-created-with-write-ins`: Host creates show with `allow_write_ins: true` and manual staging approval.
  2. `singer-device-a-favorites`: Singer 1 (Device A) joins show and favorites a track from the catalog.
  3. `device-b-cross-device-sync`: Singer 1 logs into a brand new isolated Device B and verifies the favorited track is present.
  4. `device-a-logout-isolation`: Singer 1 logs out on Device A; verifies `localStorage` favorites are wiped for shared-device security.
  5. `custom-write-in-submitted`: Singer 2 (Guest) searches for an uncataloged song and submits a custom write-in request.
  6. `host-sees-write-in`: Host verifies the write-in appears in Staging Inbox with write-in badge.
  7. `singer-self-cancellation`: Singer 2 opens mobile Queue drawer and cancels their pending request.
  8. `host-verified-cancellation`: Host verifies the cancelled request is cleared from Staging and logged in History.

---

### Vignette D: `fair-priority-rotation`

- Two singers perform and then request again in reverse order. The singer whose performance was less recent moves ahead.
- A newcomer enters fourth while the three protected positions stay fixed. The host queue, Stage TV, and the newcomer's queue position agree.
- A second newcomer stays behind the first when their priorities tie.
- The host assigns a positive private weight to a later newcomer, who moves ahead of both. The public queue omits the weights.

### Vignette E: `kiosk-patron-session`

- A patron searches the catalog, submits a request, sees the success splash, and can clear the screen for the next singer.
- A second patron leaves a search and request form open; after 45 seconds without input, both are cleared.
- The configured host PIN exits kiosk mode.

### Vignette F: `round-robin-and-admission`

- Two singers submit in round one, then submit again; their repeat requests are placed in round two in submission order.
- Separate shows assert `409 max_active_limit` and `429 submission_cooldown`; rejected requests do not appear in the public queue.

### Vignette G: `host-reorder-and-reconnection`

- The host moves Casey ahead of Blake while Avery remains called to stage; the public queue and Stage view reflect the new order.
- Blake goes offline during the reorder, reconnects, and receives the new queue position through visibility-triggered full synchronization.
- The show ends and is restarted without clearing; the queue and Blake's position remain.

### Vignette H: `songbook-catalog-management`

- The owner creates a songbook, renames it, assigns a public slug, and changes its visibility.
- The owner adds two tracks, edits one, deletes the other, and verifies the CSV export matches the managed catalog.
- The owner creates and then cancels a pending collaborator invitation with catalog-edit and hosting permissions.

### Vignette I: `songbook-import-management`

- The owner reviews a locally generated CSV and imports three songs through the Assistant Import UI and background workers.
- A second additive import adds one track while preserving all three existing tracks.
- A smaller replacement prompts for confirmation. Cancel does not dispatch an upload; confirmation publishes the replacement catalog.

### Vignette J: `account-privacy`

- A new local-only host creates a temporary book and show and downloads its JSON account export.
- The host confirms account deletion; a fresh sign-in fails and the show is no longer publicly available.

### Manual Vignette: Songbook Collaboration

**Status:** Not yet executed. This is manual by choice; do not build a mail-capture fixture for it.

**Setup:** Use an isolated local backend with its own development log sender and
two dedicated, email-verified host accounts in separate browser sessions. Use a
new private songbook owned by the first host. Retrieve only these accounts'
verification and invitation links from that backend's own logs; do not inspect
shared logs or include links, tokens, or credentials in screenshots or evidence.
Do not use production resources or reset local data.

1. As owner, open **Manage Access** and invite the second host with both **Edit
   catalog** and **Host shows**. In the collaborator session, open the invitation
   link, verify the displayed book and permissions, and accept it. The book
   appears as shared; the owner sees the collaborator under accepted members.
2. As collaborator, open **Manage Songs**, add a temporary track, and confirm
   it appears to the owner. Confirm the collaborator can start a show with the
   shared book, but cannot manage its access or owner-only settings.
3. As owner, remove **Edit catalog** while retaining **Host shows**. Refresh the
   collaborator's book list: catalog editing is unavailable, while hosting remains
   available. With the collaborator's authenticated session, send a valid
   `POST /api/v1/songbooks/{id}/songs` request for the test book and verify
   `403`; UI visibility alone does not establish authorization.
4. As owner, remove the collaborator and confirm the book is no longer listed
   for that host and a new catalog write is denied. Any already active or paused
   collaborator show may still finish; do not assert that revocation ends it.
   Delete only the temporary show, songbook, and dedicated accounts created for
   this vignette. Record outcomes without secrets.

This manual flow illustrates the user experience; router and DynamoDB tests
remain the primary evidence for authorization and commit-time revocation races.

## 4. Remaining Gaps & Roadmap

- **Evidence boundary (VB1):** Browser vignettes cover observable cross-boundary user journeys such as replacement-import confirmation and catalog results, reconnect/recovery, and isolated account export/deletion and show restart. Invitation acceptance and permission changes are manual. Prioritize user journeys over testing internals through the UI.
- Deterministic backend and real DynamoDB tests remain the primary evidence for concurrent admission, transaction conflicts, migrations, and publication fencing. Browser scenarios can illustrate outcomes but cannot prove those guarantees. Add browser coverage for configurable show policies and catalog comparison when implemented, not before.
- The new browser assertions cover the previously listed kiosk, multi-round Round Robin, admission limit/cooldown, host reorder, and reconnect scenarios against local services. They do not prove concurrency/transaction guarantees; those remain covered by backend tests in C02/C03.
- The collaboration manual vignette has not been run. Songbook vignettes do not cover all replacement/pruning paths or catalog recovery failures. Account deletion covers one successful cascade, not partial failures or recovery.
- C09 still needs targeted checks for unchanged polling responses, long backoff, stale event results, and cleanup across every view lifecycle.
- C11's configurable show-policy acceptance scenarios remain out of scope because that policy is not implemented.
