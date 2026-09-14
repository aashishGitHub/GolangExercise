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
