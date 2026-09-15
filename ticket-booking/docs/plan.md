# Plan — `ticket-booking`: Event Ticket Booking Platform (Go + AWS + Terraform)

## Context

`ticket-booking/` today contains only two study documents — `docs/deep-dive.md` (a 1,025-line
vendor-neutral system-design study of seat reservation under contention) and `docs/script.md` (a
261-line AWS-flavored interview script). There is no code, no `go.mod`, and the directory is
untracked in git. The sibling project `offline-sync-app/` (FieldSync) has just completed Phases 0–7
and established a working house style: a phased `MILESTONES.md` where every checked item records
*how it was verified* with real output, a free local-AWS emulator patchwork, and Terraform that is
written and `validate`-passing throughout but never `apply`-ed automatically.

The goal is to turn those two study docs into a real, production-standard build in the same house
style — a system whose thesis is **a seat is never sold twice, under a 500,000-person stampede**,
and whose correctness claim is *proven by tests that race, not asserted in prose*.

The two source docs **disagree on the core mechanism**. `deep-dive.md` prescribes Postgres as the
arbiter with Redis `SET NX PX` holds and fencing tokens; `script.md` prescribes DynamoDB conditional
writes with Redis as a pure read cache. Both are correct implementations of the same invariant. The
user has locked **Postgres as the arbiter**, with the DynamoDB variant built later as a benchmarked,
explicitly non-dual-maintained spike.

---

## Locked decisions

| # | Decision | Rationale |
|---|---|---|
| 1 | **Postgres `event_seats` is the sole arbiter** of seat state. Redis is a contention filter + read cache, never authoritative. | A multi-seat hold within one event is a single-shard transaction in Postgres (deep-dive §8) without DynamoDB `TransactWriteItems`' 2× write cost and hard-fail-under-contention. Orders/payments need a relational financial audit trail anyway — one engine, not two. |
| 2 | **Expiry is a predicate in the SQL `WHERE` clause** (`hold_expires_at < now()`), evaluated by the next contender. Redis TTL and the scheduler are UX only. | `script.md`: *"correctness never depends on a timer firing."* This also makes the moto-server emulator gap (it never fires schedules) cost freshness, never correctness. |
| 3 | **Fence guard is `fence_token = :expected`, not `>=`.** | `deep-dive.md` §4 writes `>=`, which with a per-row monotonically increasing token is a tautology — it always passes and fences nothing. Kleppmann's rule is that the *resource* rejects a stale token; the resource here is the row, so the guard must be equality against the generation the hold was issued under. **This is a correction to the source doc** and should be noted as such in `MILESTONES.md`. |
| 4 | **Hold TTL = 600s default, configurable**, surfaced as server-authoritative `expiresAt`. | `deep-dive` says `PX 600000` (10 min), `script.md` says 5 min. Note the conflict rather than silently picking. |
| 5 | **The dense `ordinal` is the client-facing seat identity** on every wire format. `seat_id BIGINT` stays internal. | Ordinal is simultaneously the index into the layout's columnar arrays, the bitset index, the quadtree payload, the delta payload, and the a11y gridcell key. UUID seat ids would grow every delta from 4 to ~20 bytes and break the bitset scheme outright. |
| 6 | **Broadcast availability is binary and impersonal**; personal state (my hold, my order) is unicast JSON. | One blob in Redis, identical bytes to all 500K subscribers. Personalizing the bitset destroys the fan-out property the whole read path depends on. |
| 7 | **WS connection registry is Postgres**, mirroring the sibling's deliberate DynamoDB drop. | One less moving part locally and in prod. The DynamoDB argument (high-churn, TTL-per-member, keeps 500K connect/disconnect writes off the seat-write path) is genuinely stronger here than it was in FieldSync — it is written down and revisited in the Phase 12 spike rather than dismissed. |
| 8 | **Search = Postgres `pg_trgm` + GIN**, not OpenSearch. The `search` Terraform module is written and `validate`s but is gated behind `var.enable_search = false`. | Two OpenSearch data nodes cost more per month than Aurora + ElastiCache + all Lambda combined, for a catalog of thousands of events. `script.md`'s `multi_match`/`fuzziness AUTO`/edge-ngram story survives the swap, which is itself the argument for deferring. |
| 9 | **Emulators = free patchwork** (sibling's proven stack + Redis). Terraform written and `validate`-passing throughout; `plan`/`apply` is a separate, explicitly user-triggered step, never automatic. | LocalStack's free image refuses to boot without `LOCALSTACK_AUTH_TOKEN` — confirmed via container logs in FieldSync Phase 3. |

---

## Local stack (`docker-compose.yml`, stack name `ticketing`)

| Service | Image | Ports | Why |
|---|---|---|---|
| `postgres` | `postgres:16` | 5432 | The arbiter. Aurora Postgres 16.x is the prod target, so the major must match. |
| `redis` | `redis:7-alpine` | 6379 | Three jobs: `SET NX PX` hold fast-path, the availability bitset cache, the waiting-room `ZSET`. |
| `cognito-local` | `jagregory/cognito-local:4.0.0` (pin exactly) | 9229 | ≥4.0.0 for `.well-known/{openid-configuration,jwks.json}`. Port must stay 9229 — the image hardcodes it in issuer/JWKS URLs. `CODE: "123456"`. |
| `minio` | `minio/minio:RELEASE.<pinned>` | 9000/9001 | S3 stand-in. **Pin the RELEASE tag, not `latest`** — recent MinIO releases removed most of the console. |
| `minio-init` | `minio/mc:RELEASE.<pinned>` | — | One-shot: creates `seat-layouts`, `ticket-qr`, `web`; sets `Cache-Control: public, max-age=31536000, immutable` on layouts so the browser cache behavior under test is the real one. |
| `moto-server` | `motoserver/moto` | **5001**→5000 | EventBridge/SQS/Scheduler control plane. Host 5001 because 5000 collides with macOS AirPlay Receiver (FieldSync's finding). |

Deliberately **not** containers: the WebSocket hub (hand-rolled in-process Go, `internal/wshub`, on
`/ws`), any DynamoDB emulator (until Phase 12), any pgbouncer.

**Redis key hash-tagging from day one:** `{event:123}:seat:456:hold`, `{event:123}:bitset`,
`{event:123}:queue`. Costs nothing now; without it any future ElastiCache cluster mode breaks every
multi-key Lua script.

---

## Repo layout

```
ticket-booking/
  cmd/          server, lambda, outbox-relay(+lambda), hold-reaper(+lambda),
                saga-worker(+lambda), reconciler(+lambda), projector(+lambda),
                notifier-lambda, ws-connect-lambda, ticketer(+lambda),
                waiting-room(+lambda), dynamo-spike
  internal/     config auth apierr httpapi db catalog seatmap holdlock inventory
                order payment reconcile ticketing waitingroom notify wshub
                events outbox consume sqsconsume scheduler storage metrics
  migrations/   golang-migrate paired up/down
  sqlc/         sqlc.yaml + queries.sql
  scripts/      seed-cognito-local.sh, seed-venue/, loadtest/
  web/          React + Vite + TS (see Frontend below)
  infra/terraform/{envs/local, modules/*}
  docs/         deep-dive.md, script.md (existing) + comparison writeups
  MILESTONES.md docker-compose.yml Makefile .env.example
```

Module `ticketing`, `go 1.26`, imports `ticketing/internal/...`. Every convention mirrors
`offline-sync-app/`: flat `config.Load()` with `getenv(k, fallback)` and **local-only settings
defaulting to empty, not localhost**; chi v5 + pgx/v5 + sqlc (`emit_interface: true` → `Querier`,
`json_tags_case_style: camel`); `go.uber.org/mock` with committed `mocks/` subpackages carrying the
`mockgen` command in the file header; no testify; integration tests behind `//go:build integration`.

**Hard invariant, stated in `internal/inventory`'s package comment:** *no package other than
`inventory` may write `event_seats`.* That is `script.md`'s "Inventory is the sole writer of seat
state", enforced by convention and greppable.

---

## Data model (9 migrations)

`venues/sections/seat_rows/seats/events` use `BIGINT` identity (dense — the ordinal depends on it,
and it matches deep-dive §8 verbatim). `holds/orders/payments/tickets` use `UUID`.
`domain_events.aggregate_id` therefore widens to `TEXT` — a deliberate deviation from the sibling.

**`000002_event_seats`** — the write-path table and the heart of the schema:

```sql
event_seats(
  event_id BIGINT, seat_id BIGINT,
  seat_ordinal INT NOT NULL,              -- dense 0..N-1 per event: THE bitset index
  status SMALLINT NOT NULL DEFAULT 0,     -- 0 AVAILABLE 1 HELD 2 BOOKED 3 PENDING_PAYMENT
  sellable BOOLEAN NOT NULL DEFAULT true, -- false = structurally unsellable (closed section, obstructed, kill)
  hold_id UUID, held_by TEXT, hold_expires_at TIMESTAMPTZ,
  booking_id UUID, price_cents INT NOT NULL,
  hold_price_cents INT,                   -- deep-dive §9: price locked at hold time
  version INT NOT NULL DEFAULT 0,
  fence_token BIGINT NOT NULL DEFAULT 0,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (event_id, seat_id))
UNIQUE (event_id, seat_ordinal)
INDEX event_seats_cover_idx  ON (event_id, seat_ordinal) INCLUDE (status, sellable, price_cents)
INDEX event_seats_expiry_idx ON (hold_expires_at) WHERE status IN (1,3)
CHECK (status <> 2 OR booking_id IS NOT NULL)
CHECK (status NOT IN (1,3) OR (hold_id IS NOT NULL AND hold_expires_at IS NOT NULL))
```
The Acquire CAS additionally requires `sellable = true` (below). The projector projects
`sellable = false` straight to wire `UNAVAILABLE` regardless of `status`; the catalog's
`closedSections` list is denormalized *from* this column for display, never the other way round.

**Max seats per request:** `POST /holds` rejects `seatOrdinals.length` / `quantity` above
`MAX_SEATS_PER_HOLD` (default 8) with `422 too_many_seats` — both an anti-scalping control and a
bound on transaction size, applied before the Redis step.

Other tables: `holds_audit` (every attempt won or lost — this is what the concurrency test queries),
`orders` + `payments` (`UNIQUE(idempotency_key)`) + `order_saga_steps`, `refunds` (`payment_id` FK,
`provider_ref`, `amount_cents`, `status`, `created_at` — **a separate table, not a status flip on
`payments`**, so a captured payment's row stays queryable as CAPTURED and a refund is independently
auditable; see the reconciler note below), `idempotency_keys` (HTTP-level, distinct from the payment
key), `domain_events` + `processed_events`, `ws_connections`, `tickets`, `waiting_room_audit`.

**`tickets` carries a second, independent overbooking tripwire:**
`UNIQUE (event_id, seat_id) WHERE revoked_at IS NULL` — even if the CAS were ever wrong, two live
tickets for one seat cannot be inserted.

### Event publish pipeline (the missing step between "venue exists" and "event_seats exist")

Nothing above works until an event's `event_seats` rows exist with `seat_ordinal`s matching the
static layout's `section → row → seat` order, and `layout.json`/`seats.bin` exist in the layouts
bucket. `cmd/event-publisher` (invoked by an admin route, `POST /admin/events/{id}/publish`, not a
consumer) does this as one job, idempotent on `event_id`:
1. Walk `venues → sections → rows → seats` in `section.display_order, row.display_order, seat_label`
   order — this fixed order **is** the ordinal assignment, computed once and stored.
2. Bulk-insert `event_seats` (`seat_ordinal` = loop counter, `price_cents` from the section's tier,
   `sellable` from the section's `closed` flag) in one transaction.
3. Render `layout.json` + `seats.bin` from the same walk (guaranteeing they can never disagree with
   the DB's ordinal assignment) and upload to the layouts bucket at the versioned path.
4. Flip `events.status = 'ON_SALE'`.
Re-running against an already-published event is a no-op (checked via `events.status`), which is what
makes this safe to retry from a failed step.

---

## The correctness core

### Acquire a hold
0. **Validate the request first, before touching Redis or Postgres:** dedupe and sort `seatOrdinals`
   ascending, reject if any duplicate was removed (`422 duplicate_seat`), reject if
   `len > MAX_SEATS_PER_HOLD` (`422 too_many_seats`). All downstream code — Redis keys, the CAS's
   `ANY(@seat_ids)`, and `ConfirmSeats`'s `unnest` join — depends on the array being duplicate-free;
   a repeated seat id in an `unnest`-joined `UPDATE` throws ("command cannot affect row a second
   time"), so this validation is not optional polish.
1. `holdID := uuid.New()` — opaque, non-enumerable.
2. **Redis contention filter**, seats ascending: `SET {event:E}:seat:S:hold <holdID> NX PX 600000`.
   `nil` → compare-and-delete everything already taken in this batch, audit `LOST_REDIS`, return
   `409 SEAT_TAKEN` in ~0.2 ms with zero DB work. This is a *throughput* mechanism, not a correctness
   one. **If Redis itself is unreachable (connection error/timeout, not just "key exists"), skip this
   step entirely and go straight to the Postgres CAS** — treat every seat as provisionally
   uncontended and let the CAS be the sole gate. Failing the request closed here would turn the
   non-arbiter into a single point of failure for the write path, which decision #1 explicitly rejects.
   The cost is real and must be capacity-planned for: during a Redis outage, throughput drops to raw
   Postgres CAS throughput because the cheap rejection path is gone.
3. **Postgres CAS — the arbiter.** One transaction, seats **already sorted ascending by `seat_id`
   from step 0** so deadlock is impossible — this ordering guarantee matters for `ConfirmSeats` too
   (see below), not just here:
   ```sql
   UPDATE event_seats
      SET status=1, hold_id=@hold_id, held_by=@sub,
          hold_expires_at = now() + @ttl, hold_price_cents = price_cents,
          fence_token = fence_token + 1, version = version + 1, updated_at = now()
    WHERE event_id=@event_id AND seat_id = ANY(@seat_ids::bigint[])
      AND sellable
      AND ( status = 0 OR (status IN (1,3) AND hold_expires_at < now()) )
   RETURNING seat_id, seat_ordinal, fence_token, hold_expires_at, hold_price_cents;
   ```
   Full match → COMMIT with a `seat.held` outbox row in the *same* transaction. Partial → ROLLBACK
   the whole batch, release the Redis keys, return `409` with the exact `conflicts` ordinals so the
   client repaints only those seats.

   **The fence token comes from the database.** `fence_token + 1` is evaluated inside the row's own
   write lock — monotonic per `(event_id, seat_id)` with no coordinator. `RETURNING` hands the caller
   the generation its hold was issued under; it is opaque to the client and echoed back on confirm.
4. Schedule the active release (`expiresAt + 5s`). Best-effort; failure is logged, not fatal.

**Best-available / contiguous selection** (`bestAvailable: true`): a candidate query per row,
ordered by section preference then row proximity to the "best" edge —
`SELECT seat_ordinal FROM event_seats WHERE event_id=$1 AND status=0 AND sellable AND price_cents<=$2
  AND seat_ordinal BETWEEN @rowFirst AND @rowLast ORDER BY seat_ordinal` — scanned per row (rows are
contiguous ordinal ranges, decision #2) for the first in-row run of `quantity` consecutive ordinals.
If `contiguous:true` and no row has a large-enough run, widen to adjacent rows in the same section
before falling back to "best N seats regardless of adjacency". The candidate set is deliberately
over-fetched (rows continue being scanned past the first hit) so that a CAS conflict (step 3 partial
match) can retry against the *next* candidate run without a second round trip to the client — bounded
to 3 attempts, then `409 no_contiguous_seats` with the largest run found as a suggestion.

### Release (`DELETE /holds/{id}`)
Symmetric to Acquire, and idempotent by construction — called on explicit deselect/navigation-away,
by the reaper, and by a failed saga's compensation step.
```
EVAL "if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]) else return 0 end"
     1 {event:E}:seat:S:hold {holdID}          -- per seat; Redis errors here are logged, not fatal
```
```sql
UPDATE event_seats
   SET status=0, hold_id=NULL, held_by=NULL, hold_expires_at=NULL, hold_price_cents=NULL,
       version=version+1, updated_at=now()
 WHERE event_id=@event_id AND seat_id = ANY(@seat_ids::bigint[])
   AND status IN (1,3) AND hold_id=@hold_id;
```
`rowcount=0` is **success, not an error** — the hold was already gone (expired-and-reclaimed, or
already confirmed). The handler also checks `held_by == caller's sub` before issuing the release,
independent of the hold id's non-enumerability, as defense in depth against a guessed/leaked UUID.

### Extend at payment initiation
`UPDATE ... SET status=3, hold_expires_at = greatest(hold_expires_at, now() + @ext) WHERE ... AND
hold_id=@hold_id AND hold_expires_at > now()`. `rowcount < N` → the saga aborts **before** charging.
This is the cheapest possible prevention of money-with-no-seat. `status=3` seats remain reclaimable
on the longer clock, so a dead saga cannot deadlock a seat forever. **The admission token's own TTL
is independent of this** — the admission check on `POST /orders`/`.../pay` is waived when the caller
already owns a live hold on the order's `hold_id` (proven by the DB row, not by the token), so a slow
checkout can never be evicted by the *queue* gate after already being admitted through it once.

### Confirm — the single most important statement
```sql
UPDATE event_seats es SET status=2, booking_id=@order_id, hold_id=NULL,
       held_by=NULL, hold_expires_at=NULL, version=version+1, updated_at=now()
  FROM unnest(@seat_ids::bigint[], @fences::bigint[]) AS f(seat_id, fence)
 WHERE es.event_id=@event_id AND es.seat_id=f.seat_id
   AND es.status IN (1,3) AND es.hold_id=@hold_id
   AND es.fence_token = f.fence          -- corrected from the doc's `>=`
   AND es.hold_expires_at > now();
```
`@seat_ids`/`@fences` **must be pre-sorted ascending by `seat_id` together**, same as Acquire — a
single `UPDATE ... FROM unnest(...)` join does not itself guarantee row-lock acquisition follows
array order, so two large concurrent multi-seat confirms with overlapping rows could otherwise
deadlock (Postgres would detect and abort one safely, but it's an avoidable retry, not a correctness
issue). Runs inside the confirm transaction with the `orders` flip, the `payments` row, the `tickets`
insert and the outbox row. `rowcount != len(seats)` → roll back everything → the saga enters
compensation.

**Reallocation's effect on `orders`:** compensation's "re-allocate an equivalent seat" path acquires
a **new** `hold_id` (via the normal Acquire path against different seats) and, on success, updates
the *same* `orders` row's `hold_id` and `seat_ids` in place inside one transaction, setting
`reallocated=true`. `orders.hold_id UNIQUE` is therefore enforced against the current hold only — the
original (now-abandoned) hold is released through the normal Release path first, in the same
transaction, so the unique constraint never sees two live values for one order.

### Race matrix (each row becomes a test)
| Race | Outcome | What saves it |
|---|---|---|
| Two users `SET NX` the same seat | One `OK`, one `nil` | Redis is single-threaded. Throughput win only. |
| **Redis flushed / failed over — both get `OK`** | Both reach the DB CAS; exactly one `rowcount=1` | Postgres row lock. **Double-hold possible, double-book impossible.** |
| Redis TTL fired but the DB row still says HELD | Next contender's `hold_expires_at < now()` reclaims it | Passive expiry. The reaper never had to fire. |
| DB expired but the Redis key survives | Contender fails at Redis, retries | Fails *closed* — an extra "try again" beats an overbook. |
| Hold expires mid-payment, **before** charge | `ExtendHold` rowcount < N → abort → `409`, no money moved | The extend step exists for exactly this. |
| Hold expires mid-payment, **after** charge | Confirm rowcount 0 → `COMPENSATING` → **re-allocate equivalent seat first, refund second** | `script.md`'s compensation priority. `saga_compensation_count` watches it. |
| Duplicate confirm (double-click, SQS redelivery) | Second CAS matches 0 rows — `hold_id` is now NULL | The CAS is self-idempotent. |
| Payment provider 408 (ambiguous) | Never a blind retry: re-send with the same idempotency key, or query by key | `payments.status='UNKNOWN'` + the reconciler owns the tail. |
| Zombie confirms late after reclaim + re-hold | New holder bumped `fence_token`; the zombie's equality guard fails | The fence. |
| Crash between COMMIT and EventBridge publish | Row committed unpublished; relay rescans `published_at IS NULL` | Transactional outbox. |

### The saga
`PENDING → AUTHORIZING → CAPTURED → CONFIRMING → CONFIRMED → TICKETED`, with
`COMPENSATING → REALLOCATED | REFUNDING → COMPENSATED`.

Idempotency key = `sha256(userSub:orderID:holdID)`, stored `UNIQUE` on `payments`; insert-first
`ON CONFLICT DO NOTHING` is the real guard, the provider's own key is the second independent layer.
`POST /orders` writes rows and returns `202` — the saga runs async so a slow provider never holds an
HTTP connection.

`internal/payment` is a fake provider with **injectable pathology**: `PAYMENT_FAIL_RATE`,
`PAYMENT_TIMEOUT_RATE`, `PAYMENT_AMBIGUOUS_RATE` (captures, *then* returns a timeout — the only way
to genuinely exercise the reconciler). All default to zero.

The reconciler (boot + every 60s) implements deep-dive §6's three steps and runs the two invariant
queries, both of which **must always return zero rows**:
```sql
-- seat with no money
SELECT o.order_id FROM orders o LEFT JOIN payments p
  ON p.order_id=o.order_id AND p.status='CAPTURED'
 WHERE o.status IN ('CONFIRMED','TICKETED') AND p.payment_id IS NULL;
-- money with no seat and no refund
SELECT p.payment_id FROM payments p WHERE p.status='CAPTURED'
  AND NOT EXISTS (SELECT 1 FROM orders o WHERE o.order_id=p.order_id
                    AND o.status IN ('CONFIRMED','TICKETED'))
  AND NOT EXISTS (SELECT 1 FROM refunds r WHERE r.payment_id=p.payment_id AND r.status='COMPLETED');
```
(`refunds` is its own table, not a status flip on `payments` — a captured payment's row stays
queryable as `CAPTURED` and the refund is independently auditable; had refund been modeled as an
in-place status update, this `NOT EXISTS` would be unreachable dead logic since the outer
`p.status='CAPTURED'` filter would already exclude a refunded row.)

### Waiting room
Redis per event: `{event:E}:q` (`ZSET`, score = arrival micros, `ZADD NX` so a refresh keeps your
place), `:cursor` (admitted iff `ZRANK < cursor`), `:rate` (so N API replicas agree without
coordinating). Admission token is HMAC, not JWT — `v1.<b64 payload>.<b64 HMAC-SHA256>` over
`{sub,eid,pos,iat,exp,nonce}`, verified in middleware with a constant-time compare and **bound to the
Cognito sub** so it cannot be traded.

AIMD controller at 1 Hz reads hold CAS p99 (red > 500 ms), pgx pool utilization (red > 0.80), and
hold error rate (red > 0.01): `anyRed → rate = max(rate*0.5, MIN)`, `allGreen → rate = min(rate+Δ, MAX)`,
`cursor INCRBY rate*tick`. Every tick appends to `waiting_room_audit` so the loop closing can be *plotted*,
not asserted.

---

## API contract (base `/api/v1`)

Locked reconciliation points between the two designs:

| # | Locked |
|---|---|
| 1 | Seats are **dense ordinals** on the wire, never UUIDs. `seatId` is internal. |
| 2 | Ordinal assignment is `section → row → seat-number`, frozen per `layoutVersion`, so **every row and section is a contiguous ordinal range**. |
| 3 | Bitset states: `0 FREE, 1 HELD, 2 SOLD, 3 UNAVAILABLE`. DB `status=3 PENDING_PAYMENT` projects to wire `1 HELD`. `HELD_BY_ME` is **not** in the bitset — it is a client overlay from `holdStore`. **`UNAVAILABLE` has no corresponding `event_seats.status` value** — it is synthesized by the projector from `event_seats.sellable = false` (added to the schema below), never inferred from booking-flow status. |
| 4 | Every hold/extend response returns `expiresAt` **and** `serverTime`, so the client computes skew instead of trusting its clock. |
| 5 | The monotonic counter is named `seq`, appears in every frame, and gaps are detectable. |
| 6 | `layoutVersion` appears in every snapshot frame; on mismatch the client **refuses to render**. |

```
GET  /events?q=&cursor=                 -> { events[], nextCursor }
GET  /events/{id}                       -> { …, layoutVersion, layoutUrl, pricingUrl, wsUrl, saleState }
GET  /events/{id}/availability          -> binary 0x01 snapshot (octet-stream, gzip, ETag)
GET  /events/{id}/pricing               -> { priceVersion, tiers[], closedSections[] }  (max-age=60, SWR)
GET  /venues/{id}/layout?v=             -> layout.json (immutable, 1y)   + seats.bin
POST /events/{id}/queue                 -> 202 {position, etaSeconds, pollAfterMs} | 200 {admissionToken}
POST /events/{id}/holds       [Idempotency-Key, X-Admission-Token]
       {seatOrdinals[]} | {quantity, priceTierId, bestAvailable:true}
  201 -> { holdId, seatOrdinals, expiresAt, serverTime, totalCents, fenceTokens }
  409 -> { code:'SEAT_TAKEN', conflicts:[], suggestions:[] }
GET/DELETE /holds/{id}                  -> server-computed secondsRemaining / 204 (idempotent)
POST /holds/{id}/extend                 -> { expiresAt, serverTime }
POST /orders                  [Idempotency-Key] -> 202 { orderId, status:'PENDING', pollAfterMs }
GET  /orders/{id}                       -> full resumable state (the resume source of truth)
GET  /tickets/{id}/qr                   -> { url, expiresAt }   presigned, short TTL
POST /gate/redeem                       -> 200 | 409 already_redeemed | 403 bad_signature
```

**Status-code semantics (the part that gets argued about):** `409 SEAT_TAKEN` is *the system working*
— it is the dominant non-2xx during an on-sale and must be fast and cheap; `409 HOLD_EXPIRED`
retryable by re-holding; `409 IN_PROGRESS` for an in-flight idempotency key; `410` for an unknown/reaped
hold (nothing to retry against); `403 NOT_ADMITTED` carries queue position; `422` for business rules;
`503` when Postgres is unreachable — **deliberately not degraded to Redis**, the write path fails closed.

### WebSocket
`wss://…/ws?event={id}&token={jwt}&admission={t}` — token in the query string because a browser
`WebSocket` cannot set headers (the sibling's documented finding).

**Binary = availability, identical bytes for every subscriber** (one buffer, fan-out for free):
```
0x01 SNAPSHOT  op|protoVer|u16 layoutVersion|u32 eventIdHash|u64 seq|u32 seatCount|packed bitset
0x02 SPARSE    op|protoVer|u64 seq|u16 count|u32[count] = (state<<30)|ordinal    -- 4 bytes/change
0x03 RUN       op|protoVer|u64 seq|u16 runCount|{u32 start,u16 len,u8 state,u8 pad}[]
```
Packing: `byte = ord>>2`, `shift = (ord&3)*2`, little-end-within-byte. 50,000 seats = 12,500 bytes,
<2 KB gzipped. 200 changes in a busy second = 812 bytes.

**JSON = control/personal, unicast:** `hold_expiring`, `hold_lost` (with `suggestions`),
`queue_position`, `order_update`, `error`.

**Ordering rule:** `seq` is monotonic per event. A gap → discard the frame, `{"action":"resync"}`,
receive a fresh `0x01`. **Never apply an out-of-order delta** — a bitset is not self-correcting.
Deltas are coalesced server-side per 100 ms tick, then batched to `requestAnimationFrame` client-side.

---

## Frontend (`web/`)

React 19 + Vite + TS + Zustand + Amplify Auth (`USER_PASSWORD_AUTH`) + Dexie, `envDir` at the app
root — all mirroring the sibling. **New deps: only `@playwright/test` and `@axe-core/playwright`.**
No PixiJS/Three.js/react-window — the renderer and quadtree are hand-rolled because a general scene
graph is the wrong shape for 30,000 static points with a 2-bit mutable attribute. `vite-plugin-pwa`
is **not** carried over; this app is not offline-first and a service worker fighting a 1-year-immutable
CDN layout cache is a bug generator.

### The crux: one source of truth for canvas *and* hidden DOM

> Every seat has exactly one dense integer identity — its **ordinal** — which is simultaneously its
> index in the layout's columnar arrays, its index in the 2-bit bitset, its payload in the quadtree,
> its identity in every delta, and the `key` of its hidden-DOM gridcell. Ordinals run
> `section → row → seat`, so **every row and section is a contiguous range**.

```ts
interface SeatIndex {                    // built once from seats.bin, never mutated
  count, layoutVersion, venueId;
  x: Int16Array; y: Int16Array; seatNumber: Uint16Array; tierIdx: Uint8Array;
  sections: { name[], firstRow, rowCount, firstSeat, seatCount, polygon[] };
  rows:     { label[], sectionIdx, firstSeat, seatCount };   // seats = [firstSeat, +seatCount)
  tiers:    { name, priceCents, colorRgb, patternIdx };
  quadtree: FlatQuadtree;                // typed arrays, (x,y) -> ordinal
}
availability: Uint8Array                 // the ONE mutable thing, 2 bits/ordinal
```
Canvas path (worker): `queryRect(viewport)` → ordinals → read `x[o]`, `tierIdx[o]`, `getState(bits,o)`
→ draw. A11y path (main): focused row `r` → gridcells are ordinals `firstSeat[r] .. +seatCount[r]` →
read *the same* arrays and *the same* bitset accessor to build `aria-label`. **Two projections of one
structure — there is no synchronization code because there is nothing to synchronize.**

**Focus flows one way, DOM → canvas.** The hidden button takes real DOM focus (roving `tabindex`);
its `onFocus` posts `{type:'focus', ordinal}` to the worker, which draws a high-contrast double ring
at `x[ordinal], y[ordinal]` and auto-pans if off-screen. A canvas click resolves to an ordinal via the
main-thread quadtree and then calls `.focus()` on the hidden button — mouse and keyboard converge on
one code path after one step.

### Threading
- **Worker** owns: the WebSocket, frame decode, the authoritative bitset, per-section free counters,
  all painting via `OffscreenCanvas`, dirty-tile bookkeeping, the rAF loop, **and its own copy of the
  quadtree** (for viewport-rect culling during painting).
- **Main** owns: React/routing/auth/HTTP/Dexie, the `SeatIndex`, **the canonical quadtree** (so
  hit-testing and nearest-equivalent-seat are synchronous O(log n) with no round trip), a **mirror**
  bitset, the hidden DOM tree, checkout, the hold timer.
- **The quadtree is built once, on main**, from the layout binary — flat typed arrays (`Int32Array`
  bounds, `Uint32Array` child indices/seat ordinals), all transferable — and a **structurally-cloned
  copy is sent to the worker at init** (~30K seats ≈ 400 KB, a one-time copy). Both threads then query
  their own copy independently. Neither copy is ever mutated after init, so there is nothing to keep
  in sync between them.
- **No `SharedArrayBuffer`** — it requires COOP/COEP, which constrains the CDN config. Instead both
  sides apply the same ordered delta stream; the worker sends a rolling FNV-1a hash every 10 s and a
  mismatch triggers a snapshot refetch. The bounded divergence window is the honest cost.
- **The trick that keeps 30,000 seats out of React's diff:** the mirror bitset is a plain `Uint8Array`
  mutated in place, never in Zustand. `availabilityVersion` increments **once per animation frame**;
  mounted row components subscribe to it plus their own ordinal range. 5,000 deltas/sec becomes ≤60
  React updates/sec regardless of delta rate.

### The hidden a11y tree
`role="grid"` per section with `role="row"`/`role="gridcell"` wrapping real `<button>`s inside a
`visually-hidden` container (clip-path, **not** `display:none`, **not** `aria-hidden`); the `<canvas>`
gets `aria-hidden="true"`. A navigation state machine synthesizes tree behavior over the grid:
section ↑/↓, Enter drills to rows, Enter drills to seats, ←/→ within a row, Escape back up, Home/End.
**Roving tabindex — exactly one `tabindex="0"` in the whole tree**, so there are never 30,000 tab stops.

Only the focused section's rows and the focused row's (±1) gridcells are mounted (~160 nodes instead of
30,000); to keep that honest for AT, the grid carries `aria-rowcount`/`aria-colcount` and every mounted
element carries `aria-rowindex`/`aria-colindex`.

Accessible name: `"Section A, row 12, seat 4, one hundred and twenty dollars, available"` — price in
words to remove cross-screen-reader pronunciation variance. Polite live region for availability,
**debounced 4 s and aggregated** ("Twelve seats became available in Section A"); assertive region for
errors only. **Best available is the first interactive element after the page heading**, not buried
after the map — for a screen-reader user it is often the fastest route to a ticket.

**Never color alone:** FREE solid, HELD 45° hatch, SOLD dot stipple, SELECTED solid + heavy ring;
the pattern index rides as a per-instance WebGL attribute and the legend shows the pattern.

### Static layout format
Two immutable versioned files (version in the **path**, never a query string):
`/venues/{id}/{layoutVersion}/layout.json` (~20 KB metadata) and `seats.bin` (~250 KB raw / ~80 KB gz)
— a columnar blob parsed as four `TypedArray` views over one `ArrayBuffer` (a memcpy, not a parse;
30,000 seats of JSON would be ~2 MB and 50–80 ms of main-thread jank on the frame after admission).
Cached in Dexie keyed `${venueId}:${layoutVersion}`, LRU-capped at 3 venues.

**Layout version skew is a correctness hazard, not cosmetic:** if ordinals shift, every seat renders
as the wrong seat. On mismatch the client refuses to render, purges, refetches, resubscribes.

### Hold timer & checkout
`skew = serverTime - (tSend+tRecv)/2`; remaining is always **recomputed** from `expiresAt`, never
decremented. Announcements at 5:00/2:00/1:00/0:30/0:10 only. `prefers-reduced-motion` → no pulsing.
On `visibilitychange` → recompute *and* refetch (a backgrounded tab's timers are throttled). **The
client never decides the hold is dead** — at zero it asks the server.

Checkout is one resumable page, not a wizard; `GET /orders/{id}` is the resume source of truth, with
order id + idempotency key in IndexedDB. Offers apply optimistically and **revalidate at charge time
with a mandatory explicit re-confirm** if the total changed — never a silent larger charge.

**Route-level code splitting is load-bearing, not a detail:** the waiting-room chunk must load
*without* the seat-map bundle, because it is what 500,000 people load first in the same sixty seconds.
CI enforces a bundle-size budget on that chunk specifically.

---

## Terraform modules

`infra/terraform/{envs/local/main.tf, modules/<n>/{main,variables,outputs,versions}.tf}`. Constraints
in the env (`>= 1.5`, `aws ~> 5.0`), modules declare `source` only, `.terraform.lock.hcl` committed,
no backend block, placeholder creds + `skip_credentials_validation` so `validate` runs credential-free,
`us_east_1` provider alias for CLOUDFRONT-scope WAFv2.

| Module | Contents | Phase |
|---|---|---|
| `network` | VPC, 2 AZ × {public,private}, 1 NAT, SGs, VPC endpoints for s3/secretsmanager/logs/sqs/events | essential |
| `secrets` | DB creds, ElastiCache auth token, **KMS key for QR HMAC** | essential |
| `database` | Aurora Serverless v2 PG 16 (0.5–32 ACU), writer+reader, RDS Proxy, `idle_in_transaction_session_timeout=5000` | essential |
| `cache` | ElastiCache **cluster-mode disabled**, 1 primary + 1 replica, multi-AZ failover, TLS+auth | essential |
| `auth` | Cognito pool w/ `ALLOW_USER_PASSWORD_AUTH` so the real pool matches cognito-local | essential |
| `storage` | 3 buckets: `layouts` (CloudFront+OAC, immutable), `tickets` (**private, no CDN** — a CDN defeats short-lived presigned URLs), `web` (SPA 403/404→index.html) | essential |
| `compute` | Lambda `provided.al2023`/arm64, least-priv inline IAM, **`reserved_concurrent_executions` on the write path** | essential |
| `api` | HTTP API v2 + JWT authorizer + throttling, **plus a CloudFront distribution purely as a WAF attachment point** | essential |
| `websocket` | WS API, `$connect` validates the JWT from the query string | essential |
| `eventing` | Bus + per-consumer {SQS, DLQ `maxReceiveCount=5`, rule, target, ESM} via `for_each`; scheduler group | essential |
| `waf` | Managed rules + **rate-based rule scoped to `POST /api/v1/events/*/holds`**; Bot Control gated off (billed per request) | essential |
| `observability` | Explicit log groups w/ retention, dashboard, alarms | essential |
| `search` | OpenSearch, `count = var.enable_search ? 1 : 0`, **default false** | gated |
| `waitingroom` | CloudFront Function at viewer-request — the *edge* version of the queue | additive |

**Three config consequences of "Redis is never the arbiter" that belong as comments in `cache`:**
1. `snapshot_retention_limit = 0`, no AOF — a node recovering a *stale* bitset from disk is strictly
   worse than one that comes back empty and refills from Postgres.
2. `maxmemory-policy = noeviction`, not `allkeys-lru` — LRU-evicting a hold key silently frees a held
   seat, a correctness bug disguised as a cache tuning knob.
3. Automatic failover can lose the last seconds of writes, so an in-flight `SET NX PX` hold can vanish
   — which is fine, and is exactly why the Postgres CAS is the truth.

**Terraform/runtime boundary to state explicitly:** Terraform creates the scheduler *group*, the target
Lambda, and the invoke role. The **individual one-shot hold-expiry schedules are created at runtime by
the Order Service** — or someone will try to model 50,000 schedules in HCL.

**Alarms mapped to `script.md`'s on-the-wall metrics:** hold conflict rate (metric-math over two log
metric filters, threshold **20%** from deep-dive §3), saga compensation count (`Sum > 0` over 15 min),
WS delta lag p99 > 2000 ms (tied to the stated "two-second stale view is acceptable" NFR), DLQ depth,
conditional-write p99 > 500 ms, plus RDS Proxy `DatabaseConnectionsCurrentlySessionPinned > 0` and
ElastiCache `Evictions > 0` (**critical**, given `noeviction`).
**The best single alarm in the system:** a scheduled overbooking canary running the "two BOOKED rows
for one seat" query, Sev-1 on any row. It should never fire.

---

## Phased roadmap

Legend as in the sibling: `[ ]` `[~]` `[x]` `[!]`, and **every `[x]` records how it was verified with
real output — never "should work."** `MILESTONES.md` is a Phase 0 deliverable.

### Phase 0 — Scaffolding *(blocking)*
Go module, `cmd/server` + `/health`, `docker-compose.yml`, Makefile (`dev-*`, `test`,
`test-integration`, `compose-up/down`, `migrate`, `sqlc-generate`, `build`, `build-lambda`,
`tf-validate`), `internal/config`, `.env.example`, Vite skeleton, `infra/terraform/envs/local`
skeleton, `MILESTONES.md`, and `.github/workflows/ticketing-ci.yml` (path-filtered to
`ticket-booking/**`, `working-directory: ticket-booking`, jobs go / web / terraform-validate + a
placeholder zero-byte lambda zip — mirroring `fieldsync-ci.yml`; **no plan/apply/deploy**).
**Verify:** `curl localhost:8080/health`; `redis-cli ping` → `PONG`; `terraform init && validate` clean.

### Phase 1 — Auth *(blocking)*
Port `internal/auth` (JWKS/RS256/iss/aud/token_use), idempotent `scripts/seed-cognito-local.sh`,
`/api/v1/whoami`.
**Verify:** real token → real sub+email pasted into MILESTONES; 5 unit tests (valid/missing/expired/
wrong-aud/wrong-token_use).

### Phase 2 — Catalog + seat-map read path *(blocking)*
Migrations 0001–0002; sqlc; `scripts/seed-venue/` building a **30,000-seat venue with dense ordinals**
producing byte-identical `layout.json`/`seats.bin` fixtures for backend *and* frontend tests;
`internal/seatmap` bitset codec; catalog + availability + layout + pricing endpoints (DB-scan path,
no Redis yet). `pg_trgm` GIN index for search. TF: `network`, `secrets`, `database`.
**Verify:** `EXPLAIN ANALYZE` shows an **Index Only Scan** on `event_seats_cover_idx` (paste the plan);
availability response is ~7,500 bytes for 30k seats; measured p50/p95 recorded as real numbers.

### Phase 3 — Hold / release / confirm CAS — **the keystone** *(blocking)*
`internal/holdlock` (SET NX PX + Lua compare-and-delete), `internal/inventory`, migration 0003
`holds_audit`, hold/release/best-available routes, the 409 mapping. TF: `cache`.
**Verify — the project's thesis, proven not asserted:**
1. `TestAcquireHold_ExactlyOneWinnerUnderRace` — 200 goroutines released from one `close(start)`
   barrier against real Postgres + Redis. Assert 1 win / 199 `SEAT_TAKEN`, then query the DB
   *directly*: `status=1` count → 1, `count(DISTINCT hold_id)` → 1, `holds_audit` `ACQUIRED` → 1.
   **Repeated 20× as subtests — a race test that passes once proves nothing.**
2. `..._WithRedisFlushed` — same, with a goroutine running `FLUSHALL` every 5 ms throughout. Still
   exactly one winner. **This is the test that proves Postgres, not Redis, is the arbiter.**
3. `TestConfirm_ExactlyOneWinner`, `TestExpiredHoldIsReclaimed` (raw SQL sets the clock back, Redis
   untouched — proves passive expiry alone suffices), `TestStaleFenceRejected`.
4. Multi-seat overlap: two 4-seat requests sharing 1 seat → one full success, one clean rollback, and
   crucially **the loser's other 3 seats are AVAILABLE, not orphaned**.
5. Gomock unit tests for control flow; mocks committed with the `mockgen` command in the header.

### Phase 4 — Canvas seat map + a11y tree *(blocking for the product story)*
`SeatIndex`, flat quadtree, LOD with incremental `sectionFreeCount`, Canvas2D + WebGL renderers behind
one interface, the worker + `OffscreenCanvas`, the hidden `role="grid"` tree, roving tabindex nav,
live regions, best-available, Dexie layout cache. Map polls `/availability` until Phase 7.
**Verify:** see the frontend verification section below — all of 5.1 F3/F4, 5.2 (a)–(g), 5.3 tests 1–5.

### Phase 5 — Expiry side effects: outbox + reaper *(blocking)*
Migration 0006; `internal/events.Publish` inside every inventory transaction; `outbox-relay` → moto
EventBridge; `hold-reaper` + `internal/scheduler` (local ticker / Scheduler one-shot). Map deep-dive
§2's **8 mandatory side effects of hold expiry** to concrete code in a MILESTONES table.
**Verify:** hold with `HOLD_TTL=10`, **stop the reaper**, `status` still `1` at t+15 s, yet a fresh
`POST /holds` **succeeds** (paste both — this demonstrates passive expiry is the guarantee). Restart
the reaper: status flips to 0 within a tick, a `seat.released` row appears in `domain_events`, then
flips to published after a relay tick — all as real `SELECT` output. Reaper run twice on a confirmed
seat → 0 rows, still BOOKED.

### Phase 6 — Payment saga, idempotency, reconciler + checkout page *(blocking)*
Migrations 0004/0005; fake provider with injectable pathology; orchestrator + `order_saga_steps`;
`saga-worker`; `reconcile`; order routes; the resumable checkout page.
**Verify:** happy path curl → `TICKETED`, seat `status=2`. Two concurrent `POST /orders` with the same
`Idempotency-Key` → one 202, one replayed identical body, `count(payments)` → **1**. Forced
expiry-after-capture → asserts **both** branches (`REALLOCATED` and `COMPENSATED` + refund row).
`PAYMENT_AMBIGUOUS_RATE=1.0` → orders stall → run the reconciler → every one resolves. **Both money
invariant queries return 0 rows, pasted.** Playwright: refresh mid-checkout and assert resume; offer
rejected at charge requires explicit re-confirm and does **not** auto-proceed.

### Phase 7 — Realtime: projector + WS deltas *(blocking for the frontend; additive for correctness)*
`projector` → Redis bitset + `seq` + capped delta ring; `internal/wshub`; `notify`; `/ws`; migration
0007; availability now reads Redis with DB fallback. **Local hub enforces a 10-min idle timeout, a
128 KB frame cap, and ~1% random disconnects** — the highest-value fidelity patch in the stack, because
it exercises reconnect/resync on every local run instead of in prod. Client-side resync + rolling hash.
**Verify:** a Go WS client receives a `0x01` snapshot, another process holds a seat, the client receives
a `0x02` at `seq+1` — **paste the actual received bytes**, not a description. Gap handling: kill the
socket, hold 3 seats, reconnect with `sinceSeq` → one delta covering all 3; overflow the ring → exactly
one resync (not a storm). `docker stop redis` → `/availability` still 200s from Postgres (paste both).

### Phase 8 — Virtual waiting room + AIMD *(blocking for the on-sale story)*
`internal/waitingroom`, queue routes, `X-Admission-Token` middleware, the controller, migration 0009,
the waiting-room route as its own chunk.
**Verify:** 5,000 arrivals in 1 s; `POST /holds` without a token → `403`; measured hold RPS at the
inventory layer stays within ±20% of `:rate`; clamp the pgx pool to 5 and show the AIMD rate **halving
within 3 ticks**, plotted from `waiting_room_audit` with real numbers. Tamper a signature byte → 403;
replay another user's token → 403 (sub mismatch).

### Phase 9 — QR ticketing + reminders *(additive)*
Migration 0008; HMAC-signed payload behind a `Signer` interface (env key locally, KMS in prod); QR
render → MinIO; short-TTL presign; `POST /gate/redeem`; T-24h/T-2h one-shots.
**Verify:** `aws s3 ls` against MinIO shows the object, download it and **decode the QR** to prove real
image bytes (the sibling's "not just 'a file exists'" bar). Tamper one payload byte → 403; redeem twice
→ 409; expired presigned URL → 403 and the client refetches rather than showing a broken image.

### Phase 10 — Load harness + measured numbers *(additive, but the source of every number quoted)*
Extend `scripts/loadtest`: arrival ramp, configurable contention (all racers on one seat ↔ uniform
across 30k), the full browse→queue→hold→order→ticket journey, CSV output.
**Verify:** one canonical scenario committed with pasted output — hold p50/p95/p99, conflict rate,
hold→purchase conversion, saga compensation count, WS delta lag p95 — plus
`SELECT event_id,seat_id,count(*) FROM tickets WHERE revoked_at IS NULL GROUP BY 1,2 HAVING count(*)>1`
→ **0 rows**. Labeled "measured on dev hardware — a relative regression baseline, not a production
capacity claim," as the sibling does.

### Phase 11 — IaC completion + Lambda twins *(additive)*
Remaining modules (`compute`, `api`, `websocket`, `eventing`, `waf`, `observability`, gated `search`);
every `cmd/*` gets its `-lambda` twin; `make build-lambda` cross-compiles arm64 `bootstrap` + zips into
each module's `build/`.
**Verify:** `make tf-validate` green across all modules (pasted); every twin cross-compiles.
`terraform plan` is **never** run automatically — noted explicitly.

### Phase 12 — DynamoDB comparison spike *(explicitly non-blocking, explicitly not dual-maintained)*
`cmd/dynamo-spike` implementing `script.md`'s model against `amazon/dynamodb-local`: `UpdateItem` with
`ConditionExpression: status = :free OR (status = :held AND hold_expires_at < :now)`,
`TransactWriteItems` for multi-seat, jittered backoff on `TransactionCanceledException`. Also evaluates
the DynamoDB WS connection registry deferred in decision #7.
**Verify:** the *same* Phase 10 scenario run against both paths, with a committed `docs/` table of real
p50/p95/p99, conflict rate and cost-shape notes, plus the "exactly one winner" race test passing against
DynamoDB. The conclusion must state which properties transfer and which don't (single-item atomicity is
free in Dynamo; multi-item costs 2× WCU and fails hard under contention; Postgres gives you the outbox in
the *same* transaction, Dynamo needs Streams). Nothing from this phase wires into `cmd/server`.

### Phase 13 — Frontend UX repair *(additive; scoped to `web/`, no new screens)*

**Why:** `web/` is a single commit (Phase 4) and nothing from Phases 6–8 ever reached it. Opening the
app shows a bare "Ticketing" heading and a Sign-up form with no path to Sign-in — a returning user hits
`UsernameExistsException` and is structurally stuck, because `App.tsx`'s auth state machine defaults to
`signUp` and only ever transitions `signUp → confirm → signIn`, and `signedIn` is local `useState` with
no session restore, so every reload logs the user out regardless. This phase fixes usability —
information architecture, flow, state coverage, and copy — **not visual design**; CSS is explicitly out
of scope for this phase.

**Architecture verdict:** the model layer (`seatIndex`/`bitset`/`quadtree`, the a11y tree as a peer
projection of the same arrays, the server-authoritative `HoldTimer`) is correctly built and unit-tested.
What's missing is the application shell around it: `react-router-dom` and `zustand` are installed
(`package.json`) but **never imported anywhere in `src/`**; `ApiError.code` is captured by `api/client.ts`
but discarded by every caller (`setError(String(e))`); and `EventPage.tsx`'s loading state and error state
are conflated, so a load failure hangs on "Loading event…" forever with the real cause visible only to
screen readers (the error region is `className="visually-hidden"`).

**Scope, confirmed with the user:**
- Repair what exists; no new screens (no checkout, ticket/QR, or waiting-room pages — those stay the
  documented Phase 6/8 frontend gaps, deferred).
- Browse without an account; authenticate only at the point of reserving a seat — `GET /events`,
  `/events/{id}`, `/availability`, `/pricing`, and the layout are already public routes in `router.go`,
  only `POST .../holds` requires a JWT, so gating the whole app behind sign-up is stricter than the API
  and worse UX.
- **Correction made during implementation:** `RequireAdmission` (`internal/waitingroom/middleware.go`)
  gates every `POST .../holds` **unconditionally** — it is not specific to real on-sale contention, so
  reserving seats was never actually optional-waiting-room-only as first assessed. The frontend must
  call `POST /events/{id}/queue` before every hold and send the resulting token as `X-Admission-Token`,
  which in turn requires widening `router.go`'s CORS `AllowedHeaders` (was missing `X-Admission-Token`,
  so the browser preflight for every hold request failed closed) — the one backend change this phase
  needed after all, made surgically rather than speculatively once the gap was confirmed to actually
  block the feature. `Idempotency-Key` and the order response's missing `ticketIds` remain deferred —
  they block the checkout/ticket screens, which stay out of scope here.

**Work:**
1. **Auth** (`App.tsx`, `auth/useAuth.ts`): restore session on mount instead of local `useState`; default
   to Sign-in with a working link to Create-account and back; real `<label>`s, `type`, `autoComplete` on
   every input; disable-on-submit; map Cognito exceptions to plain copy — `UsernameExistsException`
   specifically offers "Sign in instead" with the email preserved; surface signed-in identity and a
   working Sign-out (the existing `logout()` is never called today).
2. **Browse-then-auth** (`App.tsx`, `EventPage.tsx`): render the event page unconditionally; gate only
   the reserve/best-available actions on auth, preserving the in-progress seat selection through
   sign-in and auto-retrying the action once signed in.
3. **Seat selection UX** (`EventPage.tsx`): split `loading`/`ready`/`error` explicitly (today's `.catch`
   sets only `error`, never resolves the loading state) with a visible retry; add a visible error region
   alongside the existing `aria-live` one; show a selection summary with per-seat price and subtotal; make
   the silent 8-seat cap (`toggleSeat`) explain itself; replace the hardcoded "2 seats" best-available with
   a quantity picker; reconcile `selection` against each 5s availability poll so a sold seat is dropped and
   announced instead of only failing at hold time.
4. **Error taxonomy** (new `api/errorCopy.ts`): map `ApiError.code` — `SEAT_TAKEN`, `HOLD_EXPIRED`,
   `no_contiguous_seats`, `too_many_seats`, `gone`, `forbidden`, `unauthorized`, `internal`, network
   failure — to plain-language copy; `SEAT_TAKEN` must read as normal/recoverable, not a crash, since it's
   the dominant non-2xx during an on-sale (decision already stated in the API contract above).
5. **Hold lifecycle** (`EventPage.tsx`, `hold/HoldTimer.tsx`): handle expiry by asking the server
   (`getHold`) rather than leaving a dead timer on screen, per "the client never decides the hold is dead"
   above; label the timer visibly; add a warning state under 2:00.

**Verify:** real running stack (`docker compose up -d`, `make dev-server`, `npm run dev`). Sign in, hard
reload → still signed in. Signed-out load → seat map renders and is keyboard-navigable; attempting to
reserve prompts sign-in, preserves selection, auto-completes the reserve after sign-in. Stop `cmd/server`,
reload → visible error + working retry, not a permanent "Loading event…"; restart, retry → recovers.
Hold a seat via `curl` under a second identity, then attempt it in the UI → named seats deselected, plain
message, zero console errors. Run with `HOLD_TTL=20`, reserve, let it expire → UI asks the server, clears
the hold, explains. `npm test` (37 vitest model tests) and `npm run e2e` (`e2e/hold-flow.spec.ts`, which
asserts zero console errors and drives the a11y tree keyboard-only) both stay green against the new
labels/copy.

---

## Frontend verification standard

Inherited verbatim from the sibling: **full browser E2E with Playwright headless Chromium, screenshots
checked, zero console errors** — a global fixture registers `page.on('console')`/`page.on('pageerror')`
and fails the test on any error-level message.

**The a11y tree needs five layers; no single tool proves it:**
- **axe** (`@axe-core/playwright`) — necessary and **explicitly insufficient**. It confirms the ARIA is
  well-formed; it cannot tell you the arrow keys work.
- **Keyboard-only walkthrough — the test that actually proves the claim.** One test that never calls
  `.click()` or `.fill()` on the map: Tab → ArrowDown to Section A → Enter → ×11 to Row 12 → Enter → ×3
  to seat 4 → Enter → all the way to a CONFIRMED order, **asserting `document.activeElement`'s
  `aria-label` equals the expected string exactly at every step.** Companion: press Tab 50× and assert
  focus never lands in a gridcell twice — proving there are not 30,000 flat tab stops.
- **Exact-string accessible-name assertions** per state variant, plus `aria-rowcount`/`aria-rowindex`
  (the virtualization-honesty check), plus a committed golden `page.accessibility.snapshot()`.
- **Live-region behavior including the debounce:** inject **500 deltas over 10 s**, assert the polite
  region updated **≤ 3 times**; assert lost-race errors land in the assertive region and never the polite.
- **Canvas focus-ring sync, pixel-level:** focus a seat by keyboard, compute the expected screen
  coordinate from the transform, read the canvas back and assert the ring pixels are within ±2 px.
- **Manual VoiceOver pass per phase, labeled as such.** Automated tests verify structure and names;
  they do not verify the screen-reader experience.

**Rendering perf at 30,000 seats — nothing is "it feels smooth":**
1. 300 frames of scripted pan/zoom → p95 frame < 16.7 ms, p99 < 33 ms, dropped < 5%.
2. **CDP trace ground truth:** `Tracing.start` with `devtools.timeline` → assert **zero main-thread long
   tasks (>50 ms)** during interaction. Self-reported worker timings cannot prove "the main thread stays
   free"; a trace can.
3. Culling is real: `count === 30000` **and** `seatsDrawn < 1000` at default zoom.
4. Hit-testing: 10,000 random hits < 50 ms total (a linear scan would be ~3 orders slower — cannot pass by accident).
5. **DOM ceiling: `document.querySelectorAll('*').length < 2000`** with the full map loaded — the direct
   proof that both the canvas decision and the a11y virtualization held.
6. Delta throughput: 5,000/s for 10 s, final client bitset hash equals the server's, one dropped `seq`
   triggers **exactly one** resync.
7. Re-run 1–3 at `Emulation.setCPUThrottlingRate: 4` as the mobile proxy.

**Perf caveats, written down rather than discovered:** run perf assertions against **Canvas2D** in CI —
headless Chromium runs WebGL on SwiftShader, so WebGL numbers from CI are meaningless, and Canvas2D is
the slower path so passing there is conservative. Assert only WebGL *correctness* (pixels match 2D within
tolerance) in CI. The perf job is **separate and non-blocking on PRs** with absolute thresholds plus a
relative check against a committed baseline — a flaky perf test everyone learns to re-run is worse than none.

---

## Fidelity gaps and risks — in `MILESTONES.md` on day one

**Emulator stack, ranked:**
1. **moto is control-plane only** — it creates buses, rules, targets and schedules but **never delivers
   an event or fires a schedule**. All local eventing is a `domain_events` poller and hold expiry is a Go
   ticker. *Architecturally, this gap maps exactly onto the design's own "correctness never depends on a
   timer firing"* — it costs freshness, never correctness.
2. **No API Gateway WebSocket** — no 128 KB frame limit, 10-min idle timeout, 2-hour max duration, or
   `GoneException`. Mitigated by making the local hub enforce those (Phase 7).
3. **No RDS Proxy** — session pinning is invisible locally. Mitigation: keep hold/confirm to a single
   short statement, no session state, plus the pinning alarm.
4. **No ElastiCache slot semantics** — mitigated by hash-tagging every per-event key from day one.
5. **No Aurora Serverless v2 scaling** — local load numbers are optimistic by construction.
6. **No CloudFront/WAF** — the entire edge bot-and-admission story is untestable locally; the queue
   *mechanics* are testable, its *edge placement* is not.
7. cognito-local's claims are a subset of real Cognito's; no SRP, no MFA.

**Design/system:** single-node Postgres and Redis (no failover, replication lag, or split-brain — all of
deep-dive §9 is design-only); no actual sharding (`event_id` is documented as the shard key and the schema
is shaped for it, but one Postgres holds everything); the payment provider is fake; the AIMD loop reads one
process's metrics (multi-replica aggregation is a CloudWatch concern); no bot defense beyond rate limits
and unguessable tokens; `home_region` exists and is read but nothing routes on it.

**Two honest self-criticisms worth stating rather than hiding:**
- **`fence_token` is arguably redundant** given a UUID `hold_id` in the same `WHERE` clause. It is kept
  because it is the correct answer to the RedLock question and gives an auditable per-seat generation
  counter, and it costs one column — not because it is load-bearing.
- **The Redis pre-check costs two round trips on the happy path.** Justified by the loser path being one
  Redis round trip instead of a DB transaction, which dominates during an on-sale — but **Phase 10 must
  actually measure it.** If the harness shows it doesn't buy throughput at realistic contention, say so and
  consider dropping to a pure-Postgres path. That would be an honest measured result, not a failure.

**Frontend:** `OffscreenCanvas` + WebGL2-in-worker on Safari is the biggest portability risk and headless
Chromium will never reveal it (a main-thread fallback renderer is selected by feature detection and tested
with OffscreenCanvas stubbed out; **Playwright/Chromium does not verify Safari or iOS**). The virtualized
`role="grid"` with `aria-rowcount` is the riskiest a11y bet — spec-correct but inconsistently supported;
pre-planned fallback is to stop virtualizing at the row level. A synthetic 30,000-seat fixture is uniform
and real venues are not, so **add a deliberately lopsided fixture** (70% of seats in 20% of the area) so
quadtree balance is tested rather than assumed.

---

## Files created

All new. Key ones: `ticket-booking/MILESTONES.md`, `docker-compose.yml`, `Makefile`, `go.mod`,
`internal/inventory/inventory.go` (the keystone), `internal/holdlock/`, `internal/order/`,
`internal/seatmap/`, `sqlc/queries.sql`, `migrations/0000{1..9}_*.sql`,
`web/src/seatmap/model/seatIndex.ts`, `web/src/seatmap/a11y/`, `infra/terraform/modules/*`,
`.github/workflows/ticketing-ci.yml` (repo root, path-filtered).

Existing files touched: the repo-root `README.md` gains a line for `ticket-booking/` alongside
`offline-sync-app/` (it currently mentions neither).

## Verification summary

Per-phase gates are above. The three that matter most, all of which produce **pasted real output** in
`MILESTONES.md`:

1. `make test-integration` — the racing tests, especially `..._WithRedisFlushed`, repeated 20×.
2. The two money-invariant SQL queries returning **0 rows** under injected payment pathology.
3. The keyboard-only Playwright walkthrough asserting exact `aria-label` strings from map to
   CONFIRMED order, plus `querySelectorAll('*').length < 2000` at 30,000 seats.
