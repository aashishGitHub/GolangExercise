# ticket-booking

An event ticket-booking platform built to prove one thesis under a stampede: **a seat is never sold
twice.** Go backend, Postgres as the sole seat-state arbiter, Redis as a throughput filter (never the
arbiter), React/TypeScript frontend, and AWS infrastructure simulated locally via Docker so the whole
system runs on a laptop with no cloud account.

Start with [`docs/plan.md`](docs/plan.md) for the full design — locked decisions, the correctness
core, API contract, and the phased build plan. [`MILESTONES.md`](MILESTONES.md) is the build log: every
phase, checked off only once verified against this real running stack, with real command output pasted
in, not "should work." If you want the short version of what's actually been built and proven, read
`MILESTONES.md` first — this README only gets you running.

## Requirements

| Tool | Why | Check |
|---|---|---|
| Go 1.26+ | backend | `go version` |
| Docker + Docker Compose | the whole local-AWS emulator stack | `docker compose version` |
| Node 24+ | frontend | `node -v` |
| [`golang-migrate`](https://github.com/golang-migrate/migrate) | SQL migrations | `migrate -version` |
| [`sqlc`](https://sqlc.dev) | regenerating `internal/db` from `sqlc/queries.sql` (only if you change queries) | `sqlc version` |
| AWS CLI v2 | talking to cognito-local / moto-server / dynamodb-local | `aws --version` |
| `zip` | only for `make build-lambda` | `zip -v` |
| Terraform 1.5+ | only for `make tf-validate` (never `apply` — see below) | `terraform version` |

Everything above except Go/Docker/Node is only needed for specific workflows (migrations, lambda
builds, terraform) — you don't need `sqlc` or Terraform installed just to run the server.

## Quickstart — get a real seat map holding a real seat in under 5 minutes

```bash
cd ticket-booking

# 1. Local-AWS emulator stack: Postgres, Redis, cognito-local, MinIO, moto-server
docker compose up -d
docker compose ps    # everything should say "Up" / "healthy" within ~15s

# 2. Apply migrations
make migrate

# 3. Env file — config.Load()'s local-only settings (S3 endpoint/keys, etc.)
#    default to EMPTY, not localhost, by design (see internal/config's own
#    doc comment) — so anything other than `make dev-server` (which sources
#    this automatically) needs it exported into the shell first.
cp .env.example .env
set -a && source .env && set +a

# 4. Seed a Cognito user pool + app client in cognito-local, write the IDs
#    into .env.local (also read by the frontend via VITE_ prefixes)
bash scripts/seed-cognito-local.sh
source .env.local

# 5. Seed a venue (30,000 seats: 10 sections x 30 rows x 100 seats — override
#    with -sections/-rows/-seats-per-row for a smaller/faster local venue)
go run ./scripts/seed-venue
# -> logs: seeded venue_id=1 "Interview Arena" in "Metropolis": ...

# 6. Publish an event for that venue: builds event_seats, renders and
#    uploads layout.json + seats.bin to MinIO, flips the event ON_SALE
go run ./cmd/event-publisher -venue-id=1
# -> logs: created event_id=1 for venue_id=1 ... event_id=1 is now ON_SALE

# 7. Run the API server (make dev-server sources .env/.env.local itself,
#    so this step alone is fine from a fresh shell too)
make dev-server
# -> ticketing server listening on :8080
```

In another terminal, confirm it's alive and serving real seeded data:

```bash
curl localhost:8080/health
curl "localhost:8080/api/v1/events?limit=1"
```

### Running the frontend

```bash
cd web
npm install
npm run dev
# -> http://localhost:5173
```

The frontend reads Cognito config from the same `.env.local` step 4 wrote (Vite's `envDir` points at
the repo root, not `web/`, specifically so one seed run configures both sides).

### Getting a real auth token to call authenticated endpoints

Most of the interesting routes (`POST .../holds`, `POST /orders`, `GET /whoami`) require a real
Cognito ID token. `scripts/seed-cognito-local.sh` only creates the user *pool*; create a test user and
get a token like this (swap in your own pool/client IDs from `.env.local`, or read them back with
`grep COGNITO .env.local`):

```bash
export AWS_ACCESS_KEY_ID=local AWS_SECRET_ACCESS_KEY=local AWS_DEFAULT_REGION=us-east-1
POOL_ID=<COGNITO_USER_POOL_ID from .env.local>
CLIENT_ID=<COGNITO_CLIENT_ID from .env.local>

aws --endpoint-url http://localhost:9229 cognito-idp admin-create-user \
  --user-pool-id "$POOL_ID" --username you@example.com \
  --user-attributes Name=email,Value=you@example.com --message-action SUPPRESS

aws --endpoint-url http://localhost:9229 cognito-idp admin-set-user-password \
  --user-pool-id "$POOL_ID" --username you@example.com --password 'Passw0rd!' --permanent

TOKEN=$(aws --endpoint-url http://localhost:9229 cognito-idp admin-initiate-auth \
  --user-pool-id "$POOL_ID" --client-id "$CLIENT_ID" \
  --auth-flow ADMIN_USER_PASSWORD_AUTH \
  --auth-parameters USERNAME=you@example.com,PASSWORD='Passw0rd!' \
  --query 'AuthenticationResult.IdToken' --output text)

curl -H "Authorization: Bearer $TOKEN" localhost:8080/api/v1/whoami
```

From there: `POST /api/v1/events/1/queue` to get an admission token (the waiting room gates
`POST .../holds` behind `X-Admission-Token` — see below), then hold a seat, then create an order.
`MILESTONES.md`'s Phase 6/8/9 sections have full worked curl sessions if you want to see the entire
hold → order → ticket flow end to end.

## What's running, and on which port

| Service | Port | Role |
|---|---|---|
| `postgres` | 5432 | the seat-state arbiter — every hold/confirm/release is a row-level CAS here |
| `redis` | 6379 | throughput filter + availability cache + waiting-room queue — **never the arbiter** |
| `cognito-local` | 9229 | real JWT issuance/verification, no fake tokens |
| `minio` | 9000 (API) / 9001 (console) | S3 stand-in — venue layouts (public) and QR tickets (private, presigned-URL-only) |
| `moto-server` | 5001 | EventBridge/SQS/Scheduler control plane (control-plane only — see the fidelity-gaps note below) |
| `dynamodb-local` | 8000 | **not started by `docker compose up -d`** — behind the `spike` profile; only used by Phase 12's `cmd/dynamo-spike`, never by the main app. Bring it up with `docker compose --profile spike up -d dynamodb-local` |
| `cmd/server` | 8080 | the API — chi router, everything under `/api/v1` plus `/ws` and `/gate/redeem` |
| `web` (Vite dev server) | 5173 | the React frontend |

## Repository layout

```
cmd/                One binary per process — see the table below
internal/           All business logic; internal/inventory is the sole writer of seat state
migrations/          golang-migrate, sequential, paired up/down
sqlc/                queries.sql -> regenerates internal/db (run `make sqlc-generate` after editing)
web/                 React 19 + Vite + TypeScript frontend
infra/terraform/     14 modules, `validate`-clean; `apply` is never run automatically (see below)
scripts/             seed-venue, seed-cognito-local.sh, load-test harnesses, a WS test client
docs/                plan.md (design), script.md / deep-dive.md (source studies),
                      dynamodb-comparison.md (Phase 12 writeup)
MILESTONES.md         the build log — what's done, how it was verified, real bugs found and fixed
```

### `cmd/*` — what each process does

| Binary | Role | Its `-lambda` twin |
|---|---|---|
| `server` | the API + WebSocket hub + in-process AIMD waiting-room controller | `server-lambda` (API Gateway) |
| `event-publisher` | one-shot: builds `event_seats` for a venue, uploads the layout, flips an event `ON_SALE` | — (operator CLI, not a service) |
| `outbox-relay` | polls `domain_events` for unpublished rows, publishes to EventBridge | `outbox-relay-lambda` |
| `hold-reaper` | actively releases expired holds (UX freshness only — expiry is correct even if this never runs) | `hold-reaper-lambda` |
| `projector` | turns `domain_events` into the Redis-backed availability bitset `/ws` serves | `projector-lambda` |
| `reconciler` | resolves stuck/ambiguous payments, checks the two money-safety invariants | `reconciler-lambda` |
| `saga-worker` | catch-up sweep for orders whose saga a crashed process left mid-flight | `saga-worker-lambda` |
| `reminder-scheduler` | T-24h/T-2h reminder one-shots (a fake provider — logs, doesn't really send) | `reminder-scheduler-lambda` |
| `dynamo-spike` | Phase 12's standalone DynamoDB comparison — **never imported by `server`** | — |

Everything except `event-publisher` and `dynamo-spike` is designed to run continuously; run them with
`go run ./cmd/<name>` alongside `cmd/server` if you want the full system (outbox delivery, hold
reaping, realtime deltas, saga catch-up, reconciliation) live locally.

## Testing

```bash
make test              # unit tests only — no external dependencies, fast
make test-integration  # real Postgres/Redis/MinIO required — bring the stack up first
```

`test-integration` runs with `-p 1` (packages serialized) deliberately: several integration suites
scan global state (all `domain_events`, all `orders`), and Go runs different packages' test binaries
concurrently by default, which produces real cross-package flakiness against one shared local stack —
see `MILESTONES.md`'s Phase 9 section for how this was found and fixed.

The frontend has its own suites: `cd web && npm test` (vitest — model/a11y unit tests) and
`npm run e2e` (Playwright, against a real running backend + real seeded venue — start the stack and
`cmd/server` first).

## Load testing

```bash
go run ./scripts/loadtest -pool-id=<...> -client-id=<...> -event=1 \
  -workers=20 -duration=20s -contention=uniform   # or -contention=hot
```

Drives the full real browse → queue → hold → order → ticket journey concurrently over real HTTP and
prints hold p50/p95/p99, conflict rate, conversion rate, WS delta lag, saga compensation count, and
the duplicate-ticket invariant check. Numbers are dev-hardware relative baselines, not production
capacity claims — see `MILESTONES.md`'s Phase 10 section for real recorded runs (including a real
crash bug this harness found and the fix).

## Infrastructure (Terraform)

```bash
make tf-validate   # cross-compiles every cmd/*-lambda twin, then `terraform validate`
```

14 modules under `infra/terraform/modules/`, wired together in `infra/terraform/envs/local/`.
**`terraform plan`/`apply` against real AWS is a deliberate, explicitly user-triggered step — never
run automatically, by anyone, including CI.** The providers use placeholder credentials and
`skip_credentials_validation` specifically so `init`/`validate` never need real AWS access.

## Honest gaps (read this before you assume something works)

This project's house style is to document what's real and what isn't rather than let a reader assume
completeness. The short list, with full detail in `MILESTONES.md`:

- **moto-server is control-plane only** — it creates EventBridge buses/rules/schedules but never
  fires them. `outbox-relay`/`hold-reaper`/etc. are ticker loops locally; their `-lambda` twins are
  the real scheduled/event-driven versions.
- **`internal/wshub`'s realtime design doesn't map onto API Gateway WebSocket's Lambda-per-message
  model** — `modules/websocket`'s Terraform is real and `validate`-clean, but the actual
  `PostToConnection`-based push integration isn't built (Phase 11's own documented gap).
- **The resumable checkout React page** and a few frontend perf/a11y test suites were scoped down in
  Phase 4 — see that phase's section in `MILESTONES.md` for the exact list.
- Single-node Postgres/Redis locally (no failover/replication), a fake payment provider, and no real
  bot defense beyond rate limits and unguessable IDs — all deliberate, all documented, none hidden.

## Where to go next

- **Building a mental model of the system** → `docs/plan.md` (start with "Locked decisions" and "The
  correctness core")
- **What's been built and how it was proven** → `MILESTONES.md`, phase by phase
- **The DynamoDB-vs-Postgres tradeoff, with real measured numbers** → `docs/dynamodb-comparison.md`
- **The two original design studies this project reconciled** → `docs/deep-dive.md`, `docs/script.md`
