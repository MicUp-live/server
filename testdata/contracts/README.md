# Cross-Repository Contract Fixtures (`testdata/contracts/`)

Canonical HTTP wire fixtures for the Go `micup.live/server` test suite and proposed cross-repository consumption by the Rust `micup-client` (`micup-hoster`) test suite (Task **P2**, contracts **C01**, **C03**, **C04**).

## Ownership & Consumption Status

- **Owner**: `micup.live/server` owns these fixtures alongside [`docs/openapi.yaml`](../../docs/openapi.yaml).
- **Go Server Suite (Implemented & Verified)**: [`internal/app/contract_fixtures_test.go`](../../internal/app/contract_fixtures_test.go) loads these JSON files directly from `../../testdata/contracts/` and executes each exchange against the real `app.NewRouter`, developer-key authentication/scope middleware, handlers, and DynamoDB Local.
- **Rust `micup-client` Suite (Proposed, Unverified)**: The external `micup-hoster` repository is not present in this workspace, so Rust client consumption is not yet implemented or verified. When `micup-client` is wired up to close **P2**, its test suite is intended to read this same directory via `MICUP_CONTRACT_FIXTURES_DIR` (or a sibling checkout/submodule path such as `../server/testdata/contracts`) using `serde_json` without duplicating JSON payload literals across repositories.

## Fixture Inventory

| File | Endpoint(s) | Contract Behavior Covered |
| --- | --- | --- |
| `events_list_200.json` | `GET /api/v1/events` | `{"events": [...]}` envelope, empty list `{"events": []}`, active + ended events, RFC3339 timestamps, omitted optional fields, unknown future fields |
| `queue_host_requests_200.json` | `GET /api/v1/events/{id}/requests` | `200 OK` host queue snapshot, `X-Queue-Version` header, `current_round`, all 6 canonical statuses (`up_next`, `accepted`, `pending`, `completed`, `rejected`, `cancelled`), omitted optional `song_id`/`notes`/`decline_reason`, RFC3339 timestamps, unknown future fields |
| `queue_public_200.json` | `GET /api/v1/events/{id}/queue` | `200 OK` public queue snapshot, `X-Queue-Version` header, filters strictly to `up_next` and `accepted`, omits private fields (`notes`, `decline_reason`, `cancel_token`, `song_id`) |
| `queue_poll_204.json` | `GET /api/v1/events/{id}/requests?since=N`, `GET /api/v1/events/{id}/queue?since=N` | `204 No Content` with `0`-byte body and `X-Queue-Version` when `since == queue_version`; `200 OK` when `since != queue_version` |
| `request_status_mutations.json` | `PATCH /api/v1/events/{id}/requests/{rid}` | Canonical status transitions, compatibility input aliases (`playing` -> `up_next`, `declined` -> `rejected`), omitted optional `song_id`, `expected_queue_version`, `Idempotency-Key`, `X-Queue-Version` bump, unknown future request fields |
| `queue_reorder_200.json` | `POST /api/v1/events/{id}/requests/reorder` | Manual reorder with `order`, `expected_queue_version`, `Idempotency-Key`, `{"status": "ok"}`, `X-Queue-Version` bump, and persisted rank order |
| `conflicts_and_idempotency.json` | `PATCH .../requests/{rid}`, `POST .../requests/reorder` | `409` `queue_version_conflict` (`expected_queue_version`, `current_queue_version`, `queue_version`, `X-Queue-Version`), idempotent replay returning stored `200` + `X-Queue-Version` without double-incrementing, and `409` `idempotency_key_conflict` on payload mismatch |
| `developer_scope_failures_403.json` | `/api/v1/events`, `/api/v1/events/{id}/requests*`, `/api/v1/events/{id}/pause`, `/api/v1/events/{id}/resume`, `/api/v1/songbooks/{id}/sync` | `403` `insufficient_scope` (`error`, `detail`, `required_scope`) in `DEV_KEY_SCOPE_MODE=enforce` across `events:read`, `requests:read`, `requests:write`, `events:write`, and `catalogs:write` |
