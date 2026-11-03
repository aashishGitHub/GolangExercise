# Phase 10 — Load Harness: Questions

Siblings: [BASICS.md](BASICS.md) · [FILES.md](FILES.md) · [Learn index](../README.md)

> Numbers quoted here are from [MILESTONES.md](../../../MILESTONES.md), measured on one dev machine. Treat them as relative baselines, not capacity.

## L1 Beginner

**Q1. What is a load test and why not just rely on unit tests?**
A load test runs many concurrent users against the real stack. Unit tests rarely have enough concurrency to hit a race window. Phase 10's first run crashed the server in about 11 seconds, a bug three phases of tests had missed (see MILESTONES). Harness: [scripts/loadtest/main.go](../../../scripts/loadtest/main.go).

**Q2. What do p50, p95 and p99 mean?**
Sort all latencies. p50 is the middle value. p95 means 95% of requests were at or below it. p99 shows the slow tail. `printReport` in [main.go](../../../scripts/loadtest/main.go) picks `sorted[int(p*n)]`.

**Q3. What is the difference between the `uniform` and `hot` scenarios?**
`uniform` picks a random seat from 30,000 per journey. `hot` sends every worker at one seat (`-hot-ordinal`). Uniform shows normal traffic. Hot shows the worst race. See `pickSeat` in [main.go](../../../scripts/loadtest/main.go).

**Q4. What is a conflict, and is it a failure?**
A conflict is HTTP 409 on the hold call, classified `OutcomeSeatTaken` in [journey.go](../../../scripts/loadtest/journey.go). It means the seat was already held. It is correct behavior, not an error.

## L2 Junior

**Q5. Trace one journey from `run` to its outcome.**
`journeyRunner.run` in [journey.go](../../../scripts/loadtest/journey.go): `joinQueue` → `pickSeat` → `acquireHold` → `onHold` (WS observer) → `createOrder` → `pollOrder`. It returns a `Result` with `HoldMs`, `OrderMs`, `TotalMs` and an `Outcome`.

**Q6. How do the workers, the CSV writer and the report fit together?**
Each worker goroutine loops `runner.run` and sends `Result` into a buffered channel. A goroutine closes the channel after `wg.Wait()`. The main goroutine ranges over the channel, writes CSV rows and collects a slice, then calls `printReport`. See [main.go](../../../scripts/loadtest/main.go).

**Q7. How is WS delta lag measured?**
Workers store `ordinal → time` in `noteHoldRequested`. A separate WS client in [wslag.go](../../../scripts/loadtest/wslag.go) decodes `OpSparse` frames. For each changed ordinal it computes `now - storedTime`. The clock starts after the hold response, not at the request.

**Q8. How do I run it?**
Get the pool and client ids from `scripts/seed-cognito-local.sh` (written to `.env.local`), then run the command from the [README](../../../README.md).
Try it:
```
make compose-up
```
```
go run ./scripts/loadtest -pool-id=<pool> -client-id=<client> -event=1 -workers=20 -duration=20s -contention=uniform
```

## L3 Mid

**Q9. Why a reused identity pool of 20 users instead of one user per arrival?**
cognito-local admin calls are slow, and per-arrival user creation would measure the login system, not seat contention and the saga. The comment on `provisionUsers` in [users.go](../../../scripts/loadtest/users.go) says so. Cost: all journeys from one user share a `sub`, so per-user behavior (such as per-user limits) is not exercised.

**Q10. What did the first load run find, and why did unit tests not?**
`cmd/server` panicked with "send on closed channel". `writeLoop` closed `c.send` on exit while `pumpRedis` (another goroutine) could be sending to it. The chaos disconnect (about 1% of connections) triggered it. Phase 7 tested with one WS client at a time. The fix in [internal/wshub/wshub.go](../../../internal/wshub/wshub.go): `writeLoop` never closes `c.send`; it closes the websocket, which ends `readLoop`, which runs `unsubscribe`. What is proven: both scenarios then ran to completion. What is not: there is no regression test for the race itself.

**Q11. What does the post-run invariant check prove?**
`runDBInvariants` in [main.go](../../../scripts/loadtest/main.go) queries for any `(event_id, seat_id)` with more than one live ticket. Zero rows after the hot run (49,735 journeys, 1 ticketed, per MILESTONES) is the same thesis the Phase 3 race tests proved, now under real HTTP load. It checks tickets only. It does not check money invariants.

**Q12. Why are hold latencies so low in the hot scenario?**
MILESTONES attributes it to cheap rejections: nearly all 49,734 journeys were 409s on the Redis fast path (it cites about 0.2 ms each, from `docs/plan.md`'s own estimate, which I did not re-measure). Hold p99 was 4.64 ms. So the hot run measures the fast-reject path, not the winning path.

## L4 Senior

**Q13. What can this harness not tell you?**
It runs on a laptop with Docker, so only run-to-run comparison on the same machine is meaningful. Each worker starts its next journey only after the last one finishes, so offered load slows down when the server slows down. That can make tail latency look better than a fixed arrival rate would. The repo does not discuss this effect; I am adding it from general load-testing knowledge, so verify it against a load-testing reference. The identity pool is 20 users. WS lag had `n=29` samples in the uniform run.

**Q14. What is wrong or fragile in the harness itself?**
The saga compensation count is meant to be global across the database, not per run. As written it filters on a non-existent column (`status`, the column is `state`), so it errors and the line is never printed; see the note in [FILES.md](FILES.md). WS lag keys on ordinal, so two overlapping holds on the same ordinal overwrite each other. Percentiles use index `int(p*n)` on a sorted slice, which is a simple nearest-rank style, and the lag sort is insertion sort (fine for small `n`, quadratic for large). `pollOrder` gives up at 5 seconds and counts that as `ERROR`.

**Q15. Why is WS delta lag stuck near 500 ms, and what does the code say about it?**
MILESTONES reports p50/p95/p99 of 332.90 / 494.18 / 498.14 ms and says the ceiling is "not yet root-caused". I have no verified cause. One candidate to check, not a conclusion: [cmd/projector/main.go](../../../cmd/projector/main.go) polls with a 500 ms `time.NewTicker` before calling `proj.RunOnce`, which is the same order of magnitude as the ceiling. Whether the load run used that standalone projector is not stated in MILESTONES, so verify before concluding.

**Q16. How would you take this to 100× load?**
One process on one laptop will be the bottleneck first. You would split load generators across machines, use a fixed arrival rate, bring thousands of real distinct identities, and run against production-like infra. The harness would also need to be wired into CI as a regression gate; today it has no Makefile target and no automated assertions beyond the printed invariant line. The 30,000-seat range is hard-coded in `main.go`.
