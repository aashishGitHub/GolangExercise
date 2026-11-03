# Phase 5 — Expiry + Outbox + Reaper: Questions

Siblings: [BASICS.md](BASICS.md) · [FILES.md](FILES.md) · [learn index](../README.md)

Theory: [deep-dive.md](../../deep-dive.md) section 2. Verification record: [MILESTONES.md Phase 5](../../../MILESTONES.md).

## L1 Beginner

**Q1. What happens to a seat when its hold runs out?**
Two things can free it. First, passive expiry: the next hold attempt sees `hold_expires_at < now()` in the CAS `WHERE` clause and takes the seat. Second, the reaper flips it to AVAILABLE on its next 3-second tick. See `AcquireHold` in [sqlc/queries.sql](../../../sqlc/queries.sql) and [internal/reaper/reaper.go](../../../internal/reaper/reaper.go).

**Q2. What is a transactional outbox and why does the project need one?**
It is a table (`domain_events`) written in the same database transaction as the seat change. A separate relay publishes the rows to EventBridge later. Without it, a crash between "commit the seat" and "tell the bus" loses the event. See [internal/events/events.go](../../../internal/events/events.go).

**Q3. If the reaper is switched off, can two people buy the same seat?**
No. The reaper is only for freshness. Correctness comes from the predicate inside every CAS statement. MILESTONES Phase 5 shows this live: reaper off, a fresh hold on an expired seat still succeeds and the fence token goes 1 to 2.
Try it:
```
go test -tags=integration ./internal/inventory/... -run TestExpiredHoldIsReclaimed -v
```

## L2 Junior

**Q4. Trace a `seat.released` event from a user's release call to EventBridge.**
`httpapi` holds handler → `inventory.Service.ReleaseHold` → `pool.Begin` → `qtx.ReleaseHold` (UPDATE, rowcount n) → if `n > 0` `events.Publish(qtx, "1:701", "seat.released", ...)` → `tx.Commit`. Later, [cmd/outbox-relay/main.go](../../../cmd/outbox-relay/main.go) ticks every 2s → `Relay.RunOnce` → `ListUnpublishedDomainEvents` → `PutEvents` → `MarkDomainEventPublished`. Files: [internal/inventory/inventory.go](../../../internal/inventory/inventory.go), [internal/outbox/relay.go](../../../internal/outbox/relay.go).

**Q5. How does the reaper find expired holds, and what does it call to release them?**
`q.ListExpiredHolds` selects distinct `(event_id, hold_id)` where status is 1 or 3 and `hold_expires_at < now()`. That scan is backed by the partial index `event_seats_expiry_idx` from [migrations/000002_event_seats.up.sql](../../../migrations/000002_event_seats.up.sql). For each hold it fetches seat ids and calls `inv.ReleaseHold`, the same function the HTTP release uses. See [internal/reaper/reaper.go](../../../internal/reaper/reaper.go).

**Q6. What decides whether the relay marks a row as published?**
Two checks: `PutEvents` returned no Go error, and `out.FailedEntryCount == 0`. EventBridge can accept a call and still reject entries, so the second check matters. If either fails, the row stays `published_at IS NULL` and the next tick retries it. See `RunOnce` in [internal/outbox/relay.go](../../../internal/outbox/relay.go).

**Q7. Why does `ReleaseHold` only publish when rowcount is above zero?**
Rowcount 0 means the hold was already gone (expired and reclaimed, or confirmed). Nothing changed, so there is nothing to announce. This also makes double release and reaper/user races harmless. `TestReleaseHold_IdempotentOnAlreadyGoneHold` asserts exactly one `seat.released` row after two calls. See [internal/inventory/integration_test.go](../../../internal/inventory/integration_test.go).

## L3 Mid

**Q8. Why an outbox table instead of calling EventBridge right after commit?**
A direct call after commit is a dual write: crash in between and the event is lost forever, with nothing to detect it. Calling before commit risks announcing a change that rolls back. The outbox makes the event row share the transaction's fate, so the worst case is a delayed or duplicated event. Cost: an extra table, an extra process, at-least-once delivery. See [internal/events/events.go](../../../internal/events/events.go) and the package comment of [internal/outbox/relay.go](../../../internal/outbox/relay.go).

**Q9. Why a reaper at all if passive expiry is enough, and why is it a poll loop?**
Passive expiry only fires when a contender tries that seat. Other users' seat maps and the side effects (counts, cache, analytics) would stay stale until then. The reaper makes the release happen, and emit its event, on a schedule. The header comment of [cmd/hold-reaper/main.go](../../../cmd/hold-reaper/main.go) says the local loop is a substitute because moto cannot fire schedules; the Lambda twin [cmd/hold-reaper-lambda/main.go](../../../cmd/hold-reaper-lambda/main.go) does the same full scan, not per-hold schedules (its own header comment says so).

**Q10. Why was `outbox_seq` needed when `created_at` already exists?**
`created_at` is not a strict total order for rows inserted microseconds apart. In Phase 7 a test with 220 real transitions applied only 112 to 217 of them, because two same-direction changes arrived out of order and the second was treated as a duplicate. Ordering by a `BIGSERIAL` fixed it (220/220). See [migrations/000008_domain_events_outbox_seq.up.sql](../../../migrations/000008_domain_events_outbox_seq.up.sql) and `ListUnpublishedDomainEvents` in [sqlc/queries.sql](../../../sqlc/queries.sql).

**Q11. How is Phase 5 tested, and what do the tests prove?**
Honest answer: thinly. The relay and reaper packages have no test files. Their behavior was proven by live verification in MILESTONES (passive expiry with reaper off, bulk sweep, relay draining a backlog). The code-level proofs are in the inventory integration tests: `TestExpiredHoldIsReclaimed` (passive expiry alone) and `TestReleaseHold_IdempotentOnAlreadyGoneHold` (outbox row in the same transaction, none on a no-op). Both need real Postgres and Redis (`-tags=integration`).
Try it:
```
go test -tags=integration ./internal/inventory/... -run TestReleaseHold_IdempotentOnAlreadyGoneHold -v
```

## L4 Senior

**Q12. Name the failure modes of the relay.**
(a) Crash after `PutEvents`, before `MarkDomainEventPublished`: duplicate send, so consumers need dedup (the projector uses `processed_events`). (b) A row EventBridge keeps rejecting: the code has no attempt counter or dead-letter state. (c) By reading the query, `ListUnpublishedDomainEvents` always returns the oldest 25 unpublished rows (`ORDER BY outbox_seq LIMIT`). If 25 or more rows fail permanently, newer healthy rows are never reached. I have not reproduced this; verify before relying on it. (d) `MarkDomainEventPublished` failing returns an error and aborts the rest of that tick's batch. See [internal/outbox/relay.go](../../../internal/outbox/relay.go).

**Q13. What does this design NOT solve?**
Per MILESTONES, at the end of Phase 5, side effects 2, 3 (counts, seat-map cache) wait for Phase 7, 4 (waiting room) for Phase 8, and 8 (idempotency key expiry) for Phase 6. Also: I found no code that deletes or archives published `domain_events` rows, so the table only grows. Ordering is per relay (by `outbox_seq`); I am not certain what ordering EventBridge itself guarantees to subscribers, so verify in the AWS docs before assuming ordered delivery downstream.

**Q14. What breaks at 100x the load?**
Reasoned from the code, not measured. The relay sends one `PutEvents` call per row, batch of 25, every 2s: that caps throughput and is chatty. The reaper does an unbounded `ListExpiredHolds` scan every 3s and a per-hold loop of reads plus one transaction. Several reaper instances would not corrupt anything (release is idempotent), but they would duplicate work, since there is no `FOR UPDATE SKIP LOCKED` or sharding of the scan. Fixes would be batched `PutEvents`, bounded scans, or per-hold schedules. The Lambda twin comment says per-hold schedules were deliberately not built.

**Q15. Can the reaper release a hold that was just extended for payment?**
Not by the code as written, by this argument: `ExtendHold` requires `hold_expires_at > now()`, and time only moves forward. A hold the reaper saw as expired can no longer be extended, so a short rowcount sends the saga to `ErrHoldExpired`. A confirm clears `hold_id`, so a late release matches nothing. Note `ReleaseHold` itself has no expiry check in its `WHERE`, so it relies on this ordering. See `ExtendHold` and `ReleaseHold` in [sqlc/queries.sql](../../../sqlc/queries.sql).
