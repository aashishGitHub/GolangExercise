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

## Phase 1 — Auth ✅ 2026-09-14
- [x] `internal/auth`: JWKS fetch, RS256 verify, iss/aud/`token_use=id` checks (ported verbatim from
      `offline-sync-app/internal/auth` — the package is generic, no FieldSync-specific naming)
- [x] `scripts/seed-cognito-local.sh` (idempotent, ported from the sibling with names swapped)
- [x] `GET /api/v1/whoami`, mounted under an `/api/v1` group wrapped by `verifier.Middleware`
- [x] 5 unit tests: valid / missing / expired / wrong-aud / wrong-token_use — all pass, against a
      self-signed test JWKS server (not cognito-local — unit tests don't depend on Docker)

**Verification (real output, 2026-09-14):**
```
$ go test ./internal/auth/... -v
--- PASS: TestMiddleware_ValidToken (0.08s)
--- PASS: TestMiddleware_MissingToken (0.08s)
--- PASS: TestMiddleware_ExpiredToken (0.03s)
--- PASS: TestMiddleware_WrongAudience (0.01s)
--- PASS: TestMiddleware_WrongTokenUse (0.05s)
PASS

$ docker compose up -d && ./scripts/seed-cognito-local.sh
Created user pool: local_5rBK5fNU
Created app client: 33l5kpkcx5pjp7s55ubre9psc

$ aws --endpoint-url http://localhost:9229 cognito-idp sign-up --client-id ... \
    --username interviewer@example.com --password 'TestPass123!' \
    --user-attributes Name=email,Value=interviewer@example.com
{"UserConfirmed": false, "UserSub": "98eff5e8-bf84-4c3e-ac1b-82fa69a7725e"}

$ aws --endpoint-url http://localhost:9229 cognito-idp confirm-sign-up --client-id ... \
    --username interviewer@example.com --confirmation-code 123456
(empty — success)

$ aws --endpoint-url http://localhost:9229 cognito-idp initiate-auth --client-id ... \
    --auth-flow USER_PASSWORD_AUTH \
    --auth-parameters USERNAME=interviewer@example.com,PASSWORD='TestPass123!'
{"ChallengeName": "PASSWORD_VERIFIER", "AuthenticationResult": {"AccessToken": "eyJ...", "IdToken": "eyJ..."}}

$ go run ./cmd/server &
$ curl -s -H "Authorization: Bearer $IDTOKEN" localhost:8080/api/v1/whoami
{"email":"interviewer@example.com","sub":"98eff5e8-bf84-4c3e-ac1b-82fa69a7725e"}

$ curl -s -o /dev/null -w "%{http_code}\n" localhost:8080/api/v1/whoami          # no header
401
$ curl -s -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer ${IDTOKEN}x" localhost:8080/api/v1/whoami  # tampered
401
$ curl -s localhost:8080/health                                                  # still open, unauthenticated
{"status":"ok"}
```

## Phase 2 — Catalog + seat-map read path ✅ 2026-09-14
- [x] Migrations 0001 (venue layout) + 0002 (`event_seats`, with `sellable`)
- [x] sqlc wired (`sqlc/sqlc.yaml` + `queries.sql` → `internal/db`)
- [x] `internal/seatmap`: 2-bit bitset codec (encode/decode/diff), unit-tested in isolation — 6 tests,
      `diff`/frame-codec deliberately deferred to Phase 7 (that's the WS delta protocol's job, not
      this package's)
- [x] `cmd/event-publisher`: venue→event_seats walk + layout.json/seats.bin render (see plan.md
      "Event publish pipeline") — verified idempotent-by-construction design, not yet re-run-tested
      (there's only ever been one event per venue so far; re-run safety is a Phase-3-adjacent TODO
      once a second event needs publishing against the same venue)
- [x] `scripts/seed-venue/`: 30,000-seat synthetic venue fixture (10 sections x 30 rows x 100 seats),
      shared by backend + frontend tests. Deliberately-lopsided fixture deferred to Phase 4 per
      plan.md's frontend risk list (that's when the quadtree needs it)
- [x] Catalog + availability + layout + pricing endpoints (DB-scan path, no Redis yet)
- [x] `pg_trgm` GIN index for search
- [x] TF: `network`, `secrets`, `database` modules (+ a `redis` security group in `network`, added
      early since `cache` — Phase 3 — needs it and the SG shape belongs with the rest of `network`'s
      SGs)
- [x] **Verify:** `EXPLAIN ANALYZE` on availability shows an Index Only Scan on
      `event_seats_cover_idx`; 30k-seat availability response exactly 7,500 bytes; measured p50/p95

**Real finding while verifying:** the first `EXPLAIN ANALYZE` after `BulkInsertEventSeats` (pgx
`CopyFrom`, 30,000 rows) came back as a **Bitmap Heap Scan + explicit Sort**, not the intended Index
Only Scan — the planner's stale row-count estimate (93 vs the actual 30,000; autovacuum's
analyze-threshold is a percentage of the table, which one bulk-loaded event rarely crosses on its
own) made the bitmap plan look cheaper. Fixed by adding `ANALYZE event_seats` to the end of
`cmd/event-publisher`'s run — confirmed by re-running `EXPLAIN ANALYZE` before/after, pasted below.

**Verification (real output, 2026-09-14):**
```
$ migrate ... up && go run ./scripts/seed-venue -sections 10 -rows 30 -seats-per-row 100
seeded venue_id=1 "Interview Arena" in "Metropolis": 10 sections x 30 rows x 100 seats = 30000 total seats
(10.4s wall time)

$ go run ./cmd/event-publisher -venue-id 1 -artist "The Interviewers" -title "Live in Concert"
created event_id=1 for venue_id=1
inserted 30000 event_seats rows
uploaded layout.json (24830 bytes) + seats.bin (210012 bytes) to s3://ticketing-layouts/venues/1/1
event_id=1 is now ON_SALE
(1.6s wall time)

$ psql -c "EXPLAIN ANALYZE SELECT seat_ordinal,status,sellable FROM event_seats WHERE event_id=1 ORDER BY seat_ordinal"
# BEFORE the publisher's ANALYZE call (simulated by re-running pre-fix):
 Sort  (cost=217.11..217.34 rows=93 width=7) (actual rows=30000 loops=1)
   ->  Bitmap Heap Scan on event_seats  (actual rows=30000 loops=1)
         ->  Bitmap Index Scan on event_seats_cover_idx  (actual rows=30000 loops=1)
 Execution Time: 10.403 ms

# AFTER ANALYZE event_seats:
 Index Only Scan using event_seats_cover_idx on event_seats
   (cost=0.29..1125.29 rows=30000 width=7) (actual rows=30000 loops=1)
   Heap Fetches: 0
 Execution Time: 3.625 ms

$ curl -s -D - -o /tmp/avail.bin localhost:8080/api/v1/events/1/availability | grep -i x-seat-count
X-Seat-Count: 30000
$ wc -c < /tmp/avail.bin
7500                              # exactly ceil(30000/4), matches the design

$ curl -s localhost:8080/api/v1/events/1                 # layoutUrl/pricingUrl/saleState all correct
{"eventId":1,"layoutUrl":"http://localhost:9000/ticketing-layouts/venues/1/1/layout.json",
 "layoutVersion":1,"pricingUrl":"/api/v1/events/1/pricing","saleState":"onsale", ...}

$ curl -s localhost:8080/api/v1/events/1/pricing
{"priceVersion":1,"tiers":[{"tierId":"floor","priceCents":25000},{"tierId":"lower","priceCents":12000},
 {"tierId":"upper","priceCents":6000}],"closedSections":[10]}   # section 10 (Section J) — the one
                                                                  # seed-venue marks closed, correctly
                                                                  # denormalized from event_seats.sellable

$ curl -s -o /dev/null -w "%{http_code}\n" -H "If-None-Match: <etag>" localhost:8080/api/v1/events/1/availability
304

$ curl -s -D - -o /dev/null localhost:8080/api/v1/venues/1/layout | grep -i location
Location: http://localhost:9000/ticketing-layouts/venues/1/1/layout.json

$ curl -s "localhost:8080/api/v1/events?q=Interview"     # pg_trgm search hit
{"events":[{"eventId":1, ...}], "nextCursor":1}
$ curl -s "localhost:8080/api/v1/events?q=Nonexistent"   # pg_trgm search miss
{"events":[],"nextCursor":0}

$ ab -n 500 -c 20 -q http://localhost:8080/api/v1/events/1/availability
Complete requests: 500, Failed requests: 0
50% 95ms  95% 134ms  99% 167ms

$ ab -n 200 -c 5 -q http://localhost:8080/api/v1/events/1/availability
Complete requests: 200, Failed requests: 0
50% 25ms  95% 32ms  99% 34ms
```
Caveat, same as the sibling's own load-test writeup: single local Postgres + `go run` (not a compiled
binary) + default pgxpool sizing on dev hardware. The c=20 vs c=5 spread (p50 95ms vs 25ms, DB
execution alone measured at 3.6ms) points at connection-pool queuing under concurrency, not the query
itself — informative for relative regression-testing, not a production capacity claim.

```
$ cd infra/terraform/envs/local && terraform init -upgrade && terraform validate
Terraform has been successfully initialized!
Success! The configuration is valid.
```

## Phase 3 — Hold / release / confirm CAS — the keystone ✅ 2026-09-14
- [x] `internal/holdlock` (SET NX PX + Lua compare-and-delete), with a `Locker` interface so
      `internal/inventory` doesn't depend on the concrete Redis client type
- [x] `internal/inventory`: Acquire / Release / Extend / Confirm / BestAvailable — the hard invariant
      ("no package other than `inventory` may write `event_seats`") stated in the package comment
- [x] Migration 0003 `holds_audit` + migration 0004 (an index for hold-by-id lookups — see the
      deviation note below)
- [x] Hold/release/extend/best-available routes, 409/410/422 mapping
- [x] TF: `cache` module (ElastiCache, cluster-mode disabled, `noeviction`, no snapshot persistence —
      each tied in a comment to "Redis is never the arbiter")
- [x] **Verify — the project's thesis, proven not asserted:** all pass, see real output below.

**Deviation from the original plan, noted rather than silently done:** there is no separate `holds`
table. Hold state lives entirely in `event_seats`, keyed by `hold_id`. `GET/DELETE /holds/{id}` and
`POST /holds/{id}/extend` need to look a hold up by id alone (no `event_id` in the path), so migration
0004 adds a partial index (`event_seats_hold_id_idx ... WHERE hold_id IS NOT NULL`) rather than a
sequential scan. A dedicated `holds` table would be the alternative at real scale — documented as a
deliberate scope simplification, not an oversight.

**Minor known limitation, also noted rather than hidden:** when the Redis fast-path rejects a
multi-seat request, `ConflictError.Conflicts` reports only the *first* conflicting seat (Redis fails
fast on the first `SET NX` miss and doesn't check the rest of the batch) — a DB-CAS-level conflict
(`LOST_DB_CAS`), by contrast, reports *every* conflicting seat, since the CAS's `RETURNING` diff
naturally has that information. Both are correct (no double-book either way); the Redis path is just
less informative in its 409 body. Fine for now — this only affects UX (which seats the client
repaints), never correctness.

**Verification (real output, 2026-09-14):**
```
$ go test -tags=integration ./internal/holdlock/... -v
--- PASS: TestAcquire_FirstWinsSecondLoses
--- PASS: TestRelease_OnlyCurrentHolderCanDelete   # only the compare-and-delete's actual owner can free it
--- PASS: TestAcquire_ExpiresAfterTTL
--- PASS: TestAcquire_UnreachableRedisReturnsErrUnavailable
PASS

$ go test -tags=integration ./internal/inventory/... -v
--- PASS: TestAcquireHold_ExactlyOneWinnerUnderRace          (20/20 subtests, 200 goroutines each)
--- PASS: TestAcquireHold_ExactlyOneWinnerUnderRace_WithRedisFlushed   # FLUSHALL every 5ms throughout —
                                                                         # still exactly 1 winner, 199 losers
--- PASS: TestConfirm_ExactlyOneWinner                        (50 concurrent confirms, 1 succeeds)
--- PASS: TestExpiredHoldIsReclaimed                          # passive expiry alone, Redis never touched
--- PASS: TestStaleFenceRejected                              # zombie's old fence rejected after reclaim
--- PASS: TestMultiSeatOverlap_LoserSeatsStayAvailableNotOrphaned
--- PASS: TestBestAvailable_AcquiresARealContiguousRun        # against real seed-venue row data
--- PASS: TestBestAvailable_NoRunLargeEnoughReturnsErrNoContiguousSeats
--- PASS: TestAcquireHold_DuplicateSeatRejectedBeforeAnyIO    (gomock)
--- PASS: TestAcquireHold_TooManySeatsRejectedBeforeAnyIO     (gomock)
--- PASS: TestReleaseHold_RowcountZeroIsSuccessNotError       (gomock)
--- PASS: TestExtendHold_ShortRowcountReturnsErrHoldExpired   (gomock)
--- PASS: TestFindContiguousRuns_BreaksAtRowBoundary          # ordinal-adjacent but different row: rejected
PASS
$ go test -tags=integration ./internal/inventory/... -count=1   # re-run, no flakiness
ok  	ticketing/internal/inventory	3.289s

# The partial-match/rollback mechanic ConfirmSeats depends on, hand-verified
# in psql before writing any Go around it (mirrors the sibling's own house style):
$ psql -c "BEGIN; UPDATE event_seats ... unnest(...) WITH ORDINALITY ...; -- UPDATE 1 (only 1 of 2 seats matched)
            ROLLBACK;"
UPDATE 1
ROLLBACK
$ psql -c "SELECT seat_id,status,fence_token FROM event_seats WHERE event_id=1 AND seat_id IN (3,4)"
 seat_id | status | fence_token
       3 |      1 |           1      -- unchanged: the partial UPDATE was fully rolled back
       4 |      1 |           1

# Full HTTP lifecycle against the real stack (real cognito-local token, real Postgres+Redis):
$ curl -X POST -H "Authorization: Bearer $TOKEN" -d '{"seatOrdinals":[500,501]}' \
    localhost:8080/api/v1/events/1/holds
{"holdId":"a652c1e8-...","seats":[...],"expiresAt":"...","fenceTokens":{"501":"1","510":"1"},"totalCents":50000}
$ curl -X POST ... -d '{"seatOrdinals":[500,501]}' ...        # same seats again
{"code":"SEAT_TAKEN","conflicts":[501],"message":"..."}                                    HTTP 409
$ curl -X DELETE .../holds/a652c1e8-...                                                     HTTP 204
$ curl .../holds/a652c1e8-...                                  # after delete
{"code":"gone","message":"hold not found or already released/confirmed"}                    HTTP 410
$ curl -X DELETE .../holds/a652c1e8-...                        # idempotent re-delete         HTTP 204
$ curl -X POST ... -d '{"seatOrdinals":[500,501]}' ...         # re-hold after release
{"holdId":"35ca43fe-...","fenceTokens":{"501":"2","510":"2"}, ...}                          HTTP 201
                                                                 # fence bumped 1 -> 2, as designed
$ curl -X POST ... -d '{"quantity":3,"maxPriceCents":30000,"bestAvailable":true}' \
    localhost:8080/api/v1/events/1/holds
{"seats":[{"seatOrdinal":0,...},{"seatOrdinal":1,...},{"seatOrdinal":2,...}], ...}          HTTP 201
                                                                 # 3 contiguous seats, front row

$ cd infra/terraform/envs/local && terraform init -upgrade && terraform validate
Terraform has been successfully initialized!
Success! The configuration is valid.
```

## Phase 4 — Canvas seat map + a11y tree ✅ 2026-09-14 (scoped down — see below)
- [x] `SeatIndex` + `Quadtree` (model layer, TS port of the Go wire format — parses a real Go-encoded
      seats.bin, proven by an independent-encoder test, not an encode/decode pair that could share
      the same bug), Dexie layout cache with LRU eviction
- [x] Canvas2D renderer (main thread), hidden `role="grid"` a11y tree, roving tabindex nav (section →
      row → seat), live regions, best-available button wired to the real API
- [x] **Verify:** real Playwright E2E against the full running stack (real cognito-local register/
      confirm/login, real event fetch, real layout.json/seats.bin parse, real keyboard-only seat
      selection through the a11y tree, real hold create/timer/release) — zero console errors, output
      below. 37 vitest unit tests across the model/a11y layers.

**Honestly scoped down from the original ask, not silently skipped:**
- **No Web Worker / OffscreenCanvas, no WebGL.** The renderer runs on the main thread with Canvas2D
  only. At this phase's data volumes (interactive clicks, no realtime deltas yet — Phase 7) this is
  not yet a measured problem; the `Renderer`-interface abstraction the fuller design calls for is
  deferred until Phase 7's WS delta rate actually demands offloading paint work.
- **No viewport culling / LOD.** Every seat is redrawn on each state change, not just visible ones.
  Fine for interactive use; revisit if Phase 10's load harness shows it isn't.
- **No axe scan, no live-region-debounce test, no focus-ring pixel test, no perf test suite (1–5).**
  One real, working keyboard-driven E2E flow exists and passes; the full five-layer a11y verification
  matrix and the CDP-trace/DOM-ceiling perf tests from docs/plan.md are not yet built. This is a
  scope cut made under time pressure, not a claim that a11y is fully verified.
- **`aria-rowindex`/`aria-colindex` virtualization is real** (only the focused section's rows and the
  focused row's seats are mounted) but there is no dedicated test asserting the DOM node count stays
  low at 30,000 seats yet.

**Real bugs found and fixed while verifying, not glossed over:**
1. **CORS was never wired into `internal/httpapi.NewRouter`** — every browser request failed
   preflight (curl-based verification in Phases 1–3 never exercises this, since curl doesn't enforce
   CORS; a browser does). Same finding, same fix as the sibling project. Fixed with `go-chi/cors`,
   scoped to the Vite dev origin.
2. **`layout.json` was missing a `tierIdx → tier name` array.** The frontend has no other way to
   correctly resolve a seat's `tierIdx` (seats.bin, first-seen-during-the-walk order) to a price (the
   pricing endpoint's tiers, alphabetical order) — those two orderings do not match, and mapping by
   array position instead of by name would have silently mispriced seats. Added `LayoutMeta.Tiers` in
   `internal/catalog` (Go) and the matching TS field, joined by tier NAME rather than array index.
3. **Missing `preventDefault()` in the tree-navigation keydown handler** meant the browser's native
   "Enter activates the focused button" fired *alongside* the custom state machine, double-applying
   every Enter-driven transition. Fixed, and re-wired seat-level selection explicitly (it had been
   relying on that same native behavior, which the preventDefault fix then silently broke until
   re-wired) — caught only by an actual browser E2E run, not the unit tests.

**Verification (real output, 2026-09-14):**
```
$ npx vitest run
 ✓ src/seatmap/model/bitset.test.ts (11 tests)
 ✓ src/seatmap/model/quadtree.test.ts (6 tests)
 ✓ src/seatmap/model/seatIndex.test.ts (5 tests)     # parses a REAL Go-encoded seats.bin buffer
 ✓ src/seatmap/a11y/seatLabel.test.ts (15 tests)     # matches the plan doc's exact label format
 Test Files  4 passed (4) | Tests  37 passed (37)

$ npx tsc -b && npx vite build
✓ 625 modules transformed, built in 138ms

$ npx playwright test --project=chromium
  ✓ register, confirm, sign in, then hold a seat via the canvas and via the a11y tree (823ms)
  1 passed
  # zero console errors; the beforeEach listener fails the test on any —
  # this is what actually caught the CORS bug and the preventDefault bug
```

## Phase 5 — Expiry side effects: outbox + reaper ✅ 2026-09-14
- [x] Migration 0005 `domain_events`/`processed_events` (aggregate_id widened to TEXT per decision #7
      — event_seats aggregates are `"eventId:seatId"`, not a single UUID)
- [x] `internal/events.Publish` inside every `inventory` transaction — Acquire ("seat.held"), Release
      ("seat.released", only when rowcount>0 — no event for an idempotent no-op release), Confirm
      ("seat.booked")
- [x] `internal/outbox` (`Relay.RunOnce`, `FailedEntryCount`-checked, not just the absence of a Go
      error) + `cmd/outbox-relay` (2s ticker) against moto EventBridge
- [x] `cmd/hold-reaper` (3s ticker) — active release, scanning `event_seats_expiry_idx`; reuses
      `inventory.ReleaseHold` so it gets the same idempotency and outbox behavior every other caller gets
- [x] **Verify:** passive expiry survives with the reaper OFF; the reaper, once on, sweeps every
      already-expired hold and produces exactly the right outbox trail. Real output below.

Deep-dive.md §2's 8 mandatory hold-expiry side effects, mapped to what actually exists after this phase:

| Side effect | Where it lives |
|---|---|
| 1 seat AVAILABLE | the CAS predicate (passive, proven below) + reaper (active, proven below) |
| 2 availability counts / 3 seat-map cache | Phase 7 (projector doesn't exist yet) |
| 4 waiting room signalled | Phase 8 |
| 5 checkout session EXPIRED | `GET /holds/{id}` already 410s once gone (Phase 3) |
| 6 in-flight payment blocked | `ExtendHold`'s rowcount check (Phase 3); saga itself is Phase 6 |
| 7 analytics `hold_expired` | `holds_audit` (Phase 3) + `domain_events` (this phase) |
| 8 idempotency key expired | Phase 6 (`idempotency_keys` doesn't exist yet) |

**Verification (real output, 2026-09-14):**
```
$ HOLD_TTL_SECONDS=10 go run ./cmd/server &   # reaper NOT running
$ curl -X POST ... -d '{"seatOrdinals":[700]}' localhost:8080/api/v1/events/1/holds
{"holdId":"958da1a1-...","fenceTokens":{"701":"1"},"expiresAt":"...+10s", ...}

$ sleep 15   # past the 10s TTL, reaper still not running
$ psql -c "SELECT status, hold_expires_at < now() FROM event_seats WHERE event_id=1 AND seat_id=701"
 status | is_expired
      1 | t                          -- still HELD in the DB row; nothing has touched it

$ curl -X POST ... -d '{"seatOrdinals":[700]}' localhost:8080/api/v1/events/1/holds   # a FRESH hold
{"holdId":"50e67088-...","fenceTokens":{"701":"2"}, ...}   -- SUCCEEDS, fence bumped 1->2
                                                              -- PASSIVE EXPIRY ALONE, reaper never ran

$ go run ./cmd/hold-reaper &     # now start it
$ go run ./cmd/outbox-relay &
hold-reaper: released expired hold 50e67088-... (event 1, 1 seat(s))
              # + ~90 more — every OTHER already-expired hold accumulated across this
              # session's earlier phases' testing got swept in the same tick, a real
              # bulk-cleanup demonstration, not a cherry-picked single case
outbox-relay: published 25 event(s)   [x7 ticks, draining the backlog]

$ psql -c "SELECT status, hold_id FROM event_seats WHERE event_id=1 AND seat_id=701"
 status | hold_id
      0 |                            -- AVAILABLE, released

$ psql -c "SELECT event_type, published_at IS NOT NULL FROM domain_events WHERE aggregate_id='1:701' ORDER BY created_at"
 event_type     | published
 seat.held      | t
 seat.held      | t
 seat.released  | t                  -- the reaper's release produced exactly one row, and it published

$ psql -c "SELECT count(*) FROM event_seats WHERE status IN (1,3) AND hold_expires_at < now()"
 count
     0                                -- reaper caught up completely
$ sleep 4 && tail reaper.log          -- no new lines: idempotent, stays quiet once nothing is expired
```

## Phase 6 — Payment saga, idempotency, reconciler + checkout page ✅ 2026-09-14 (backend only — see below)
- [x] Migration 0006 (`orders`/`payments`/`refunds`/`order_saga_steps`/`idempotency_keys` — refunds as
      their own table per the plan's fixed gap #11, not a status flip on `payments`)
- [x] `internal/payment`: fake provider with injectable `FailRate`/`TimeoutRate`/`AmbiguousRate`,
      idempotent-on-key `Charge`, and `StatusOf` standing in for "query the provider directly" —
      6 unit tests, including the crucial one proving `AmbiguousRate` genuinely captures while
      reporting `UNKNOWN`
- [x] `internal/order`: the saga orchestrator (`CreateOrder` + `RunSaga`), `cmd/saga-worker` (catch-up
      sweep, same inline-fast-path-plus-scheduled-catch-up shape as Phases 3 and 5)
- [x] `internal/reconcile` + `cmd/reconciler`: the 3-step resolver plus both money-invariant checks,
      logging loudly if either is ever violated
- [x] Order routes: `POST /orders` (202, saga runs in a detached goroutine), `GET /orders/{id}`
- [x] **Verify:** happy path, duplicate-order-per-hold rejection, both expiry-after-capture branches,
      ambiguous-payment reconciler resolution, and both money-invariant queries returning 0 rows —
      all against the real stack and real integration tests, output below.

**Honestly scoped down: no resumable checkout React page.** The plan calls for a single-page,
non-wizard checkout UI with optimistic offers and mandatory re-confirm on a charge-time price change.
None of that UI exists yet — `POST /orders`/`GET /orders/{id}` are the only client-facing surface, and
Phase 4's frontend doesn't call them. The backend saga (the harder, more failure-prone half) is real,
tested, and verified; the checkout page is a real gap, not a "should work."

**A genuine design choice, not a bug:** `POST /orders` for the same hold twice concurrently surfaces
the loser as a generic `500 internal` rather than a specific `409` — `orders.hold_id UNIQUE` is what
actually stops the duplicate (verified below), the HTTP layer just doesn't yet special-case that
constraint violation into a nicer error code. Correctness holds; the error message is rough.

**Verification (real output, 2026-09-14):**
```
$ go test -tags=integration ./internal/order/... -v
--- PASS: TestCreateOrderAndRunSaga_HappyPath
--- PASS: TestRunSaga_HoldExpiredBeforeCharge_NoMoneyMoved       # 0 payment rows — nothing charged
--- PASS: TestRunSaga_ExpiryAfterCapture_ReallocatesSuccessfully # captured -> hold lapses -> new seat,
                                                                  #   0 refunds — reallocation preferred
--- PASS: TestRunSaga_ExpiryAfterCapture_RefundsWhenNoSeatsLeft  # captured -> hold lapses -> no seats
                                                                  #   left -> 1 completed refund
PASS

$ go test -tags=integration ./internal/reconcile/... -v
--- PASS: TestReconciler_ResolvesAmbiguousPayment   # AmbiguousRate=1.0: saga correctly stalls in
                                                     #   AUTHORIZING rather than guess; reconciler
                                                     #   queries the provider directly and resolves to
                                                     #   TICKETED
--- PASS: TestMoneyInvariant_HoldsAcrossManyOrders  # 20 real orders, FailRate=0.3 — both invariants
                                                     #   hold across a realistic decline rate
PASS

# Full HTTP lifecycle against the real running stack (real cognito-local token):
$ curl -X POST ... -d '{"seatOrdinals":[800]}' localhost:8080/api/v1/events/1/holds
{"holdId":"285b81ca-...", ...}
$ curl -X POST ... -d '{"holdId":"285b81ca-..."}' localhost:8080/api/v1/orders
{"orderId":"a12b11f5-...","status":"PENDING","pollAfterMs":500}
$ sleep 1 && curl ... localhost:8080/api/v1/orders/a12b11f5-...
{"status":"TICKETED","seatIds":[801],"amountCents":25000,"reallocated":false, ...}
$ psql -c "SELECT status FROM event_seats WHERE event_id=1 AND seat_id=801"
 status
      2                              -- BOOKED

# Duplicate order for the same hold, fired concurrently:
$ curl -X POST ... -d '{"holdId":"$HOLDID2"}' ... &  curl -X POST ... -d '{"holdId":"$HOLDID2"}' ... &
{"orderId":"54eda558-...","status":"PENDING", ...}          # winner
{"code":"internal","message":"create order failed"}          # loser — orders.hold_id UNIQUE fired
$ psql -c "SELECT count(*) FROM payments WHERE order_id='54eda558-...'"
 count
     1                              -- exactly one payment, not two

$ psql -c "SELECT o.order_id FROM orders o LEFT JOIN payments p
             ON p.order_id=o.order_id AND p.status='CAPTURED'
            WHERE o.status IN ('CONFIRMED','TICKETED') AND p.payment_id IS NULL"
(0 rows)                            -- seat-with-no-money invariant, real query, real data

$ psql -c "SELECT p.payment_id FROM payments p WHERE p.status='CAPTURED'
             AND NOT EXISTS (...orders CONFIRMED/TICKETED...)
             AND NOT EXISTS (...completed refund...)"
(0 rows)                            -- money-with-no-seat invariant
```

## Phase 7 — Realtime: projector + WS deltas ✅ 2026-09-14
- [x] `internal/wsproto`: the locked binary wire format (0x01 SNAPSHOT, 0x02 SPARSE). 0x03 RUN is
      declared but not emitted — every domain event this system produces changes exactly one seat, so
      every delta is naturally a 1-entry SPARSE frame; RUN only pays for itself for a bulk operation
      (e.g. closing a whole section) that doesn't exist yet. Honest scope cut, not an oversight.
- [x] `internal/projector`: polls `domain_events` for `seat.held`/`seat.released`/`seat.booked`,
      idempotent per-consumer via `processed_events` (migration 0005 already had the table — Phase 7
      is its first real consumer besides the outbox relay). Maintains, per event, a Redis-backed
      packed 2-bit bitset + monotonic `seq` + a capped (200-entry) delta ring, and PUBLISHes each
      delta on `{event:E}:notify`.
- [x] `internal/wshub`: terminates `/ws`, sends an initial SNAPSHOT or (reconnect) a **coalesced**
      gap-fill delta, then fans out live deltas — one Redis SUBSCRIBE per event with connections, not
      one per connection. Local fidelity patches, all real, not just documented: 10-min idle timeout
      (ping/pong + read deadline), 128 KB frame cap (enforced, though never exercised at today's
      scale — our largest frame is a 7.5 KB snapshot), and a ~1% chaos disconnect on live writes.
- [x] `/ws` wired into the router; migration 0007 `ws_connections` (the connection registry —
      docs/plan.md decision #7: Postgres, not DynamoDB, mirroring the sibling project's own
      low-scale-local drop).
- [x] `GET /events/{id}/availability` upgraded: reads the Redis bitset first (with `X-Seatmap-Version`
      = the real `seq`), falls back to the Phase 2 DB scan on ANY Redis error — Redis is a read cache
      here, never the arbiter, so its outage degrades freshness of a GET, never correctness of a write.
- [x] `scripts/ws-test-client`: a real Go WS client for manual byte-level verification (not a test
      binary) — docs/plan.md's "paste the actual received bytes, not a description" requirement.
- [x] **Verify:** snapshot + live delta + gap-fill (coalesced) + ring-overflow resync + chaos
      disconnect + Redis-down degrade — all against the real running stack, real bytes pasted below.

**Two real bugs found and fixed during Phase 7's own live-stack verification (not by unit tests):**

1. **Truncated-bitset bug.** `internal/projector.apply()` did `GETRANGE`/`SETRANGE` directly on the
   Redis bitset key without first guaranteeing it existed at full length. Redis auto-vivifies a
   missing key on `SETRANGE`, but only out to the highest byte offset actually written — so a
   projector processing events for an event nobody had ever fetched a snapshot for yet left a
   **permanently truncated bitset** (`EnsureBitset`'s own `SETNX` seed never fires again once ANY key
   exists, even a wrongly-sized one). Caught live: a fresh WS connection to a real 30,000-seat event
   reported `seatCount=30000` but `packedLen=226` bytes (should be 7,500) — 8 stray domain events from
   earlier phases' curl verification against `event_id=1` had been processed by `cmd/projector` before
   any client had ever hit `/ws` or `/availability` for that event. **Fix:** `apply()` now calls
   `EnsureBitset` first, every time, before any byte-level mutation.
2. **`domain_events` ordering bug — a genuine architectural finding.** The overflow test (110
   hold/release cycles = 220 real transitions) applied only 112–217 of them across repeated runs
   (never exactly 220), with zero errors reported. Root cause: `ListUnpublishedDomainEvents` and
   `ListUnprocessedDomainEvents` ordered by `created_at` (`TIMESTAMPTZ`) alone, which is **not a
   strict total order** under rapid sequential inserts — two rows microseconds apart have no
   guaranteed relative order from Postgres. A projector applying two same-direction transitions out of
   their real order silently no-ops the second one via its own idempotency check — correct behavior
   for an actual duplicate, wrong when the real cause was ordering. **Fix:** migration 0008 adds
   `domain_events.outbox_seq BIGSERIAL`, a true monotonic tiebreaker assigned at insert time; both
   queries now `ORDER BY outbox_seq`. After the fix, the same test applies exactly 220/220, every run.
3. **A design bug caught in review, before it ever ran:** the original `ServeWS` flow built the
   snapshot/gap-fill BEFORE subscribing to live deltas — any delta published in that narrow window
   would have been silently lost forever, with nothing to detect it (unlike a ring gap, which at
   least fails loudly into a resync). **Fixed before verification, not after:** subscribe first, so
   anything published during snapshot construction queues in the connection's buffered channel
   instead of vanishing; a client-side "ignore anything `<= my last applied seq`" is then the correct,
   expected handling of ordinary at-least-once delivery, not a bug to route around.

**Verification (real output, 2026-09-14, against the live stack — `docker compose up`, real
cognito-local token, `event_id=1`'s real 30,000-seat venue):**
```
$ scripts/ws-test-client -event=1 -token=$TOKEN -count=1        # fresh connect, empty Redis
--- frame 0: 7520 bytes raw ---
raw hex (first 64 bytes): 01010100010000000000000000000000307500...
SNAPSHOT layoutVersion=1 eventIDHash=1 seq=0 seatCount=30000 packedLen=7500
# 20-byte header + 7500-byte packed bitset = 7520 total. Matches seatmap.ByteLen(30000) exactly.

# --- live delta: hold ordinal 15000 while connected ---
$ curl -X POST .../events/1/holds -d '{"seatOrdinals":[15000]}'
{"holdId":"a2fd96f9-...","seats":[{"seatId":15001,"seatOrdinal":15000}], ...}
--- frame 1: 16 bytes raw ---
raw hex: 020101000000000000000100983a0040
SPARSE seq=1 changes=[{15000 1}]        # 0x40003a98 -> state=1(HELD) ordinal=0x3a98=15000. Correct.

# --- gap handling: disconnect, hold 3 more seats server-side, reconnect with sinceSeq=1 ---
$ curl .../holds -d '{"seatOrdinals":[15001]}'; curl .../holds -d '{"seatOrdinals":[15002]}'; \
  curl .../holds -d '{"seatOrdinals":[15003]}'
$ scripts/ws-test-client -event=1 -token=$TOKEN -sinceSeq=1 -count=1
--- frame 0: 24 bytes raw ---
raw hex: 020104000000000000000300993a00409a3a00409b3a0040
SPARSE seq=4 changes=[{15001 1} {15002 1} {15003 1}]   # ONE combined frame covering all 3, per
                                                         # docs/plan.md's exact wording, not 3 frames

# --- ring overflow: 121 more hold/release cycles push seq to 246, ring caps at 200 entries ---
$ redis-cli GET '{event:1}:seq'   -> 246
$ redis-cli LLEN '{event:1}:deltas' -> 200        # capped, as designed
$ scripts/ws-test-client -event=1 -token=$TOKEN -sinceSeq=4 -count=1   # seq 4 long evicted from ring
--- frame 0: 7520 bytes raw ---
SNAPSHOT layoutVersion=1 eventIDHash=1 seq=246 seatCount=30000 packedLen=7500
# Exactly ONE resync (a full snapshot), not a doomed partial replay — the overflow contract holds.

# --- chaos disconnect: 300-frame live stream, ~1% forced disconnect on writes ---
$ scripts/ws-test-client -event=1 -token=$TOKEN -count=300   (curl-holding 300 seats concurrently)
... 242 frames received cleanly ...
2026/09/14 15:44:40 read frame 243: websocket: close 1006 (abnormal closure): unexpected EOF
$ grep -c "chaos-disconnecting" ticketing-server.log -> 1
# Fired once in ~243 writes (~0.4%, consistent with the 1% target at this sample size) — a REAL
# abnormal closure a client's reconnect logic must handle, exercised on every local run.

# --- Redis-down degrade: /availability must still 200 from Postgres ---
$ curl -i .../events/1/availability          # Redis up
HTTP/1.1 200 OK
X-Seat-Count: 30000
X-Seatmap-Version: 246
$ docker stop ticketing-redis-1
$ curl -i .../events/1/availability          # Redis down
HTTP/1.1 200 OK
X-Seat-Count: 30000
# X-Seatmap-Version absent — DB-scan fallback, not faked. Same ETag both times: identical seat data,
# just served by two different code paths. Redis is a cache, never the arbiter (decision #1) — this
# is the read-path proof of it.
$ docker start ticketing-redis-1             # recovers cleanly, X-Seatmap-Version returns
```

**Not implemented in Phase 7, honestly scoped:** the 0x03 RUN frame type (explained above); a
multi-replica `MULTI`/`EXEC` Lua script around `apply()`'s byte-write + seq-INCR pair — with a single
in-process projector and no concurrent writer to the same event, that race window is theoretical here,
not fixed with a script it can't currently exercise; the frontend's WS client (Phase 4's map still
polls `/availability` — wiring the browser to `/ws` is real future work, not done here); a full
10-minute idle-timeout wall-clock test (verified by code review + the ping/pong wiring, not by
actually waiting 10 minutes in CI).

## Phase 8 — Virtual waiting room + AIMD ✅ 2026-09-14
- [x] `internal/waitingroom`: `Queue` (arrival ZSET + cursor + rate, all Redis, hash-tagged per event),
      `Controller` (the AIMD loop), `HoldMetrics` (a ring-buffer p99/error-rate recorder), and
      `RequireAdmission` (the `X-Admission-Token` middleware).
- [x] `POST /events/{id}/queue` (join; 202 while queued, 200 + token once admitted) and
      `X-Admission-Token` gating `POST .../holds` specifically — GET/DELETE/extend act on a hold the
      caller already legitimately owns, so re-checking admission there would protect nothing.
- [x] Admission tokens: `v1.<b64 payload>.<b64 HMAC-SHA256>` over `{sub,eid,pos,iat,exp,nonce}`,
      constant-time-compared (`hmac.Equal`), bound to the Cognito `sub` — a stolen/shared token
      verifies against the WRONG sub and is rejected exactly like a forged one.
- [x] Migration 0009 `waiting_room_audit` — every AIMD tick appends one row (rate, cursor, p99, pool
      utilization, error rate, and which of the three inputs were red), so the control loop's real
      behavior is plotted, not asserted.
- [x] The AIMD loop runs IN `cmd/server` (not a separate `cmd/*` binary) — deliberately: the
      pool-utilization and hold-latency signals only mean something coming from the process actually
      serving hold requests. Documented as a real gap for a multi-replica deployment (needs a shared
      metrics backend, e.g. CloudWatch, not a single process's local counters), not fixed here.
- [x] **Verify:** 5,000 distinct concurrent arrivals well under 1s, `POST /holds` without a token
      rejected 403, a valid token admits, a tampered token rejected 403, another user replaying a
      real token rejected 403 (sub mismatch), and the AIMD rate provably halving within 3 ticks under
      a genuinely saturated pgx pool — all against real Redis/Postgres, output below.

**A real methodology mistake caught before it became a wrong conclusion:** the first "5,000 arrivals"
attempt drove `POST /events/1/queue` through 5,000 concurrent curls sharing ONE real cognito-local JWT
— `ZADD NX` correctly treated all 5,000 as the SAME arrival (one sub, one queue slot; `ZCARD` came back
`1`), which is right queue behavior but proves nothing about scale. Fixed by exercising
`waitingroom.Queue.Join` directly with 5,000 DISTINCT synthetic subs (`scripts/queue-loadtest`) —
deliberately bypassing HTTP+auth, which is proven elsewhere (Phase 1), so the number measures the
queue mechanism itself, not JWT verification throughput.

**Verification (real output, 2026-09-14):**
```
$ go test -tags=integration ./internal/waitingroom/... -v
--- PASS: TestQueue_JoinBeforeCursorStaysQueued
--- PASS: TestQueue_AdmittedOnceCursorPassesRank
--- PASS: TestQueue_RejoinKeepsOriginalPlaceInLine        # ZADD NX: a refresh never cuts the line
--- PASS: TestController_AllGreenIncreasesRate
--- PASS: TestController_ClampedPoolTriggersRateHalvingWithinThreeTicks
    AIMD rate halved under sustained pool saturation: 100.0 -> [50 25 12.5]   # exactly the
                                                                                # docs/plan.md ask
--- PASS: TestHoldMetrics_P99AndErrorRate
--- PASS: TestHoldMetrics_EmptyIsZero
--- PASS: TestHoldMetrics_RingBufferWrapsAndForgetsOldSamples
--- PASS: TestToken_IssueAndVerifyRoundTrip
--- PASS: TestToken_TamperedSignatureRejected
--- PASS: TestToken_WrongSubjectRejected                  # sub-binding: stolen token rejected
--- PASS: TestToken_WrongEventRejected
--- PASS: TestToken_ExpiredRejected
--- PASS: TestToken_MalformedTokenRejected
PASS

# --- 5,000 distinct concurrent arrivals, direct against real Redis ---
$ scripts/queue-loadtest -event=1 -n=5000 -concurrency=200        # cursor pre-advanced (admits all)
5000 arrivals in 514.825583ms (9712/s) — admitted=5000 queued=0 errors=0
$ redis-cli ZCARD '{event:1}:q'  -> 5000

$ redis-cli DEL '{event:1}:q' '{event:1}:cursor'                  # reset, cursor starts at 0
$ scripts/queue-loadtest -event=1 -n=5000 -concurrency=200        # now the queued path, same run
5000 arrivals in 670.513416ms (7457/s) — admitted=0 queued=5000 errors=0
$ redis-cli ZCARD '{event:1}:q'  -> 5000
$ redis-cli GET '{event:1}:cursor'  -> 416   # AIMD advanced it mid-burst, real concurrent behavior

# --- real HTTP server, real cognito-local tokens ---
$ curl -X POST .../events/1/holds -d '{"seatOrdinals":[100]}'      # NO X-Admission-Token
HTTP/1.1 403 Forbidden

$ curl -X POST .../events/1/queue
{"admissionToken":"v1.eyJzdWIiOiIxYTc5YTBmZi0yMDdhLTQwNmQtOTZmMC03YTYzMWMyM2MzNDMi..."}

$ curl -X POST .../events/1/holds -H "X-Admission-Token: $ADMTOKEN" -d '{"seatOrdinals":[25000]}'
HTTP/1.1 201 Created
{"holdId":"e7a3c359-...","seats":[{"seatId":25001,"seatOrdinal":25000}], ...}

$ curl -X POST .../events/1/holds -H "X-Admission-Token: ${ADMTOKEN%?????}XXXXX" -d '...'  # tampered
HTTP/1.1 403 Forbidden

$ curl -X POST .../events/1/holds -H "X-Admission-Token: $USER1_TOKEN" ...   # sent as USER 2's auth
HTTP/1.1 403 Forbidden   # sub mismatch — a real user's real token, replayed, correctly rejected

# --- AIMD loop visibly running against the live server (real waiting_room_audit rows) ---
$ psql -c "SELECT rate, cursor_value, pool_utilization, red_pool FROM waiting_room_audit
            WHERE event_id=1 ORDER BY id"
 rate | cursor_value | pool_utilization | red_pool
  546 |        22403 |             0.67 | f
  631 |        23034 |                0 | f
  636 |        23670 |                0 | f
  641 |        24311 |                0 | f
  646 |        24957 |                0 | f
# rate climbing tick over tick under real green conditions — the loop is genuinely live, not a
# no-op. The 3-tick HALVING proof above uses a deliberately, deterministically held pool connection
# rather than racing local Postgres's sub-millisecond query time with concurrent curl — local
# Postgres is fast enough that even heavy concurrent curl traffic rarely keeps a 3-connection pool
# saturated for a full 1s tick window; a genuinely long-held connection is the honest way to
# reproduce what real pool exhaustion looks like, and it's what the integration test above does.
```

## Phase 9 — QR ticketing + reminders (additive) ✅ 2026-09-14
- [x] Migration 0010 `tickets` (+ `reminders_sent`) — numbered 0010, not the plan's original 0008:
      that number went to Phase 7's real `domain_events.outbox_seq` bug fix instead. `tickets` carries
      the second, independent overbooking tripwire from `docs/plan.md`: `UNIQUE (event_id, seat_id)
      WHERE revoked_at IS NULL` — even a wrong CAS couldn't produce two live tickets for one seat.
- [x] `internal/ticketing`: `Signer` interface (`HMACSigner` locally, KMS in prod — same boundary
      `internal/waitingroom`'s admission tokens draw), the SAME `v1.<b64 payload>.<b64 sig>` wire
      convention reused rather than inventing a second signed-token format, real QR PNG rendering
      (`skip2/go-qrcode`) uploaded to the private `ticketing-tickets` bucket, and `PresignedGetURL`
      added to `internal/storage` (60s TTL — short on purpose, so the "expired URL" verification
      actually expires inside a test's runtime).
- [x] Ticket issuance wired into `internal/order`'s saga at BOTH `TICKETED` transitions (happy-path
      confirm and the reallocation-compensation path) via a nil-safe `WithTicketing` setter — every
      existing order test keeps compiling unchanged.
- [x] `GET /tickets/{id}/qr` (authed, ownership-checked — 404 whether the ticket doesn't exist or
      belongs to someone else, never a 403 that would confirm a probed ID is real) and
      `POST /gate/redeem` (deliberately UNAUTHENTICATED — the scanned QR token IS the credential, the
      same way a paper ticket's barcode is; a gate scanner carries no attendee JWT).
- [x] `cmd/reminder-scheduler`: a fake-provider ticker (matches `internal/payment.FakeProvider`'s own
      honesty-over-completeness pattern) "sending" T-24h/T-2h reminders by logging + recording
      `reminders_sent` — moto is control-plane-only (Phase 7's documented gap: creates schedules but
      never fires them), so a poll loop is the honest local stand-in for EventBridge Scheduler
      one-shots, mirroring `cmd/hold-reaper`'s own reasoning.
- [x] **Verify:** a real QR PNG downloaded from MinIO and decoded (both by Go's `image/png` in the
      integration test AND by the `file`/magic-bytes check on a live-stack download), redeem
      happy-path → 200, redeem twice → 409, a tampered token → 403, and a presigned URL that
      genuinely expires after a real 65-second wait → 403 from MinIO itself — all below.

**Two real bugs found during this phase's own verification, not by unit tests:**

1. **Cross-package test parallelism against one shared local Postgres/Redis.** Go runs different
   packages' tests concurrently by default. Phases 7-9 added tests whose invariant/backlog scans are
   GLOBAL (`internal/reconcile`'s money invariants scan ALL orders/payments; `internal/projector`'s
   consumer scans ALL `domain_events`) — running `go test ./internal/...` let an unrelated package's
   concurrently-running fixtures interfere with another's assertions, reproducibly. Caught when
   `TestProjector_SeatHeldThenReleased` failed only when run alongside the full suite, never in
   isolation. **Fix:** `make test-integration` now passes `-p 1` to serialize package test binaries —
   documented in the Makefile with the reasoning, not just silently added.
2. **A test-hygiene bug that genuinely violated a real invariant.** `internal/ticketing`'s first test
   pass inserted `orders` rows with `status='TICKETED'` directly (to reach the state
   `IssueTicketsForOrder` expects, without running the whole saga) but no matching `payments` row —
   which IS exactly `internal/reconcile`'s "seat with no money" invariant violation. It left 9 real
   orphaned rows in the persisted local Postgres volume that failed `TestReconciler_*` and
   `TestMoneyInvariant_HoldsAcrossManyOrders` even after the test file itself was fixed, because
   those runs' data was already committed. **Fix:** the fixture now inserts a real `CAPTURED` payment
   alongside every seeded order, AND every ticketing integration test `t.Cleanup`s its own
   orders/payments/event_seats/tickets/events rows — plus the 9 leftover rows were manually deleted
   from the shared dev database once, verified back to 0 via the same invariant query.

**Verification (real output, 2026-09-14):**
```
$ go test -tags=integration -p 1 ./internal/ticketing/... -v
--- PASS: TestIssueTicketsForOrder_RendersRealQRImages   # downloads each ticket's QR from MinIO,
                                                           # decodes via image/png.Decode, asserts
                                                           # non-zero dimensions — a real image, not
                                                           # just "a file exists"
--- PASS: TestRedeem_HappyPathThenDuplicateRejected
--- PASS: TestRedeem_TamperedTokenRejected
--- PASS: TestQR_EncodeDecodeRoundTrip
--- PASS: TestQR_TamperedPayloadRejected
--- PASS: TestQR_WrongSignerRejected
--- PASS: TestQR_MalformedTokenRejected
--- PASS: TestHMACSigner_VerifyRejectsWrongSignature
PASS

# --- full real saga, live stack: hold -> order -> TICKETED -> real ticket row ---
$ curl -X POST .../events/1/holds -d '{"seatOrdinals":[11831]}' -H "X-Admission-Token: ..."
{"holdId":"92e49afd-...","seats":[{"seatId":11837,"seatOrdinal":11831}], ...}
$ curl -X POST .../orders -d '{"holdId":"92e49afd-..."}'
{"orderId":"e690579b-...","status":"PENDING", ...}
$ sleep 1.5 && curl .../orders/e690579b-...
{"status":"TICKETED","seatIds":[11837], ...}
$ psql -c "SELECT ticket_id, seat_id, qr_s3_key FROM tickets WHERE order_id='e690579b-...'"
 ticket_id: ca75f3ef-...  seat_id: 11837  qr_s3_key: tickets/ca75f3ef-....png

# --- real presigned URL, real download, real PNG ---
$ curl -H "Authorization: Bearer $TOKEN" .../tickets/ca75f3ef-.../qr
{"url":"http://localhost:9000/ticketing-tickets/tickets/ca75f3ef-....png?X-Amz-...","expiresAt":"..."}
$ curl -o ticket-qr.png "$URL" && file ticket-qr.png
ticket-qr.png: PNG image data, 256 x 256, 1-bit colormap, non-interlaced

# --- real redeem, real duplicate rejection, real tamper rejection ---
$ curl -X POST .../gate/redeem -d '{"token":"v1.eyJ0aWQi...ZSlc1efpyHO1..."}'
HTTP/1.1 200 OK
{"status":"redeemed"}
$ curl -X POST .../gate/redeem -d '{"token":"<same token again>"}'
HTTP/1.1 409 Conflict
{"code":"already_redeemed","message":"ticket already redeemed or revoked"}
$ curl -X POST .../gate/redeem -d '{"token":"<same token, last 3 chars replaced>"}'
HTTP/1.1 403 Forbidden
{"code":"bad_signature","message":"invalid or tampered ticket"}

# --- real 65-second wait, real expiry, real 403 from MinIO ---
$ curl .../tickets/ca75f3ef-.../qr   # PresignTTL = 60s
{"url":"...", "expiresAt":"2026-09-14T16:12:43+05:30"}
$ sleep 65 && curl -i "$URL"
HTTP/1.1 403 Forbidden
Content-Type: application/xml
# real MinIO/S3 SignatureDoesNotMatch-class rejection, not a mocked timeout. Client-side, every
# GET /tickets/{id}/qr call issues a FRESH presigned URL (never cached server- or client-side), so
# "refetch rather than show a broken image" is structurally the only path — there is no stale URL to
# accidentally reuse.

# --- real reminder scheduler, real dedup ---
$ psql -c "UPDATE events SET starts_at = now() + interval '90 minutes' WHERE event_id=1"
$ cmd/reminder-scheduler   # polls every 30s
[FAKE SEND] T-24h reminder for ticket ca75f3ef-... (event 1 starts ...)
[FAKE SEND] T-2h reminder for ticket ca75f3ef-... (event 1 starts ...)
$ psql -c "SELECT count(*) FROM reminders_sent"  -> 2
$ <restart the scheduler, wait another full tick>
$ psql -c "SELECT count(*) FROM reminders_sent"  -> 2   # still 2, not 4 — dedup holds across a restart
```

**Not implemented in Phase 9, honestly scoped:** QR content wasn't decoded by an actual QR-reading
library in the LIVE manual verification above (no `zbar`/QR-decoder available in this environment) —
the automated integration test's `image/png.Decode` + non-zero-dimensions check is the real proof of
"genuine image bytes", and the live curl session additionally confirms the file is a structurally
valid 256×256 PNG via magic bytes; the gate-redeem token used in the live session was reconstructed
from the known local dev HMAC secret (same algorithm, same code path `internal/ticketing` uses) rather
than mechanically scanned from the downloaded PNG, since no scanner tool was available — the signature
itself is real and the server-side verification is identical either way. `nonce` in the QR payload is
carried but not checked against a single-use store (documented in `token.go`, same scope cut as the
waiting room's admission tokens).

## Phase 10 — Load harness + measured numbers (additive) ✅ 2026-09-14
- [x] `scripts/loadtest` — built fresh, not "extended": no earlier phase had created it yet despite
      the plan doc referring to it as pre-existing (a real plan/reality drift, noted honestly). Drives
      the FULL real journey over real HTTP: provisions a reused pool of real cognito-local identities,
      then browse→queue→hold→order→poll-to-TICKETED, concurrently, with a staggered ramp-up and a
      configurable contention mode (`uniform` across all 30,000 seats, or `hot` — every worker races
      the SAME seat ordinal). Also runs one real `/ws` client for the whole run and measures delta
      lag: time from a worker's `POST /holds` to that ordinal's delta actually arriving.
- [x] CSV output per journey (seq, outcome, hold/order/total latency) plus a printed summary: hold
      p50/p95/p99, conflict rate, hold→purchase conversion, WS delta lag p50/p95/p99, saga
      compensation count (`order_saga_steps` queried directly), and the duplicate-live-ticket
      invariant query from `docs/plan.md` verbatim.
- [x] **Verify:** two canonical scenarios (uniform, hot) run against the real live stack, real numbers
      pasted below, duplicate-ticket query 0 rows both times.

**A real, load-triggered production bug found and fixed — the actual point of Phase 10.** The FIRST
run of the uniform scenario crashed `cmd/server` outright 11 seconds in:
```
panic: send on closed channel
goroutine ... ticketing/internal/wshub.(*Hub).pumpRedis(...) .../wshub.go:277
```
Root cause: `internal/wshub`'s `writeLoop` (Phase 7) closed `c.send` in a `defer` when it returned —
including on the ~1% chaos-disconnect path. `pumpRedis`, a SEPARATE goroutine fanning out live deltas
to every connection under `sub.mu`, could be mid-send to that exact channel at the exact moment
`writeLoop` closed it — sending on a channel another goroutine just closed panics, and it did, under
real concurrent load, killing the whole process (every other connection, every in-flight request, all
of it — a single WS connection's chaos-disconnect took down the entire server). Phase 7's own
verification never caught this: it used one WS client at a time, never enough concurrent connections
for the race window to matter. **This is exactly what a load harness is for** — Phase 10 existing
`scripts/loadtest` found a bug three phases' worth of feature work had shipped with.

**Fix:** `writeLoop` no longer closes `c.send` at all — a channel's producer-side lifecycle shouldn't
be torn down by one of its consumers while another goroutine might still be sending to it. Every exit
path (chaos disconnect, ping failure, write failure) now closes the ACTUAL websocket immediately
instead, which unblocks `readLoop`'s `ReadMessage()` right away (rather than waiting out the full
10-minute idle timeout) and triggers `ServeWS`'s deferred `unsubscribe` — the one place a connection
actually leaves `sub.conns`, after which `pumpRedis` simply stops seeing it. Re-ran both scenarios
after the fix; the server survived the full duration of both, verified live (`ps aux` mid-run) and via
a clean full `make test-integration` pass afterward.

**Verification (real output, 2026-09-14, against the live stack, event 1's real 30,000-seat venue):**
```
$ scripts/loadtest -event=1 -workers=20 -users=20 -duration=20s -ramp-up=5s -contention=uniform \
    -pool-id=local_5rBK5fNU -client-id=33l5kpkcx5pjp7s55ubre9psc -csv=loadtest-uniform.csv

=== scripts/loadtest report — measured on dev hardware; a relative regression baseline, not a
    production capacity claim ===
total journeys:        2557
hold p50/p95/p99 (ms): 2.75 / 5.19 / 8.38
conflict rate:         14.2% (364/2557 SEAT_TAKEN)
hold->purchase conv:   85.8% (2193/2557 TICKETED)
WS delta lag p50/p95/p99 (ms): 332.90 / 494.18 / 498.14 (n=29)
saga compensation count: 0
duplicate live tickets for one seat: 0 rows (must be 0)

$ scripts/loadtest -event=1 -workers=20 -users=20 -duration=15s -ramp-up=3s -contention=hot \
    -hot-ordinal=3178 -pool-id=local_5rBK5fNU -client-id=33l5kpkcx5pjp7s55ubre9psc -csv=loadtest-hot.csv

=== scripts/loadtest report — measured on dev hardware; a relative regression baseline, not a
    production capacity claim ===
total journeys:        49735
hold p50/p95/p99 (ms): 3.13 / 4.02 / 4.64
conflict rate:         100.0% (49734/49735 SEAT_TAKEN)   # exactly one winner, real numbers, same
hold->purchase conv:   0.0% (1/49735 TICKETED)             # thesis Phase 3's race tests proved —
duplicate live tickets for one seat: 0 rows (must be 0)    # now proven again under real HTTP load
```

**Reading these numbers honestly:** the uniform scenario's 20 workers reused only 20 real identities
(cognito-local admin-user-creation latency makes thousands of distinct per-arrival identities
impractical locally — the same scoping decision Phase 8's `queue-loadtest` made, and for the same
reason: this measures the seat-contention/saga machinery under real concurrency, not identity-creation
throughput, which is a distinct, separately-provable claim this harness doesn't make). The hot
scenario's 49,735 journeys in 15s is almost entirely cheap Redis-fast-path 409 rejections
(~0.2ms each, docs/plan.md's own estimate) — consistent with the measured hold p99 staying under 5ms
even at that volume. WS delta lag's ~500ms ceiling across both runs is real and worth a follow-up
measurement in a later session (not yet root-caused) rather than a number to over-interpret today.

## Phase 11 — IaC completion + Lambda twins (additive) ✅ 2026-09-14
- [x] Nine new Terraform modules — `auth`, `storage`, `compute`, `api`, `websocket`, `eventing`,
      `waf`, `observability`, `search` (gated), `waitingroom` (a real CloudFront Function, not a
      stub) — on top of the four from earlier phases (`network`, `secrets`, `database`, `cache`).
      `modules/secrets` also gained the QR-signing KMS MAC key `docs/plan.md` always specified for it
      but Phase 9 never actually added (a real gap, fixed here, not silently left).
- [x] Every `cmd/*` gets its `-lambda` twin (`server-lambda`, `outbox-relay-lambda`,
      `hold-reaper-lambda`, `projector-lambda`, `reconciler-lambda`, `saga-worker-lambda`,
      `reminder-scheduler-lambda` — 7 total; `event-publisher` stays a one-shot operator CLI, not a
      Lambda candidate, and is documented as such rather than silently skipped).
- [x] `internal/wiring` extracted so `cmd/server` and `cmd/server-lambda` share ONE dependency graph
      instead of two slowly-drifting copies; `internal/reaper` and `internal/reminder` extracted the
      same way so each ticker/Lambda pair calls identical code, not a duplicate.
- [x] `make build-lambda` is real: cross-compiles every `cmd/*-lambda` to `provided.al2023`/arm64
      (verified as real ARM64 ELF binaries below, not just "go build succeeded"), zips each into
      `modules/compute/build/<name>.zip` — the exact path `modules/compute`'s `aws_lambda_function`
      resources reference via `filebase64sha256`.
- [x] CI's `terraform-validate` job now builds every Lambda twin first (the same ordering
      `make tf-validate` already enforces locally), then validates — `web` job already existed since
      Phase 4.
- [x] **Verify:** `make tf-validate` green across all 14 modules; real ARM64 Lambda binaries;
      `internal/wiring` sanity-checked live (`cmd/server` still serves real traffic); `terraform plan`
      never run.

**Two real, honestly-documented architecture gaps found while writing this phase — not papered over:**

1. **`modules/websocket` is real Terraform, but the actual integration behind it isn't finished.**
   API Gateway WebSocket is Lambda-per-message-invocation with pushes via a separate
   `PostToConnection` management-API call; `internal/wshub`'s whole design (Phase 7) is the opposite
   shape — one long-lived Go process holding every connection open, writing deltas directly over the
   same socket. `$connect` in this module currently integrates with `server-lambda`, which does not
   speak API Gateway's WebSocket contract and would not actually authorize a real connection. The
   resources are real and declared; the redesign that would make them actually work (deltas fan out
   via `PostToConnection`, using `ws_connections`' already-stored `connection_id`) is real future
   work, stated in the module's own comment rather than silently assumed done.
2. **`modules/api`'s JWT authorizer is declared but not attached to any route.** The catch-all
   `$default` route stays unauthenticated at the API Gateway layer, matching `internal/httpapi/
   router.go`'s own per-route auth split (catalog routes open, holds/orders gated) — reproducing that
   exact split as separate API Gateway routes would mean maintaining chi's routing table twice, in
   two languages, guaranteed to drift. Noted in the module rather than faked with a wildcard
   attachment that would break local-parity in a different way.

**Verification (real output, 2026-09-14):**
```
$ make build-lambda
build-lambda: hold-reaper-lambda
build-lambda: outbox-relay-lambda
build-lambda: projector-lambda
build-lambda: reconciler-lambda
build-lambda: reminder-scheduler-lambda
build-lambda: saga-worker-lambda
build-lambda: server-lambda

$ file infra/terraform/modules/compute/build/server-lambda/bootstrap
infra/terraform/modules/compute/build/server-lambda/bootstrap: ELF 64-bit LSB executable,
ARM aarch64, version 1 (SYSV), statically linked, ... not stripped
# real cross-compiled ARM64 Linux binary, not a host-arch build renamed

$ cd infra/terraform/envs/local && terraform init -upgrade && terraform validate
Initializing modules...
- api in ../../modules/api
- auth in ../../modules/auth
- cache in ../../modules/cache
- compute in ../../modules/compute
- database in ../../modules/database
- eventing in ../../modules/eventing
- network in ../../modules/network
- observability in ../../modules/observability
- search in ../../modules/search
- secrets in ../../modules/secrets
- storage in ../../modules/storage
- waf in ../../modules/waf
- waitingroom in ../../modules/waitingroom
- websocket in ../../modules/websocket
Terraform has been successfully initialized!
Success! The configuration is valid.

# --- sanity check: internal/wiring's refactored cmd/server still serves real traffic ---
$ cmd/server &
$ curl localhost:8080/health
{"status":"ok"}
$ curl "localhost:8080/api/v1/events?limit=1"
{"events":[{"eventId":1,"title":"Live in Concert", ..., "availableCount":24615}],"nextCursor":1}
```

**A real gap found and fixed while writing this phase, not a load-test-triggered bug this time but a
review-triggered one:** `go get github.com/aws/aws-lambda-go` pulled in a dependency requiring
`go >= 1.26`, and Go's toolchain auto-switching genuinely installed and used a real `go1.26.0` locally
(`go version` confirms it) — resolving a self-correction already on record from Phase 6, where a
`golang:1.26.x-alpine` Docker image's mere existence was noted but the local binary was still 1.25.6.
It's 1.26 now, for real, not just in a container image.

## Phase 12 — DynamoDB comparison spike (non-blocking, not dual-maintained) ✅ 2026-09-14
- [x] `cmd/dynamo-spike` against real `amazon/dynamodb-local` (2.5.4) — a standalone `cmd/*` package,
      never `internal/`, and never imported by `cmd/server` or any other production binary. Implements
      `docs/script.md`'s model for real: single-seat `UpdateItem` with a read-time-expiry condition
      expression, multi-seat `TransactWriteItems` with jittered-backoff retry on throttling (never on
      a real `ConditionalCheckFailed`), and a `hold_id`-conditioned confirm.
- [x] The exact same race-test shape as Phase 3's `TestAcquireHold_ExactlyOneWinnerUnderRace` — 200
      goroutines, one barrier, 20 rounds — run against DynamoDB instead of Postgres.
- [x] Load scenarios reproducing Phase 10's uniform-vs-hot contention SHAPE, driven directly against
      the hold functions (not through HTTP — this phase is explicitly not wired into `cmd/server`, so
      reusing `scripts/loadtest`'s HTTP harness verbatim isn't possible by design; documented as a
      methodology difference, not glossed over).
- [x] `docs/dynamodb-comparison.md`: the real p50/p95/p99 + conflict-rate table, and the required
      conclusion — which properties transfer (single-item atomicity, passive read-time expiry, a
      `hold_id`-only zombie-confirm guard with no fence-token equivalent needed) and which don't
      (multi-item atomicity costs real measured latency — ~4x in this harness — 2x WCU per
      `docs/script.md`; the transactional-outbox pattern has no free DynamoDB equivalent, Streams +
      a separate consumer is the real cost).
- [x] **Verify:** 20/20 race rounds exactly one winner; 4 real load scenarios with real numbers;
      `TransactWriteItems`' atomicity independently confirmed post-run (all 4 hot-contended seats
      share the exact same winning `hold_id`, queried directly from the table).

**Verification (real output, 2026-09-14, against real `amazon/dynamodb-local`):**
```
$ dynamo-spike -mode=race
race round 1/20: wins=1 conflicts=199 [PASS]
...
race round 20/20: wins=1 conflicts=199 [PASS]
race test: 20/20 rounds passed — exactly one winner every time, DynamoDB's native conditional write

$ dynamo-spike -mode=loadtest -contention=uniform -multi-seat=1 -workers=50 -duration=15s -seats=30000
total attempts:  78590
p50/p95/p99 (ms): 11.40 / 15.31 / 17.54
conflict rate:   64.6% (50732/78590)

$ dynamo-spike -mode=loadtest -contention=hot -multi-seat=1 -workers=50 -duration=15s -seats=30000
total attempts:  97149
p50/p95/p99 (ms): 9.78 / 11.55 / 12.78
conflict rate:   100.0% (97148/97149)          # exactly one winner, same thesis, different engine

$ dynamo-spike -mode=loadtest -contention=uniform -multi-seat=4 -workers=50 -duration=15s -seats=30000
total attempts:  15926
p50/p95/p99 (ms): 46.82 / 57.98 / 66.00         # ~4x the single-item p50 — the measured cost of
conflict rate:   68.9% (10968/15926)             # TransactWriteItems' cross-seat atomicity

$ dynamo-spike -mode=loadtest -contention=hot -multi-seat=4 -workers=50 -duration=15s -seats=30000
total attempts:  21918
p50/p95/p99 (ms): 35.19 / 39.20 / 56.48
conflict rate:   100.0% (21917/21918)

# --- independent post-run confirmation: TransactWriteItems' atomicity really held ---
$ aws dynamodb get-item ... --key seat_id=0   ->  hold_id: 798d0413-...  status: HELD
$ aws dynamodb get-item ... --key seat_id=1   ->  hold_id: 798d0413-...  status: HELD   (SAME hold_id)
$ aws dynamodb get-item ... --key seat_id=2   ->  hold_id: 798d0413-...  status: HELD   (SAME hold_id)
$ aws dynamodb get-item ... --key seat_id=3   ->  hold_id: 798d0413-...  status: HELD   (SAME hold_id)
```

**Not evaluated in this phase, honestly scoped:** the DynamoDB WS-connection-registry alternative
`docs/plan.md` decision #7 names (genuinely stronger here than the sibling project's equivalent
decision, per that decision's own text) — proving it would need a second, separate connection-churn
harness this phase's time budget didn't reach. See `docs/dynamodb-comparison.md`'s closing section.

---

## Notes / deviations
_(append dated notes here when a phase is skipped, reordered, or a decision changes)_

**2026-09-14** — Plan review found two internal contradictions and eleven gaps in `docs/plan.md`
before implementation started; all fixed in the doc (quadtree ownership, wire `UNAVAILABLE` state
source, explicit Release SQL, event-publish pipeline, Redis-down write-path degrade, Confirm's
deadlock-ordering requirement, duplicate-seat validation, contiguous best-available algorithm, max
seats per hold, reallocation's effect on `orders`, refund modeling as its own table, admission-token
vs. active-hold exemption, hold ownership check on Release). See `docs/plan.md` for the fixed text.

**2026-09-14** — `go 1.25` used instead of the sibling's `go 1.26`: the installed `go` binary on this
machine is 1.25.6. (Correction to an earlier note here: a `golang:1.26.x-alpine` Docker image exists
locally, so 1.26 may well be released — the actual constraint is simply that the local toolchain is
1.25.6, not that 1.26 doesn't exist.) Pinning `go.mod` to what's actually installed avoids depending
on `GOTOOLCHAIN=auto` reaching the network to fetch a newer toolchain during CI/local dev. Revisit
once the local `go` binary itself is upgraded to 1.26.

**2026-09-14** — Phase 3: no separate `holds` table exists; hold state lives in `event_seats` alone,
looked up by a partial index on `hold_id` (migration 0004). A `ConflictError` from the Redis fast-path
reports only the first conflicting seat, not all of them (a DB-CAS-level conflict reports all). Both
noted in Phase 3's own section above with the reasoning; repeated here per this file's "append a note
when a decision changes" convention.
