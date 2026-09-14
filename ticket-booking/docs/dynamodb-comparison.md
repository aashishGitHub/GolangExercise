# DynamoDB comparison spike (Phase 12)

**Status: explicitly non-blocking, explicitly not dual-maintained.** `cmd/dynamo-spike` is a standalone
package under `cmd/`, not `internal/` — nothing here is imported by `cmd/server` or any other
production binary in this repository, and nothing in this document argues for switching. The
project's locked decision (`docs/plan.md` decision #1) remains: Postgres is the seat-state arbiter.
This spike exists to make that decision's tradeoffs *provable with real numbers* rather than argued
from first principles alone, evaluating `docs/script.md`'s alternative model — DynamoDB conditional
writes — against `amazon/dynamodb-local` (real DynamoDB API semantics: `ConditionalCheckFailedException`,
`TransactWriteItems`, `TransactionCanceledException` — not a Postgres-behind-a-facade stand-in).

## The model implemented

`docs/script.md`'s design, for real, in `cmd/dynamo-spike/hold.go`:

- **Single-seat hold** — one `UpdateItem` with a condition expression: `status` is `FREE`, or `status`
  is `HELD` and `hold_expires_at` is in the past. Expiry is **read-time**, exactly like
  `docs/plan.md` decision #2's Postgres equivalent (`hold_expires_at < now()` in the `WHERE` clause) —
  never DynamoDB TTL, which is background/lazy and unsuitable as a correctness mechanism (`docs/
  script.md`'s own stated reasoning).
- **Multi-seat hold** — `TransactWriteItems`, one conditional `Update` per seat, all-or-nothing.
  `ConditionalCheckFailedException` inside a cancelled transaction is treated as a real conflict (not
  retried); anything else triggers jittered exponential backoff, per `docs/script.md`.
- **Confirm** — a second `TransactWriteItems` flipping `HELD → BOOKED`, conditioned on `hold_id` still
  matching. A zombie confirm after a reclaim+re-hold has a stale `hold_id` and fails the condition —
  DynamoDB's native conditional write gives this for free, no separate fence-token column needed
  (Postgres's `fence_token` exists specifically because a UUID `hold_id` alone in a `WHERE` clause is
  *arguably* redundant with the fence, `docs/plan.md`'s own stated self-criticism; in DynamoDB's model
  the `hold_id` condition genuinely is sufficient by itself).

## The race test — the same thesis, the same proof shape

`cmd/dynamo-spike -mode=race`: 200 goroutines released from one `close(start)` barrier, racing the
same single seat, repeated 20× — the identical shape to Phase 3's
`TestAcquireHold_ExactlyOneWinnerUnderRace`, because a race test that passes once proves nothing.

```
$ dynamo-spike -mode=race
race round 1/20: wins=1 conflicts=199 [PASS]
race round 2/20: wins=1 conflicts=199 [PASS]
...
race round 20/20: wins=1 conflicts=199 [PASS]
race test: 20/20 rounds passed — exactly one winner every time, DynamoDB's native conditional write
```

**20/20 rounds, exactly one winner every time.** DynamoDB's per-item conditional write gives the same
"a seat is never sold twice" guarantee Postgres's row-lock CAS gives — this is the property that
*does* transfer, proven the same way (a race, not an assertion).

## Load scenarios — the same shapes as Phase 10, not the same harness

**Methodology note, stated honestly:** `docs/plan.md`'s Phase 12 verification asks for "the *same*
Phase 10 scenario run against both paths." Phase 10's `scripts/loadtest` drives the FULL real HTTP
journey (`browse → queue → hold → order → poll-to-TICKETED`) through `cmd/server`. This spike is
explicitly **not wired into `cmd/server`** (`docs/plan.md`'s own instruction for this phase), so
literally reusing that harness is impossible by design. What transfers is the *scenario shape* —
concurrent workers, uniform-vs-hot contention, measured p50/p95/p99 and conflict rate — driven
directly against `AcquireHoldSingle`/`AcquireHoldMulti`, the same "exercise the mechanism directly"
pattern `scripts/queue-loadtest` (Phase 8) already established when going through HTTP+auth would
measure something else entirely. The numbers below are therefore a **hold-acquisition-only**
comparison, not an apples-to-apples full-journey comparison against Phase 10's Postgres numbers —
stated here rather than left for a reader to assume.

All four runs: 50 workers, 15s duration, 30,000-seat pool, `amazon/dynamodb-local` on the same laptop
Phase 10's Postgres numbers were measured on. **Measured on dev hardware — a relative regression
baseline, not a production capacity claim or a real-DynamoDB-service capacity claim** (`dynamodb-local`
has no real partition/replication behavior at all).

| Scenario | Attempts | p50 / p95 / p99 (ms) | Conflict rate |
|---|---|---|---|
| Single-item (`UpdateItem`), uniform | 78,590 | 11.40 / 15.31 / 17.54 | 64.6% |
| Single-item (`UpdateItem`), hot | 97,149 | 9.78 / 11.55 / 12.78 | 100.0% (1 winner) |
| 4-seat transaction (`TransactWriteItems`), uniform | 15,926 | 46.82 / 57.98 / 66.00 | 68.9% |
| 4-seat transaction (`TransactWriteItems`), hot | 21,918 | 35.19 / 39.20 / 56.48 | 100.0% (1 winner) |

Verified directly against the table after the hot 4-seat run: all four contended seats (`seat_id`
0-3) held by the exact same `hold_id` — `TransactWriteItems`' atomicity held under real concurrent
load, not just in the single-round race test.

```
$ aws dynamodb get-item ... --key seat_id=0   ->  hold_id: 798d0413-...  status: HELD
$ aws dynamodb get-item ... --key seat_id=1   ->  hold_id: 798d0413-...  status: HELD   (SAME hold_id)
$ aws dynamodb get-item ... --key seat_id=2   ->  hold_id: 798d0413-...  status: HELD   (SAME hold_id)
$ aws dynamodb get-item ... --key seat_id=3   ->  hold_id: 798d0413-...  status: HELD   (SAME hold_id)
```

**Reading the uniform-contention conflict rates honestly:** 64.6%/68.9% is higher than Phase 10's
Postgres uniform run (14.2%) — but this is a methodology artifact, not a DynamoDB weakness: this
harness never *releases* a hold once acquired, so the 30,000-seat pool depletes monotonically over
the 15-second window at ~4,500-8,700 attempts/sec, and conflict rate climbs as the free pool shrinks.
Phase 10's Postgres run went through the full HTTP journey at a much lower attempt rate (2,557 total
over 20s, because it pays for JWT verification, the waiting-room queue check, and order creation on
top of the hold itself) and never came close to exhausting the pool. The two numbers measure
different things and are not directly comparable — the number that *is* directly comparable is the
**single-item p50 (~11ms) vs the 4-seat transaction p50 (~47ms) on the exact same table, same
workers, same run**: **TransactWriteItems costs roughly 4x the latency of a single conditional
UpdateItem** in this measurement, consistent with `docs/script.md`'s own stated "transactions cost
double the write units" (2x WCU is a cost-shape claim, not a latency one, but the latency overhead
of coordinating N conditional writes atomically instead of one is real and measured here).

## What transfers and what doesn't — the required conclusion, stated plainly

**Transfers cleanly:**
- **Single-item atomicity is free.** A `ConditionExpression` on one `UpdateItem` gives DynamoDB
  exactly the same "one winner, structurally" guarantee Postgres's row lock gives — proven by the
  identical 20/20 race-test result. Neither engine's single-seat path needs help from Redis, a fence
  token, or anything else.
- **Passive, read-time expiry is the correct pattern in both engines**, and for the identical reason:
  a background TTL/reaper is a freshness mechanism, never a correctness one, in DynamoDB exactly as
  much as in Postgres.
- **A stale `hold_id` alone is sufficient to reject a zombie confirm** in DynamoDB's model — Postgres
  additionally carries a `fence_token` for the same purpose, which `docs/plan.md` already flags as
  "arguably redundant given a UUID `hold_id` in the same WHERE clause." This spike is a second,
  independent data point for that self-criticism: DynamoDB's equivalent design needed no fence column
  at all and lost nothing.

**Doesn't transfer / costs more:**
- **Multi-item atomicity is not free.** `TransactWriteItems` measured ~4x the latency of a single
  conditional write in this harness, and `docs/script.md`'s own doubled-WCU cost-shape claim is a
  second, separate cost on top of that. A per-seat loop without transactions would be cheaper but
  gives up cross-seat atomicity entirely — exactly the tradeoff `docs/plan.md` decision #1 already
  named as the reason a multi-seat hold is a natural fit for a single Postgres transaction instead.
- **The outbox pattern is not free in DynamoDB.** Postgres's transactional outbox (`internal/events`,
  Phase 5) writes the `domain_events` row in the *same* transaction as the seat-state change — one
  engine, one commit, no separate infrastructure. DynamoDB has no equivalent "write two items in one
  ACID unit with an external system" primitive within a single table operation; the standard pattern
  is DynamoDB Streams triggering a separate Lambda, which is a real additional moving part (a stream
  consumer, at-least-once delivery semantics to design around, eventual rather than same-transaction
  consistency) that this project's Postgres path gets from one `INSERT` inside an existing
  transaction. Not evaluated further in this spike (out of scope — `docs/plan.md` decision #7's own
  DynamoDB-connections-registry discussion is the closer analog already written down), but stated
  here because it's the single largest asymmetry this comparison surfaced.
- **Cost shape is genuinely different**, not just numerically: DynamoDB is pay-per-request with no
  idle cost, which is attractive for a spiky on-sale traffic pattern; Aurora Serverless v2 (this
  project's actual prod target, `docs/plan.md` "database" module) scales 0.5–32 ACU but still carries
  a real floor cost even at minimum. Neither this spike nor `dynamodb-local` measures real production
  cost — this is a shape observation, not a number.

## What was NOT evaluated

The WS connection registry alternative `docs/plan.md` decision #7 flags for this phase's
consideration (DynamoDB's high-churn per-connection TTL write pattern vs Postgres's `ws_connections`
table, Phase 7) is not implemented or measured here — the argument is written down in decision #7
itself and is genuinely stronger for DynamoDB than the equivalent sibling-project decision was, but
proving it would need a second, separate connection-churn harness this phase's time budget didn't
reach. A real gap, not a "should work."
