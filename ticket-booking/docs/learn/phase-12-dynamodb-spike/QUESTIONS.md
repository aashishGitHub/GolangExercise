# Phase 12 — DynamoDB Spike: Questions

Siblings: [BASICS.md](BASICS.md) · [FILES.md](FILES.md) · [Learn index](../README.md) · Write-up: [dynamodb-comparison.md](../../dynamodb-comparison.md)

> Numbers come from the write-up. They were measured on `dynamodb-local` on one laptop: not real DynamoDB, not capacity claims.

## L1 Beginner

**Q1. What is a conditional write and why does a seat hold need one?**
A write that only happens if a rule about the current item is true, checked atomically by the database. A seat hold must succeed for exactly one person. The rule is "seat is free, or the old hold has expired". See `AcquireHoldSingle` in [hold.go](../../../cmd/dynamo-spike/hold.go).

**Q2. What are a partition key and a sort key, and which does this table use?**
The partition key chooses the storage partition; the sort key orders items inside it. Here `event_id` (string) is the partition key and `seat_id` (number) is the sort key. See `ensureTable` in [table.go](../../../cmd/dynamo-spike/table.go).

**Q3. What does `TransactWriteItems` give you that a loop of `UpdateItem` calls does not?**
All-or-nothing across several items. In a loop, you could win 2 of 4 seats and lose the rest. `AcquireHoldMulti` in [hold.go](../../../cmd/dynamo-spike/hold.go) uses it for multi-seat holds.

**Q4. Why is this called a "spike" and why is it not wired into the server?**
It is a non-blocking comparison experiment. It sits under `cmd/`, not `internal/`, and no production binary imports it. Postgres stays the seat arbiter. See the status paragraph at the top of [dynamodb-comparison.md](../../dynamodb-comparison.md).

## L2 Junior

**Q5. Trace one single-seat hold from the load test to the database.**
[loadtest.go](../../../cmd/dynamo-spike/loadtest.go) worker loop → `pickSeats` → `AcquireHoldSingle` → `client.UpdateItem` with the condition expression → success returns nil; `ConditionalCheckFailedException` returns `ErrConflict`. The worker records latency and a conflict flag.

**Q6. How does `AcquireHoldMulti` decide whether to retry?**
On `TransactionCanceledException` it calls `anyConditionFailed` on the cancellation reasons. If any reason is `ConditionalCheckFailed` it returns `ErrConflict` without retry. Otherwise it sleeps `(1<<attempt)*10ms` plus random jitter and retries, up to 5 attempts. See [hold.go](../../../cmd/dynamo-spike/hold.go).

**Q7. How is expiry handled, and where does the "now" come from?**
Read-time expiry: the condition includes `hold_expires_at < :now`. `:now` is the caller's `time.Now().UnixMilli()`. There is no TTL attribute and no reaper. See `AcquireHoldSingle` in [hold.go](../../../cmd/dynamo-spike/hold.go).

**Q8. How do I run the race test and the 4-seat hot load test?**
Start `dynamodb-local` (profile `spike`), then run the binary with the flags from [main.go](../../../cmd/dynamo-spike/main.go).
Try it:
```
docker compose --profile spike up -d dynamodb-local
```
```
go run ./cmd/dynamo-spike -mode=race
```
```
go run ./cmd/dynamo-spike -mode=loadtest -contention=hot -multi-seat=4 -workers=50 -duration=15s -seats=30000
```

## L3 Mid

**Q9. Why does the race test run 20 rounds with 200 goroutines behind one barrier?**
One pass proves little: a race bug may show only sometimes. The barrier (`close(start)`) releases all goroutines at once to maximize overlap. Same shape as `TestAcquireHold_ExactlyOneWinnerUnderRace` in [internal/inventory/integration_test.go](../../../internal/inventory/integration_test.go). What it proves: 20/20 rounds, `wins=1 conflicts=199`. What it does not: `runRaceTest` in [race.go](../../../cmd/dynamo-spike/race.go) never reads the item back, while the Postgres test does.

**Q10. Why is the uniform conflict rate 64.6% here but 14.2% in Phase 10, and is DynamoDB worse?**
No. The write-up says it is a method artifact. The spike never releases holds, so the 30,000-seat pool shrinks at thousands of attempts per second (78,590 attempts in 15 s). Phase 10 ran a full HTTP journey at 2,557 journeys in 20 s and never exhausted the pool. The comparable pair is p50 for single-item (11.40 ms) versus 4-seat transaction (46.82 ms) in the same run.

**Q11. Why does a `hold_id` condition replace the fence token here?**
A zombie confirm carries an old `hold_id`. After a reclaim and re-hold, the stored `hold_id` differs, so the condition fails. The write-up says Postgres's `fence_token` is arguably redundant for the same reason, and this spike is a second data point. See `ConfirmSeats` in [hold.go](../../../cmd/dynamo-spike/hold.go). Caveat: that function is never called by the race or load modes, so this property is argued from the condition expression, not demonstrated by a run.

**Q12. Why drive `AcquireHold*` directly instead of reusing `scripts/loadtest`?**
`scripts/loadtest` uses the full HTTP journey through `cmd/server`, and the spike is deliberately not wired in. So the comparison is hold-acquisition only, not like-for-like. The write-up states this explicitly in "Load scenarios". See [scripts/loadtest/main.go](../../../scripts/loadtest/main.go) for the Phase 10 harness.

## L4 Senior

**Q13. What trade-offs does the spike show between DynamoDB and Postgres for this problem?**
Per the write-up: single-item atomicity and read-time expiry transfer cleanly. Multi-seat atomicity costs about 4x latency here (p50 about 11 ms vs about 47 ms), and the docs claim 2x write units for transactions (from `docs/script.md`; verify against current AWS pricing docs). The transactional outbox has no one-operation DynamoDB equivalent, so Streams plus a consumer is needed. Cost shape (pay-per-request vs Aurora's floor) is a shape observation, not a measured number.

**Q14. What failure modes does `AcquireHoldMulti` have under contention?**
Real conflicts return immediately. Transient cancellations retry 5 times with backoff, then return an "exhausted" error, which the load test counts as `err`, not as conflict. I cannot tell from the docs how many errors the recorded runs had, because the report's `error rate:` line is not quoted there. Under hot contention with 50 workers, whether retries distort p99 is not analyzed.

**Q15. What is risky about deciding expiry with the client's clock?**
`:now` comes from `time.Now()` in the Go process. With clock skew between app servers, one server may treat a hold as expired while another does not. Postgres's `hold_expires_at > now()` uses one database clock. This difference is my reading of the code, not stated in the write-up. Verify it by checking whether DynamoDB offers a server-side time function (I do not believe it does, but check the docs).

**Q16. What is NOT solved or evaluated, and what would 100x look like?**
Not evaluated: the WebSocket connection-registry alternative (decision #7 in `docs/plan.md`), real DynamoDB behavior (`dynamodb-local` has no partitions or replication), hot-partition limits (all seats of one event share one partition key; the spike cannot show whether that throttles at real scale), and the confirm path under load. Also missing: expiry check in `ConfirmSeats` (the Postgres one has `hold_expires_at > now()`), and any release path. At 100x you would need a real-service test, a per-event partition strategy, and the Streams-based outbox. None of that is built or measured.
