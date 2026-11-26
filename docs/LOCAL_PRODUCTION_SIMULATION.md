# Local production simulation

This directory describes a local-only simulation of the AWS runtime. It does
not add resources to AWS and must not be passed to `sam deploy`. DynamoDB Local
provides the operational database, while the local AWS emulator configured by
`docker-compose.yml` provides S3, SQS, and classic SES-compatible endpoints.
The API and workers use the same entrypoints and Amazon Linux 2023 custom
runtime contract as production.

## Start the complete simulation

From `server/`:

```bash
mise install
mise run local:up
mise run local:verify
docker compose ps
```

`local:up` starts both containers, waits for their health checks, lets LocalStack
create the bucket, queues, DLQs, redrive policies, and SES test identity, then
creates the DynamoDB tables through the application's own schema code. All
published ports are bound to loopback. The compose services are joined to the
`micup-local` Docker network.
SAM's Lambda containers must join that network so the service DNS names in
`local/sam-template.yaml` resolve. The host process still uses `127.0.0.1`.
If you invoke SAM without the compose network, copy `local/sam-env.json` and
replace `dynamodb`/`localstack` with `host.docker.internal` (or the host
gateway address on Linux).

## Build and invoke the Lambda artifacts

Build both the production ARM64 packages and the workstation-native x86_64
`bootstrap` files used by SAM:

```bash
mise run build:all
mise run build:lambda-local
```

Validate the local SAM description (validation is static and does not deploy):

```bash
mise run local:sam:validate
```

Invoke the API Gateway HTTP API v2 adapter with a production-shaped event:

```bash
AWS_ACCESS_KEY_ID=local AWS_SECRET_ACCESS_KEY=local mise exec -- sam local invoke Api \
  --template-file local/sam-template.yaml \
  --docker-network micup-local \
  --env-vars local/sam-env.json \
  --event local/events/api-gateway-v2.json
```

For a browser-style local API, use the catch-all HTTP API route:

```bash
mise run local:lambda:api
```

The task is equivalent to starting SAM with the compose network and warm
containers. Once it is ready:

```bash
curl -i http://127.0.0.1:9000/api/v1/healthz
```

The lower-level command is:

```bash
AWS_ACCESS_KEY_ID=local AWS_SECRET_ACCESS_KEY=local mise exec -- sam local start-api --template-file local/sam-template.yaml \
  --docker-network micup-local --env-vars local/sam-env.json --port 9000
```

Invoke the worker handlers directly with representative AWS event payloads:

```bash
mise run local:lambda:catalog
mise run local:lambda:import
mise run local:lambda:compact
mise run local:lambda:gc
```

The catalog and import fixtures contain placeholder IDs. Seed the matching
songbook/import-job records and manifest object before expecting those two
worker invocations to complete successfully. The API, compaction, and dry-run
GC fixtures work against a freshly bootstrapped stack.

The tasks wrap these explicit commands when a custom fixture is needed:

```bash
AWS_ACCESS_KEY_ID=local AWS_SECRET_ACCESS_KEY=local mise exec -- sam local invoke CatalogWorker --template-file local/sam-template.yaml \
  --docker-network micup-local --env-vars local/sam-env.json \
  --event local/events/catalog-sqs.json
AWS_ACCESS_KEY_ID=local AWS_SECRET_ACCESS_KEY=local mise exec -- sam local invoke ImportWorker --template-file local/sam-template.yaml \
  --docker-network micup-local --env-vars local/sam-env.json \
  --event local/events/import-sqs.json
AWS_ACCESS_KEY_ID=local AWS_SECRET_ACCESS_KEY=local mise exec -- sam local invoke CompactionDispatcher --template-file local/sam-template.yaml \
  --docker-network micup-local --env-vars local/sam-env.json \
  --event local/events/eventbridge-scheduled.json
AWS_ACCESS_KEY_ID=local AWS_SECRET_ACCESS_KEY=local mise exec -- sam local invoke CatalogGC --template-file local/sam-template.yaml \
  --docker-network micup-local --env-vars local/sam-env.json \
  --event local/events/catalog-gc.json
```

The SQS and EventBridge declarations in the template document the production
event sources. `sam local invoke` feeds a fixture directly; it does not create
an AWS event-source mapping or poll a queue. For queue-delivery tests, send a
message to the local SQS endpoint and invoke the worker with a captured or
fixture SQS envelope. This keeps the workflow deterministic and free of AWS
charges.

## Production parity boundaries

The simulation intentionally matches configuration and event contracts while
remaining inexpensive:

- The five Lambda functions use `provided.al2023`, `bootstrap`, and production
  memory/timeout settings. This x86_64 workstation runs x86_64 SAM artifacts;
  `mise run build:all` separately cross-compiles the same entrypoints to ARM64
  for production. CPU architecture-specific behavior remains a parity boundary.
- The local API uses a catch-all with the same HTTP API v2 payload format as
  production. Production matches explicit method/path routes before invoking
  Lambda. CloudFront caching, TLS, custom-domain behavior, throttling, and
  API Gateway authorization are also outside the local simulation.
- LocalStack S3/SQS endpoints exercise AWS SDK request signing and service calls
  without contacting AWS. LocalStack Community does not implement SESv2, so
  `EMAIL_TRANSPORT=log` keeps application mail local; classic SES remains
  available for manual CLI tests. IAM policy enforcement, SES sandbox and
  deliverability, S3 OAC/public-access policy, and CloudWatch metrics/logs are
  not reproduced.
- DynamoDB Local exercises table/index/transaction behavior and persists data
  in the compose volume, but it does not reproduce DynamoDB's production
  regional capacity behavior. Global secondary indexes are not a substitute
  for testing production eventual-consistency races; retain the concurrent
  integration tests for those invariants.
- Lambda cold starts, retry/backoff, SQS visibility timeout/redrive behavior,
  and EventBridge scheduling are represented by fixtures but not automatically
  exercised by `sam local invoke`. Test handlers for idempotency and repeated
  delivery explicitly.

Never use production credentials in the local env. The defaults in this
directory use the LocalStack/DynamoDB Local dummy account (`000000000000`) and
dummy credentials supplied by the local emulator.

Use `mise run local:down` to stop the stack without deleting state. The
explicitly destructive `mise run local:reset` task removes both emulator
volumes and recreates them; it never targets AWS.
