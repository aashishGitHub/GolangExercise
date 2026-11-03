# Phase 3 — Hold / Release / Confirm (CAS): Questions

Siblings: [BASICS.md](BASICS.md) · [FILES.md](FILES.md) · [learn index](../README.md)

## L1 Beginner

**Q1. What is the difference between a hold and a booking, and why does a hold have a TTL?**
A hold is a temporary claim while the buyer pays. A booking is permanent after payment. The TTL (default 600 seconds, `HOLD_TTL_SECONDS`) stops an abandoned checkout from blocking a seat forever. Seat states are 0 available, 1 held, 2 booked, 3 pending payment, see [migrations/000002_event_seats.up.sql](../../../migrations/000002_event_seats.up.sql). Theory: [deep-dive.md](../../deep-dive.md) section 2.

**Q2. What is compare-and-swap, and how does a SQL `UPDATE` do it?**
CAS changes a value only if it still matches what you expect, as one atomic step. The `AcquireHold` statement is an `UPDATE ... WHERE status = 0 OR (held AND expired)`. If another buyer changed the row first, the `WHERE` no longer matches and your row count is lower. See `AcquireHold` in [sqlc/queries.sql](../../../sqlc/queries.sql).

**Q3. What do Redis and Postgres each do in a hold?**
Redis (`SET NX PX`) rejects most losers fast. Postgres decides who actually wins. Redis can be wrong, empty, or down and no seat is double-booked. See [internal/holdlock/holdlock.go](../../../internal/holdlock/holdlock.go) and [internal/inventory/inventory.go](../../../internal/inventory/inventory.go).

**Q4. Why does releasing an already-released hold return success?**
Release is idempotent. If the hold already expired, was reclaimed, or was confirmed, row count 0 means "nothing to do", not failure. The caller's goal, "this hold no longer blocks the seat", is already true. See `ReleaseHold` in [internal/inventory/inventory.go](../../../internal/inventory/inventory.go).

## L2 Junior

**Q5. Trace `POST /api/v1/events/1/holds` with two seat ordinals, from router to the CAS.**
1. [internal/httpapi/router.go](../../../internal/httpapi/router.go): auth middleware, then `RequireAdmission` if a queue is wired, then `holds.createHold`.
2. [internal/httpapi/holds.go](../../../internal/httpapi/holds.go): `resolveOrdinals` turns ordinals into seat IDs through `GetSeatIDsByOrdinals`.
3. `inv.AcquireHold`: `sortDedup`, validate, `uuid.New()`.
4. `lock.Acquire` per seat (Redis).
5. `pool.Begin`, `qtx.AcquireHold` (the CAS), compare `len(rows)` to `len(seatIDs)`.
6. `events.Publish("seat.held")` per seat, `tx.Commit`, audit `ACQUIRED`.
7. Handler returns 201 with `holdResponse`.

**Q6. Two users hold the same seat. What does the loser see, and which path catches them?**
If Redis is up, the loser's `SetNX` returns false, so `AcquireHold` audits `LOST_REDIS` and returns `ConflictError`. The handler maps it to 409 `SEAT_TAKEN`. If Redis let both through, the second CAS updates 0 rows, the code rolls back, audits `LOST_DB_CAS`, and returns the same 409. See `writeInventoryError` in [internal/httpapi/holds.go](../../../internal/httpapi/holds.go).

Try it:
```
curl -s -o /dev/null -w "%{http_code}\n" -X POST localhost:8080/api/v1/events/1/holds
```
(Without a token this returns 401. A winning hold needs a Cognito ID token and an `X-Admission-Token`.)

**Q7. How does a seat whose hold expired become holdable again with no background job?**
The `AcquireHold` `WHERE` clause includes `(status IN (1,3) AND hold_expires_at < now())`, so an expired hold counts as free to the next `UPDATE`. Test: `TestExpiredHoldIsReclaimed` seeds an expired row by raw SQL and shows a fresh `AcquireHold` succeeds with no reaper and no Redis key. See [sqlc/queries.sql](../../../sqlc/queries.sql) and [internal/inventory/integration_test.go](../../../internal/inventory/integration_test.go).

**Q8. Which status codes can the hold routes return and what does each mean?**
201 hold created. 409 `SEAT_TAKEN` lost the race (body has `conflicts`). 409 `HOLD_EXPIRED` for an extend after lapse. 409 `no_contiguous_seats`. 422 `too_many_seats` or `duplicate_seat`. 410 `gone` for a missing or foreign hold on `GET`. 204 for delete. All in `writeInventoryError` and the handlers in [internal/httpapi/holds.go](../../../internal/httpapi/holds.go).

## L3 Mid

**Q9. Why a Postgres CAS and not a Redis lock as the source of truth?**
A Redis lock can be lost on failover, flushed, or held past its TTL by a paused client, and then two people think they own the seat. The Postgres row is the one durable record, and the `UPDATE` is atomic inside it. Redis stays as a cheap filter. The hold TTL expiry and the confirm check both live in the same row, so there is no cross-system race to reason about. Theory: [deep-dive.md](../../deep-dive.md) sections 3 and 4 (RedLock debate, fencing tokens).

**Q10. What does `TestAcquireHold_ExactlyOneWinnerUnderRace` prove, and what does the `_WithRedisFlushed` variant add?**
The first: 200 goroutines, one seat, 20 repeated subtests, exactly 1 win and 199 `ErrSeatTaken`, checked against the database rows and `holds_audit`. With Redis healthy, most losers are likely turned away at Redis (my reading of the code, not measured). The second runs `FlushAll` every 5 ms, which disables the filter, and still gets one winner. That is what shows Postgres, not Redis, is the arbiter. Both are integration tests in [internal/inventory/integration_test.go](../../../internal/inventory/integration_test.go). Caveat: `FlushAll` wipes everything in the local Redis, so do not run it against a Redis holding data you care about.

**Q11. What does `TestStaleFenceRejected` prove, and what can it not distinguish?**
It shows a zombie holder (short TTL, hold reclaimed by a second user) has its `ConfirmSeats` rejected with `ErrHoldExpired`, while the real claimant succeeds, and that the fence token increased. By my reading, it cannot tell which predicate rejected the zombie: `AcquireHold` makes a new `hold_id` with `uuid.New()` each time, so `hold_id = ...` in `ConfirmSeats` would already fail. I am not certain the fence check is ever the deciding predicate here. You may want to verify by running the test against a locally edited query with the fence predicate removed. The fence is defense in depth. See `ConfirmSeats` in [sqlc/queries.sql](../../../sqlc/queries.sql).

Try it:
```
go test -tags=integration ./internal/inventory/... -run TestStaleFenceRejected -v
```

**Q12. Why validate and sort seat IDs before any I/O, and why can the unit tests pass a nil pool?**
`sortDedup` gives ascending order, which stops two multi-seat statements deadlocking each other, and a duplicate seat would make the `ConfirmSeats` `unnest` join error. Validation failures return before Redis or the database, so `TestAcquireHold_DuplicateSeatRejectedBeforeAnyIO` can build `Service` with a nil pool and a mock that expects no calls. Anything that needs a real transaction is integration-tested instead. See [internal/inventory/inventory_test.go](../../../internal/inventory/inventory_test.go).

## L4 Senior

**Q13. Redis is down. What happens, and what does the test suite not tell you?**
`lock.Acquire` returns `ErrUnavailable`, `AcquireHold` logs and `break`s out of the Redis loop, and every request goes to the Postgres CAS. Correctness is unchanged. Throughput drops to raw Postgres CAS throughput, because the cheap rejection path is gone. [plan.md](../../plan.md) says this cost must be capacity-planned. The tests prove correctness (`_WithRedisFlushed`), not throughput. I found no measured number for this in MILESTONES Phase 3.

**Q14. List the failure modes around a hold's side effects.**
- Redis release failure after a DB release: logged only. The stale key then blocks that seat at the Redis filter until its own TTL, even though Postgres says it is free. That is a false rejection, not a double-book. (My reading of `ReleaseHold` and `releaseRedisBatch`.)
- `holds_audit` is written after commit and is best effort, so a failed audit write leaves a gap. The audit table is evidence, not a source of truth.
- Commit succeeds but the process dies before the response: the client never receives the hold ID, so it cannot look the hold up. The seat stays held until the TTL lapses. I did not find a "list my holds" route.
- `ExtendHold` uses the plain `Querier`, not a transaction, and publishes no outbox event (see [internal/inventory/inventory.go](../../../internal/inventory/inventory.go)).

**Q15. What is deliberately not solved here?**
No separate `holds` table (hold lookup is a partial index on `event_seats.hold_id`, migration 000004, noted as a scope simplification in MILESTONES). The Redis path reports only the first conflicting seat, while the DB path reports all. I found no per-user cap on how many holds one user can create, only `MAX_SEATS_PER_HOLD` per hold. The 409 `conflicts` array and hold payload use internal seat IDs, not ordinals as [plan.md](../../plan.md) specifies. Payment and the saga are Phase 6, expiry side effects Phase 5.

**Q16. What changes at 100x load?**
Not measured in this phase, so these are hypotheses. A hot seat concentrates row-level lock waits on one `event_seats` row. With Redis healthy most contenders are filtered, but with Redis degraded all of them reach the same row. `event_seats` is one table keyed by `(event_id, seat_id)`, and a multi-seat hold is a single-event transaction, which is why [deep-dive.md](../../deep-dive.md) section 8 discusses sharding by event. The Terraform `cache` module sets `noeviction` and no snapshots so Redis never serves a stale or evicted hold key ([infra/terraform/modules/cache/main.tf](../../../infra/terraform/modules/cache/main.tf)), but only `terraform validate` has been run against it.
