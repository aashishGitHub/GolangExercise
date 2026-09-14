# ticket-booking — Milestone Tracker

Source of truth for build order. Mirrors the phased roadmap in `docs/plan.md`. Update checkboxes as
work lands; do not reorder phases without noting why below the table.

**Design is locked** (see `docs/plan.md` — Context, Locked decisions, and the Contradictions/Gaps
fixes applied 2026-09-14): Postgres is the seat-state arbiter, Redis is a contention filter and cache
only, expiry is a SQL-predicate guarantee (never timer-dependent), fence guard is equality not `>=`,
seats are identified by dense ordinal on the wire, local emulators are the free patchwork (no
LocalStack). Terraform is written and `validate`-passing throughout; `apply` against real AWS is a
separate, explicitly user-triggered step, never run automatically.

Status legend: `[ ]` not started · `[~]` in progress · `[x]` done · `[!]` blocked (see note)

Each phase is scoped to be independently pausable/resumable: land it, verify it with real output
(pasted below, not "should work"), commit, stop. The next phase never depends on in-flight state from
the current one — only on what's already verified and committed.

---

## Phase 0 — Scaffolding
- [x] Go module (`module ticketing`, `go 1.25` — installed toolchain is 1.25.6; the sibling project
      declares `go 1.26` but that exceeds what's installed here, so this project pins to what
      actually builds rather than relying on `GOTOOLCHAIN=auto` downloading a newer one)
- [x] `internal/config`: flat `Config` struct, single `Load()`, `getenv(k, fallback)` — local-only
      settings (`S3Endpoint`, `EventBusEndpoint`, `WSManagementAPIEndpoint`, etc.) default to empty
- [x] `internal/httpapi` + `cmd/server`: chi router, `GET /health`
      — verified: `curl localhost:8080/health` → `{"status":"ok"}` (see output below)
- [x] `docker-compose.yml`: postgres:16, redis:7-alpine, cognito-local:4.0.0, minio (pinned release),
      minio-init, moto-server (host port 5001)
      — **real finding while bringing the stack up:** Docker Hub's `minio/minio` and `minio/mc` now
      access-deny anonymous pulls of pinned RELEASE tags entirely ("pull access denied... requires
      docker login" — MinIO restricted historical Docker Hub tags in 2024). Confirmed via
      `docker pull minio/minio:RELEASE...` failing, then `docker pull quay.io/minio/minio:RELEASE...`
      succeeding with the identical tag. Switched both `minio` and `minio-init` to `quay.io/minio/...`.
- [x] Makefile (`dev-server`, `dev-web`, `test`, `test-integration`, `compose-up/down`, `migrate`,
      `sqlc-generate`, `build`, `build-lambda`, `tf-validate`)
- [x] `.env.example`, `.gitignore`
- [x] `infra/terraform/envs/local`: providers (default + `us_east_1` alias), no resources yet
      — verified: `terraform init && terraform validate` clean (see output below)
- [x] `.github/workflows/ticketing-ci.yml` (repo root, path-filtered to `ticket-booking/**`,
      `working-directory: ticket-booking`, jobs `go` + `terraform-validate`; `web` and the lambda
      cross-compile check are added once those phases exist)

**Verification (real output, 2026-09-14):**
```
$ go build ./... && go vet ./... && gofmt -l .
(clean, no output)

$ docker compose up -d
 Container ticketing-redis-1 Started
 Container ticketing-moto-server-1 Started
 Container ticketing-minio-1 Started
 Container ticketing-minio-1 Healthy
 Container ticketing-postgres-1 Started
 Container ticketing-cognito-local-1 Started
 Container ticketing-minio-init-1 Started

$ docker compose ps
NAME                         STATUS
ticketing-cognito-local-1    Up (no healthcheck, per plan.md's own reasoning)
ticketing-minio-1            Up (healthy)
ticketing-moto-server-1      Up (no healthcheck)
ticketing-postgres-1         Up (healthy)
ticketing-redis-1            Up (healthy)

$ docker compose logs minio-init
minio-init-1  | Added `local` successfully.
minio-init-1  | Bucket created successfully `local/ticketing-layouts`.
minio-init-1  | Bucket created successfully `local/ticketing-tickets`.
minio-init-1  | Bucket created successfully `local/ticketing-web`.
minio-init-1  | Access permission for `local/ticketing-layouts` is set to `download`
minio-init-1  | minio-init done

$ go run ./cmd/server &   # then:
$ curl -s localhost:8080/health
{"status":"ok"}

$ docker exec ticketing-redis-1 redis-cli ping
PONG

$ docker exec ticketing-postgres-1 psql -U ticketing -d ticketing -c 'select 1'
 ?column?
----------
        1

$ curl -s localhost:9229/local_dummy/.well-known/jwks.json
{"keys":[{"kty":"RSA","e":"AQAB","use":"sig","kid":"CognitoLocal","alg":"RS256",...}]}

$ aws --endpoint-url http://localhost:5001 events list-event-buses --region us-east-1
{"EventBuses": [{"Name": "default", "Arn": "arn:aws:events:us-east-1:123456789012:event-bus/default"}]}

$ cd infra/terraform/envs/local && terraform init -backend=false && terraform validate
Terraform has been successfully initialized!
Success! The configuration is valid.
[.terraform.lock.hcl generated, committed]

$ docker compose down
[all 5 containers + network removed cleanly]
```

## Phase 1 — Auth
- [ ] `internal/auth`: JWKS fetch, RS256 verify, iss/aud/`token_use=id` checks (port from
      `offline-sync-app/internal/auth`)
- [ ] `scripts/seed-cognito-local.sh` (idempotent, port from the sibling)
- [ ] `GET /api/v1/whoami`
- [ ] 5 unit tests: valid / missing / expired / wrong-aud / wrong-token_use

## Phase 2 — Catalog + seat-map read path
- [ ] Migrations 0001 (venue layout) + 0002 (`event_seats`, with `sellable`)
- [ ] sqlc wired (`sqlc/sqlc.yaml` + `queries.sql` → `internal/db`)
- [ ] `internal/seatmap`: 2-bit bitset codec (encode/decode/diff), unit-tested in isolation
- [ ] `cmd/event-publisher`: venue→event_seats walk + layout.json/seats.bin render (see plan.md
      "Event publish pipeline")
- [ ] `scripts/seed-venue/`: 30,000-seat synthetic venue fixture, shared by backend + frontend tests
- [ ] Catalog + availability + layout + pricing endpoints (DB-scan path, no Redis yet)
- [ ] `pg_trgm` GIN index for search
- [ ] TF: `network`, `secrets`, `database` modules
- [ ] **Verify:** `EXPLAIN ANALYZE` on availability shows an Index Only Scan on
      `event_seats_cover_idx`; 30k-seat availability response ~7,500 bytes; measured p50/p95

## Phase 3 — Hold / release / confirm CAS — the keystone
- [ ] `internal/holdlock` (SET NX PX + Lua compare-and-delete)
- [ ] `internal/inventory`: Acquire / Release / Extend / Confirm / best-available
- [ ] Migration 0003 `holds_audit`
- [ ] Hold/release/best-available routes, 409 mapping
- [ ] TF: `cache` module
- [ ] **Verify — the project's thesis:** `TestAcquireHold_ExactlyOneWinnerUnderRace` (200 goroutines,
      20× repeated), `..._WithRedisFlushed`, `TestConfirm_ExactlyOneWinner`,
      `TestExpiredHoldIsReclaimed`, `TestStaleFenceRejected`, multi-seat overlap (loser's other seats
      stay AVAILABLE), gomock unit tests with committed mocks

## Phase 4 — Canvas seat map + a11y tree
- [ ] `SeatIndex`, flat quadtree (main + worker copies), LOD with incremental `sectionFreeCount`
- [ ] Canvas2D + WebGL renderers behind one interface, worker + `OffscreenCanvas`
- [ ] Hidden `role="grid"` tree, roving tabindex nav, live regions, best-available UI, Dexie cache
- [ ] **Verify:** Playwright F3/F4 flows, axe + keyboard walkthrough + name assertions + live-region
      debounce + focus-ring sync, perf tests 1–5 (frame budget, CDP long-task trace, culling, hit-test,
      DOM ceiling)

## Phase 5 — Expiry side effects: outbox + reaper
- [ ] Migration 0006 `domain_events`/`processed_events`
- [ ] `internal/events.Publish` inside every inventory transaction
- [ ] `outbox-relay` → moto EventBridge
- [ ] `hold-reaper` + `internal/scheduler`
- [ ] **Verify:** hold survives with reaper stopped yet a fresh hold still succeeds (passive expiry);
      reaper restarted flips status + outbox + relay, all pasted real `SELECT` output

## Phase 6 — Payment saga, idempotency, reconciler + checkout page
- [ ] Migrations 0004/0005 (`orders`/`payments`/`refunds`/`order_saga_steps`, `idempotency_keys`)
- [ ] Fake payment provider with injectable pathology
- [ ] `internal/order` orchestrator, `saga-worker`, `internal/reconcile`
- [ ] Order routes, resumable checkout page
- [ ] **Verify:** happy path, duplicate-idempotency-key dedup, expiry-after-capture both branches
      (reallocated / compensated+refund), ambiguous-payment reconciler resolution, both money-invariant
      queries return 0 rows

## Phase 7 — Realtime: projector + WS deltas
- [ ] `projector` → Redis bitset + `seq` + delta ring
- [ ] `internal/wshub` (10-min idle timeout, 128 KB frame cap, ~1% random disconnects — the
      fidelity patch)
- [ ] `/ws`, migration 0007 `ws_connections`
- [ ] **Verify:** real WS client receives real snapshot + delta bytes pasted, gap/resync handling,
      Redis-down degrade to DB-scan

## Phase 8 — Virtual waiting room + AIMD
- [ ] `internal/waitingroom`, queue routes, `X-Admission-Token` middleware, AIMD controller
- [ ] Migration 0009 `waiting_room_audit`
- [ ] **Verify:** 5,000 arrivals/1s, AIMD rate halving under clamped pool, token tamper/replay rejected

## Phase 9 — QR ticketing + reminders (additive)
- [ ] Migration 0008 `tickets`
- [ ] HMAC-signed QR behind a `Signer` interface, MinIO upload, short-TTL presign
- [ ] `POST /gate/redeem`, T-24h/T-2h reminder one-shots
- [ ] **Verify:** decoded real QR image bytes, tamper/redeem-twice/expired-URL all rejected correctly

## Phase 10 — Load harness + measured numbers (additive)
- [ ] `scripts/loadtest` extended: arrival ramp, contention control, full journey, CSV output
- [ ] **Verify:** one canonical scenario's real numbers pasted, double-ticket query returns 0 rows

## Phase 11 — IaC completion + Lambda twins (additive)
- [ ] Remaining TF modules (`compute`, `api`, `websocket`, `eventing`, `waf`, `observability`, gated
      `search`)
- [ ] Every `cmd/*` gets its `-lambda` twin; `make build-lambda` real (not the Phase 0 no-op)
- [ ] CI gains the lambda cross-compile check + `web` job
- [ ] **Verify:** `make tf-validate` green across all modules; `terraform plan` never run automatically

## Phase 12 — DynamoDB comparison spike (non-blocking, not dual-maintained)
- [ ] `cmd/dynamo-spike` against `amazon/dynamodb-local`
- [ ] **Verify:** same load scenario on both paths, committed comparison table + conclusion

---

## Notes / deviations
_(append dated notes here when a phase is skipped, reordered, or a decision changes)_

**2026-09-14** — Plan review found two internal contradictions and eleven gaps in `docs/plan.md`
before implementation started; all fixed in the doc (quadtree ownership, wire `UNAVAILABLE` state
source, explicit Release SQL, event-publish pipeline, Redis-down write-path degrade, Confirm's
deadlock-ordering requirement, duplicate-seat validation, contiguous best-available algorithm, max
seats per hold, reallocation's effect on `orders`, refund modeling as its own table, admission-token
vs. active-hold exemption, hold ownership check on Release). See `docs/plan.md` for the fixed text.

**2026-09-14** — `go 1.25` used instead of the sibling's `go 1.26`: the installed toolchain here is
1.25.6, and `1.26` isn't released yet at time of writing. Pinning to what's actually installed avoids
depending on `GOTOOLCHAIN=auto` reaching the network to download a newer toolchain during CI/local
dev. Revisit once 1.26 is out and installed.
