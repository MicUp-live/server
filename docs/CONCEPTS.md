# MicUp.live concepts

This is the product and domain model to preserve when replacing implementation.
Read [CONTRACTS.md](CONTRACTS.md) next for observable guarantees and verification.
These documents distinguish the existing product from explicitly accepted future
policies. They are not authorization for a rewrite or implementation of roadmap
features. Baseline: repository inspection, 2026-10-10;
no production behavior was verified for this baseline.

## Purpose

MicUp helps karaoke hosts share their libraries and run live shows. Singers can
find a song, request it, and follow their place from a browser without creating
an account. Hosts retain control of admission, rotation, and stage decisions.
The cloud coordinates the show; it does not play the host's media files.

Success means a host can trust the displayed queue, a singer can trust their
request and stage call, and an import cannot casually destroy the host's work.
A faster or simpler implementation that loses these properties is not an
equivalent refactor.

## Actors and authority

| Actor | Needs | Authority boundary |
| --- | --- | --- |
| Singer | Discover a show, browse, request, cancel their request, remember favorites and pitch | Public access is not host authority; a cancellation capability applies to its request |
| Host / KJ | Own libraries, configure shows, admit and order singers, manage credentials | Authentication, host eligibility, resource ownership, and credential scopes are distinct checks |
| Kiosk patron | Use a shared device for singer actions | A shared browser must reset patron state; kiosk PIN verification does not confer general host authority |
| Stage viewer | See current show and upcoming singers | Public projection, without host secrets or local file paths |
| Developer client | Synchronize a desktop hoster with the cloud | Owner-bound, scoped, expiring/revocable developer credential |
| OpenKJ client | Use its existing integration protocol | Integration credentials and adapter-specific rules; a public join code is not a destructive credential |
| Worker / operator | Import, compile, compact, clean up, recover | Background execution must preserve the same ownership and consistency boundaries |

## Vocabulary and identity

| Concept | Meaning and distinction |
| --- | --- |
| Account | Host identity and credentials; distinct from a guest's singer name |
| Songbook | A host-owned library reusable across multiple events; not a show or queue |
| Songbook grant | Owner-managed collaborator permissions (`can_edit_catalog`, `can_host_shows`) on a songbook; never grants book ownership, access management, or visibility over another host's event queues |
| Detached show | An ended show whose original songbook was deleted by its owner; retains event and queue history under its host's retention policy and requires selecting an authorized replacement songbook before restarting |
| Track | A host's available rendition of a song, possibly with brand/disc and private file location |
| Canonical song | Shared musical identity used to link renditions across libraries; does not prove a backing-track provider |
| Catalog version | Immutable compiled catalog assets; distinct from the songbook's mutable active-version pointer |
| Override | A pending addition, edit, or removal layered over a published catalog until compilation incorporates it |
| Import job | Durable work and its progress, independent of the browser session and queue message delivery |
| Event / show | One live context: owner, library, join code, lifecycle, settings, queue, and history |
| Request | A singer's request to perform a song at an event; not a media transport command |
| Singer identity within a show | Normalized entered name for rotation/admission accounting; not an authenticated global person |
| Singer slot | Storage claim enforcing an active-request limit; not an ordered queue position |
| Queue version | Event-scoped change token for synchronization and conflict detection; not a song position or catalog version |
| Round | A rotation tier; separate from rank, lifecycle status, and wall-clock timestamps |
| Stage buffer | First three live positions (on stage, on deck, in the hole); these labels are not extra request statuses |
| Repertoire | Browser-persisted favorites and pitch preferences with cross-library song identity; not server account data |

Changing IDs, normalization, browser storage keys, or catalog formats can destroy
continuity even when Go types and HTTP routes still compile. Treat those changes
as migrations with compatibility evidence.

## Independent policies

A show can be `active`, `paused`, or `ended`. Pausing intake is distinct from
clearing a queue. Restart can involve resumable archival and reopening; it is
not equivalent to deleting and recreating the event.

Request statuses are `pending`, `accepted`, `up_next`, `completed`, `rejected`,
and `cancelled`. Pending requests consume admission capacity but are not live
queue positions. Accepted and up-next requests participate in live placement;
completed requests contribute performance history. Do not infer unrestricted
transitions merely because a value appears in the enum.

Keep these policies separate:

- **Intake:** whether requests are accepted now, cooldown, per-singer limits,
  and whether a submission needs host review.
- **Rotation:** round robin stacks a singer's requests into rounds; fair priority
  uses singer turns and completed history; first-come-first-served orders arrivals.
  Preserve supported `manual` compatibility behavior too.
- **Stage protection:** automatic insertion and mode transitions preserve the
  first three live positions. Explicit host overrides have their own rules.
- **Playback:** loading, playing, paused playback, temporary skip, and device
  failures belong to the desktop player. A locally playing/paused item remains
  cloud `up_next`; `playing` is a compatibility input alias, not a new cloud state.

## Accepted configurable hosting policies

Product decision accepted 2026-10-10. These are intended defaults for future
hosting-policy work, not a claim that the current application implements them.
See C11 in [CONTRACTS.md](CONTRACTS.md) and HP1 in [TASKS.md](../TASKS.md).

Hosts configure policies per event. Keep sensible defaults so creating a show
requires no policy questionnaire; place alternatives in advanced show settings.
The following labels describe behavior, not new API fields or enum values.

| Policy | Accepted default | Configurable alternative / explicit host action |
| --- | --- | --- |
| Empty singer queue | Retain participation for the night; skip until a song is ready | Remove from rotation when empty; subsequent participation follows the selected rotation's newcomer rules |
| Singer changes next song | Allow before being called; preserve the request's place | Require host action for all replacements; once called, replacement always requires host action |
| Absent singer | Host may defer without recording a completed performance | Host chooses later placement or removes the participant; never infer completion from absence |
| Duet turn accounting | Host selects whose turn the performance consumes | A show may default to the submitting participant or all explicitly linked participants, with an explicit host override |
| Stage protection | Automatic placement preserves the first three live positions; explicit host override allowed | Strict protection can disable ordinary host reorder overrides; deliberate defer/remove actions remain available |
| Closing intake | Stop new submissions while the existing queue can finish | Host may reopen intake or separately end the show; closing never implicitly clears or completes requests |

Some of these are actions rather than switches. Do not invent configuration just
to merge distinct actions such as close, end, remove, and complete. In FCFS/manual
modes, retained participation does not create a reserved queue position; the
selected rotation determines placement. Retained membership must not stall the
show when no song is ready or automatically displace already-called singers.

Participant identity must be separate from display name in the future model:
two people named Alex can be distinct, renaming does not change whose turn it is,
and one shared device need not mean one singer. This is a correctness rule, not
a host option, and does not require singer accounts. The current implementation
uses normalized names; migration and legacy-client mapping are required before
claiming the new identity contract is met. A duet is not inferred by parsing a
name such as “Alex & Sam.”

Configuration never disables tenant isolation, request ownership checks, honest
connection/submission status, atomic queue/version changes, or recoverable
failure behavior. A cancellation capability does not automatically authorize
song replacement. Changes to policy are host-authorized, explicit, and visible
to affected clients; they must not silently rewrite earlier performances.

## Library and catalog lifecycle

Hosts supply metadata from local folders or software exports. Normalization,
deduplication, and reference matching help them organize it; unmatched/custom
tracks remain valid. A canonical song match does not justify guessing a provider.

A songbook presents a published base plus pending overrides. Compilation creates
immutable assets before publishing the active pointer. Multiple songbooks can
reference shared content, so deleting one owner or an apparently unused version
must not break another live reference.

Sync intent matters: additive import, replacement/pruning, explicit delete, and
manual edit are different actions. Preserve each supported path's merge and
availability semantics. Do not generalize the older “soft-pruning” description
into a promise that every S3 replacement retains every absent track; see the
merge/prune tests referenced in CONTRACTS.md.

## Product constraints and replaceable choices

Preserve zero-account singer access, public/private data separation, existing
integration compatibility, and affordable static catalog browsing. Current
project constraints are a modular monolith, DynamoDB operational state, immutable
S3/CDN catalogs, client-side search, and polling suitable for venue networks.
Changing these is an architecture decision, not incidental refactoring. See
[TASKS.md](../TASKS.md), including its explicit architectural non-goals.

Package names, component decomposition, helper functions, and internal algorithms
can change when behavior and operational constraints remain intact. A new model
is an opportunity to improve implementation, not evidence that product rules,
wire protocols, persisted data, or recovery procedures should change.

## What is not an existing promise

Roadmap features such as write-ins, track ratings, paid tiers, and richer singer
profiles are not acceptance criteria for an equivalent rewrite. Browser-local
favorites do not imply account synchronization or cross-device persistence.
Known failures are tracked in TASKS.md; neither this model nor a historical
checked box proves that an invariant is currently enforced.
