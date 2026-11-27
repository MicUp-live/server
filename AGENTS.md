# AGENTS.md — MicUp.live Server

## Project context

MicUp.live helps karaoke hosts share libraries and manage live shows. Singers
browse and submit requests from a browser without a mandatory account.

The current implementation is a modular monolith: a Go BFF and background
workers, with a Vite + Preact SPA. PostgreSQL 16 (local) / Amazon Aurora DSQL
(production) holds operational state; S3/CDN assets hold immutable catalogs; SQS
carries background work. Catalog search runs in the browser, and live views
synchronize through HTTP polling. Go 1.26, Node 22, Python 3.14, and the local
environment are managed by `mise`.

These are current architecture choices. Routine maintenance follows them;
explicitly requested redesigns can revisit them with contract, migration, and
operational evidence.

## Reading and sources of truth

Read only the material relevant to the task. For refactoring, start with
[concepts](docs/CONCEPTS.md) and [contracts](docs/CONTRACTS.md), identifying the
concepts and contract IDs affected by the change.

| Source | Role |
| --- | --- |
| [CONCEPTS.md](docs/CONCEPTS.md) | Product intent, vocabulary, actor boundaries, and accepted future policies |
| [CONTRACTS.md](docs/CONTRACTS.md) | Behavioral guarantees, acceptance scenarios, evidence references, and known gaps |
| [docs/openapi.yaml](docs/openapi.yaml) | Canonical HTTP specification; published copies are generated |
| [TASKS.md](TASKS.md) | Active priorities, unresolved findings, acceptance criteria, and dated evidence |
| [ARCHITECTURE.md](ARCHITECTURE.md) | System design and implementation context |
| [RUNBOOK.md](RUNBOOK.md) | Deployment, incident response, and recovery procedures |
| [.mise.toml](.mise.toml) & [mise-tasks/](mise-tasks/) | Tool versions, environment, composite tasks, and executable file-based tasks |

Requirements, accepted future behavior, current implementation, and verified
production behavior are different things. Check historical claims against
current code and TASKS evidence. When they disagree, record the discrepancy;
do not force an obsolete finding onto changed code or silently treat a bug as
intended behavior.

## Working on a change

Start with `git status --short`, applicable directory instructions, and the
relevant task. Preserve unrelated user changes. Trace the affected operation
and its callers, including browser, developer API, OpenKJ, compatibility routes,
workers, and operator tools where applicable.

For stateful changes, give a short implementation note in the task or progress
update: the invariant, callers, commit boundary, conflict/retry behavior, failure
response, and regression scenarios. This is a working checklist. Routine
implementation choices can be resolved from existing contracts and local
patterns; ask when unresolved product or security semantics materially change
the result.

Prefer a coherent change that includes affected callers, persistence, contracts,
and tests. Incremental replacement is usually easiest to verify. Larger
replacements are appropriate when requested and supported by characterization,
compatibility, migration, and recovery evidence. Remove superseded paths or
identify their compatibility owner and removal condition.

Before completion, inspect the final diff, run `git diff --check`, and map the
applicable acceptance criteria to named tests or other recorded verification.
For non-trivial code, API, persistence, or infrastructure changes, run a
zero-context read-only subagent review of the full diff and new files before
committing or declaring completion, and fix any high/medium findings before
final verification.
Update TASKS evidence as work progresses, marking completion only when criteria
pass. Report changed behavior, commands/results, and remaining gaps. Distinguish
inspection, automated tests, and production observations. For unfinished work,
record the task ID, changed files, checks, unresolved issue, and next step.

## Ownership and implementation guidance

Current capability owners are `internal/queue`, `internal/event`,
`internal/songbook`, `internal/catalog`, `internal/account`, and `internal/auth`
(password reset). HTTP routing and
middleware live in `internal/app`; protocol adapters in `internal/handlers`;
persistence contracts in `internal/store` and PostgreSQL / Aurora DSQL
implementation in `internal/store/postgres`.
Frontend pages, components, hooks, and search workers live under `web/src`.

Transport adapters translate input, authorization, and output. Business
orchestration belongs in a testable application capability; persistence owns
atomic commit behavior. Browser, developer, and compatibility adapters should
share canonical mutation decisions. Ownership can span several cohesive
services without creating competing implementations of the same operation.

Choose package, component, and state-management boundaries that make behavior
clear and testable. Pure policy functions are useful for ordering and validation;
explicit time and other inputs make those decisions reproducible. Shared
polling behavior keeps host, singer, stage, and modal views consistent. A reducer
is useful when it clarifies related transitions, but is not required for every
multi-value workflow.

Large files, repeated mutations, hard-to-test dependencies, and components owning
several workflows are signals to consider extraction. File length and call counts
are not automatic refactoring requirements. Narrow fixes can stay narrow;
extract a coherent responsibility when it improves the change. Follow nearby
Go/Preact conventions and use the existing formatter and test frameworks.

## Correctness boundaries

These requirements remain applicable regardless of code organization:

- Authentication, host eligibility, ownership, and credential scopes are separate
  checks. Restricted credentials must not mint broader or longer-lived authority.
- Reads used for admission and placement are part of correctness. Propagate read
  failures and protect relevant state/version conditions at commit. An atomic
  write does not validate a stale read; an eventually consistent index cannot
  prove complete state for a decision requiring authoritative enumeration.
- Queue changes and their observable version commit together. Include replay
  results in that boundary where the contract promises idempotency. Alternate
  stores or test doubles must not silently weaken required guarantees.
- Conflict retries reload and recompute with a bound. Process-local locks do not
  enforce consistency across Lambda instances. Concurrency tests use controlled
  interleavings; sleeps and a passing race detector are insufficient evidence.
- Exports distinguish unavailable from empty data. Multi-step cascades and
  background work expose partial failure and retain a documented recovery path.
  If storage cannot enforce the required guarantee, explain the limitation and
  alternatives rather than substituting best-effort behavior.
- Keep participant identity, request lifecycle, rotation, stage protection, and
  playback meaning distinct. Import merge/pruning behavior follows its specific
  contract; older blanket soft-pruning descriptions do not cover every path.
- Do not weaken assertions, skip failures, raise limits, disable authorization,
  or hide storage errors to make checks pass. Intentional contract changes need
  an explanation and corresponding specification/test changes.

## Verification

Use the affected boundaries to select checks. For bugs, add a focused regression
that demonstrates the old failure through meaningful behavior. Existing passing
suites need not be repeated without new edits or unresolved evidence.

| Change | Required evidence |
| --- | --- |
| Documentation | Verify referenced paths/commands, reconcile conflicting claims, and run `git diff --check`; no application suite needed |
| Go behavior | Focused regression coverage, `mise run test` (or `mise run test:postgres` for store tests), and fitness checks |
| PostgreSQL / Aurora DSQL or concurrency | Real PostgreSQL 16 tests with `-count=1` against `DATABASE_URL` (use `mise run test:postgres` or focused `go test -count=1 ./internal/store/postgres -run ...`), including applicable conflicts, `SQLSTATE 40001` retries, `FOR UPDATE` fencing, and rollback; fakes alone do not prove persistence guarantees |
| Authentication/authorization | Tests through the real router/middleware for applicable credentials, tenant boundaries, and denied actions |
| Frontend behavior | `npm --prefix web test`, `npm --prefix web run build`, token checks for styles, and browser verification of changed interactions when available |
| API or headers | Update canonical OpenAPI, run `mise run openapi:sync` and then `mise run openapi:check`; verify adapters and applicable CORS preflight/exposed headers |
| Workers or local/cloud wiring | `mise run ci:verify` for full local integration readiness |

The fitness script enforces OpenAPI sync, design tokens, Go formatting, vet, and
the checked-in maintainability hotspot baseline (`scripts/hotspot-baseline.json`
via `scripts/check-hotspots.py`). Tracked baseline ceilings, untracked files
exceeding `default_max_lines` (600), and missing exception metadata (`owner`,
`removal_condition`) fail the check; files in the warning band (501–600 lines)
emit non-blocking advisory warnings. Do not silently raise baseline limits to
make checks pass.

## Local commands and data

Run `mise tasks ls` to list all available tasks (and `mise tasks info <task>`
to inspect dependencies and source). Common commands:

```bash
mise install
mise tasks ls                  # List all available tasks
mise run python:install        # Python dependencies in the mise-managed .venv
mise run db:up                 # Local PostgreSQL 16 (alias: mise run pg:up)
mise run local:up              # Full local emulator setup (PostgreSQL 16 + LocalStack)
mise run dev                   # Backend
mise run web:dev               # Frontend
mise run test:packages         # All Go package tests against local PostgreSQL 16
mise run test:postgres         # PostgreSQL store & backend tests only
mise run test                  # Full backend test suite (~8s against local PostgreSQL 16)
mise run test:all              # Backend + frontend test suites
mise run check:fitness
mise run openapi:check
```

### Authoring mise tasks

- **File-based tasks (`mise-tasks/`)**: Put executable leaf tasks under
  `mise-tasks/` (`chmod +x`). Subdirectory paths map to `:` namespaces (for
  example, `mise-tasks/db/migrate/production` becomes `db:migrate:production`),
  and `_default` adopts its parent directory name (`mise-tasks/dev/_default`
  becomes `dev`). Declare task metadata with `#MISE` headers (`description`,
  `depends`, `dir`, `sources`, `alias`).
- **Shell & script conventions**: For Bash tasks, start with
  `#!/usr/bin/env bash` and `set -euo pipefail`, use `exec` for leaf commands so
  signals propagate cleanly, forward `"$@"` when arguments are supported, and
  validate changes with `shellcheck` (managed in `.mise.toml`). Standalone
  Python or other interpreter scripts can live directly in `mise-tasks/` with
  their own shebang and `#MISE` headers.
- **Composite tasks (`.mise.toml`)**: Reserve `[tasks.*]` definitions in
  `.mise.toml` for tasks-of-tasks that orchestrate other tasks via `depends`,
  sequential `run = [{ task = "..." }]`, or parallel
  `run = [{ tasks = [...] }]`.

Use `mise run test` (which invokes `test:packages` and depends on `pg:up`) for
the full Go test suite against local `postgres:16-alpine` (`micup-postgres` on
`127.0.0.1:5432`), or `mise run test:postgres` for focused store tests in
`internal/store/postgres` and `internal/store/backend`.

Use `mise exec -- <command>` when the shell has not loaded repository tool
versions/environment. `internal/store/postgres` integration tests skip when
`DATABASE_URL` is unset; a passing run with skipped integration tests is not
persistence evidence. Local PostgreSQL 16 is persistent and loopback-only
(`127.0.0.1:5432`); S3/SQS/SES workflows also use LocalStack (`127.0.0.1:4566`).
See [local simulation](docs/LOCAL_PRODUCTION_SIMULATION.md).

If a test stalls, diagnose connectivity and use a bounded `-timeout` on a focused
test before launching another full suite. Report blocked checks honestly. Do not
use `local:reset` or remove persistent volumes merely to make tests pass.
