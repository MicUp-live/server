# MicUp.live Playwright Vignette Runner

Runs browser vignettes in isolated contexts (no session collisions) and generates a side-by-side visual storyboard + automated bug/pain-point report (`storyboard.html` and `findings.json`). Scenarios cover live queue sessions and host-side songbook/catalog management.

The runner exits nonzero for an aborted vignette or a high/medium finding.
Beat descriptions and screenshots are not themselves assertions. Runs create and
delete temporary shows; favorites runs may add a favorite to the test singer's account.
Use dedicated test accounts and inspect any reported cleanup failure.

## Actors (`queue-lifecycle`)
- **Host** (`1440×900` desktop, authenticated bot host)
- **Stage TV** (`1280×720` desktop, `/stage/event/<id>`)
- **Singer 1** (`390×844` @ `2x` mobile, logged-in singer)
- **Singer 2 & 3** (`390×844` @ `2x` mobile, unauthenticated guest singers)

## Automated Checks Captured on Every Beat
- Uncaught page errors (`pageerror`) and `console.error`
- Unexpected `4xx`/`5xx` API responses
- Mobile/desktop horizontal overflow (`scrollWidth > innerWidth`)
- Broken images and visible alert/error banners
- Logged-in singer screen-name prefill & polling sync behavior

## Setup
```bash
npm --prefix scripts/vignettes ci
npm --prefix scripts/vignettes test
```

The helper regression tests require Chrome/Chromium, as does the runner; set
`CHROME_PATH` if it is not in a detected location.

For a local-only run, start the emulators and app with `mise run local:up` and
`mise run dev:all`, then bootstrap dedicated test accounts with
`node scripts/vignettes/run.mjs --base-url http://localhost:3000 --bootstrap-bots`.
These commands do not reset existing local data.

### Option A: Save auth once via headed browser (recommended for prod / passkeys)
```bash
node scripts/vignettes/run.mjs --base-url https://micup.live --save-auth host
node scripts/vignettes/run.mjs --base-url https://micup.live --save-auth singer-auth
```
Auth files are stored in `scripts/vignettes/.auth/` (gitignored).
The `favorites-and-edge-cases` vignette additionally requires singer credentials
for its second, initially unauthenticated device. It searches the catalog for an
unsaved track without removing existing favorites; if all tracks are saved, it
verifies an existing favorite on Device B without changing account state.

### Option B: Pass credentials via environment variables
```bash
HOST_EMAIL=bot-host@example.com HOST_PASSWORD=... \
SINGER_AUTH_EMAIL=bot-singer@example.com SINGER_AUTH_PASSWORD=... \
node scripts/vignettes/run.mjs --base-url https://micup.live
```

## Running a Vignette
```bash
# Against live prod (headless, cleans up temporary test event automatically)
node scripts/vignettes/run.mjs --base-url https://micup.live

# Watch all browser windows live
node scripts/vignettes/run.mjs --base-url https://micup.live --headed --slow-mo 250

# Against local dev server
node scripts/vignettes/run.mjs --base-url http://localhost:3000

# Fair Priority with the host, Stage TV, and guest singers
node scripts/vignettes/run.mjs --base-url http://localhost:3000 --vignette fair-priority-rotation

# Kiosk patron session, Round Robin/admission rules, and host reorder/reconnection
node scripts/vignettes/run.mjs --base-url http://localhost:3000 --vignette kiosk-patron-session
node scripts/vignettes/run.mjs --base-url http://localhost:3000 --vignette round-robin-and-admission
node scripts/vignettes/run.mjs --base-url http://localhost:3000 --vignette host-reorder-and-reconnection

# Songbook catalog CRUD/export and worker-backed additive CSV import
node scripts/vignettes/run.mjs --base-url http://localhost:3000 --vignette songbook-catalog-management
node scripts/vignettes/run.mjs --base-url http://localhost:3000 --vignette songbook-import-management

# Local-only temporary account export and deletion
node scripts/vignettes/run.mjs --base-url http://localhost:3000 --vignette account-privacy
```

The import vignette requires the local import/catalog workers and LocalStack S3.
`scripts/local/localstack-init.sh` configures browser CORS for local app origins;
it is safe to rerun and does not reset bucket contents.
`account-privacy` creates and deletes a fresh account and associated test data;
it blocks direct non-loopback browser requests and rejects non-loopback target
URLs. A local proxy can still forward elsewhere: ensure it targets an isolated
local backend before running.

Outputs land in `scripts/vignettes/output/<timestamp>-<vignette>/` (gitignored):
- `storyboard.html` — side-by-side screenshots per beat + findings table
- `findings.json` — structured JSON report for triage
- `shots/*.png` — individual actor screenshots

Songbook invitation acceptance and permission changes are documented as a
[manual vignette](COVERAGE.md#manual-vignette-songbook-collaboration); they do
not require a Mailpit installation or automated mail-capture fixture.
