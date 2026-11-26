# MicUp.live

A professional management platform for karaoke providers to share their songbooks and manage live events. 

MicUp.live solves the "binder problem" for local KJs by turning messy local media folders into a beautiful, searchable digital catalog that singers can access instantly on their phones.

## Documentation Index

| Document | Purpose |
| :--- | :--- |
| [**Concepts**](./docs/CONCEPTS.md) | Durable product vocabulary, actor boundaries, and design intent for refactors. |
| [**Refactor Contracts**](./docs/CONTRACTS.md) | Stable behavioral requirements, evidence map, known gaps, and refactor checklist. |
| 🏛️ [**Architecture & System Design**](./ARCHITECTURE.md) | Static-first serverless BFF pattern, DynamoDB schema, S3 compiler, and security model. |
| 💻 [**Local Production Simulation**](./docs/LOCAL_PRODUCTION_SIMULATION.md) | Zero-cost offline AWS simulation with DynamoDB Local, LocalStack, and SAM Lambda emulation. |
| 📋 [**Tasks & Roadmap**](./TASKS.md) | Active priorities, open roadmap items, and explicit non-goals. |
| 🚨 [**Production Runbook**](./RUNBOOK.md) | Deployment, logs, incident recovery, and release rollbacks. |
| 🤖 [**Agent & Contributor Guide**](./AGENTS.md) | Development workflows, mise task commands, code style, and test conventions. |
| 🎨 [**Branding & Design System Guide**](./docs/BRANDING_GUIDE.md) | Brand essence, "Stage Obsidian & Neon Spotlight" design system, logo anatomy, typography, and copywriting guidelines. |

## The Vision: "Easy Sharing & Live Stage Control"

1.  **For the KJ:** An Assistant that scans local media folders and imports software catalogs (VirtualDJ `database.xml`, PCDJ, SongbookDB, OpenKJ, MTU Hoster) with deduplication, soft-pruning, and cloud sync in seconds.
2.  **For the Live Show:** Decoupled events with custom Show Codes (e.g. `OMALLEY`), live queue rotation, bump reordering, TV Stage displays with QR codes, and performance history tracking.
3.  **For the Singer:** Fast, zero-app mobile catalog search, universal favorites and pitch memory (`-3♭` to `+3♯`) that follow singers across every show, collapsible artist accordions, background web notifications ("On Deck" and "Up Next"), and screen wake lock.

## Tech Stack (Serverless & Static-First)

| Component | Technology | Role |
|-----------|------------|------|
| **Frontend** | Vite + Preact | Static SPA hosted via CloudFront & S3 for sub-second global delivery. |
| **Backend (API)** | Go 1.26 (ARM64) | High-performance API for event management, queues, and metadata sync. |
| **Database** | DynamoDB | Cost-effective on-demand cloud storage (DynamoDB Local for development). |
| **Search & Virtualization** | MiniSearch + TanStack Virtual | Instant client-side fuzzy search and smooth scrolling over 50,000+ tracks. |
| **Singer Repertoire** | Web LocalStorage + Normalized Hash | Universal favorites and pitch memory persisted across venues with zero login wall. |
| **Alerts & Display** | Web Notifications + Audio + WakeLock | Screen wake lock and OS-level alerts when singers are on deck or up next. |

## Key Workflows

### 1. Catalog Import & Safe Sync
The KJ drags their music folder or software export (VirtualDJ `database.xml`, CSV, TSV, PCDJ Karaoki, SongbookDB, MTU Hoster) into the dashboard. The parser normalizes track metadata, tags duplicates, and performs an atomic sync with soft-pruning so manual edits are never lost.

### 2. Live Events, Multi-Host Collaboration & KJ Command Center
KJs can manage multiple shows (e.g. Tuesday at O'Malley's, Thursday at Dive Bar) sharing the same master songbook—or invite fellow verified hosts by email with scoped **Edit catalog** and **Host shows** permissions so multiple KJs can run independent shows simultaneously from one shared (even private) library while the owner retains full control. Shows feature:
- 1-click launch with custom Show Codes (e.g. `OMALLEY`)
- Fullscreen TV Stage view (`/stage/event/:id`) with large QR codes and live ticker announcements
- Walk-up Kiosk mode (`/kiosk/event/:id`) with inactivity reset timeouts
- Request controls: pause/resume, bump up/down, up-next promotion, and performance history
- Multi-mode queue rotation: Fair Priority (turn-weighted smart weave with 3-song stage lock buffer), Classic Round Robin (singer song stacking with round tiers), and First-Come, First-Served

### 3. Universal Singer Repertoire & Pitch Memory
Singers scan the stage QR code or enter the Show Code without downloading an app or creating an account.
- Starred songs and artists follow singers to every MicUp show using normalized `(artist, title)` keys.
- Pitch transposition choices (e.g. `-1♭`) are remembered forever per song.
- Multi-mode songbook lets singers browse flat (`🎵 By Song`), grouped (`🎤 By Artist` with song counts), or personal repertoire (`⭐ Favorites`).
- Browser notifications alert singers when they are "On Deck" (1 song away) and "Up Next on Stage".
- Performance history logs "Songs Sung Tonight" with a 1-click "Sing Again" re-request button.

## Development Setup

### Prerequisites
- [Go 1.26+](https://go.dev/)
- [Node.js 22](https://nodejs.org/)
- [Python 3.14](https://www.python.org/)
- [mise](https://mise.jdx.dev/) for tool versions, the Python virtualenv, and project tasks

### Running Locally
```bash
# Install project tools and Python tooling dependencies
mise install
mise run python:install

# Start the full local AWS simulation and the backend API on :8080
mise run local:up
mise run dev

# Start the Frontend (UI on :3000)
mise run web:dev
```

Mise creates and activates the ignored `.venv` for `mise run` and `mise exec`.
OpenAPI and fitness tasks install the pinned Python dependencies automatically.

The backend uses DynamoDB in every environment and defaults to DynamoDB Local at `http://127.0.0.1:8000` for development. LocalStack supplies local S3, SQS, and classic SES APIs, while AWS SAM runs the Lambda entrypoints and event envelopes in Docker. Application email uses the log transport locally because LocalStack Community does not emulate SESv2. All emulator ports are loopback-only and use dummy credentials; no AWS account or cloud resources are required. See [Local production simulation](docs/LOCAL_PRODUCTION_SIMULATION.md).

### Database Tests

Run `mise run test` (or `mise run ci:verify` for the full local verification suite). Backend tests run in parallel shards (`test:packages` and `test:dynamo:1..4`), each backed by an ephemeral in-memory DynamoDB Local container that cleans up automatically on exit without touching the persistent development tables. Set `DYNAMO_USE_EXISTING=1` to target `DYNAMO_ENDPOINT` directly.

## License

Licensed under the Functional Source License, Version 1.1, Apache 2.0 Future License ([`FSL-1.1-Apache-2.0`](./LICENSE)) — converts to Apache 2.0 two years after each version's release date. See [`LICENSE`](./LICENSE) for full terms.
