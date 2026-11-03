# Phase 8 — Virtual Waiting Room and AIMD: Questions

Siblings: [BASICS.md](BASICS.md) · [FILES.md](FILES.md) · [learn index](../README.md)

Theory: [deep-dive.md](../../deep-dive.md) section 7. Verification record: [MILESTONES.md Phase 8](../../../MILESTONES.md).

## L1 Beginner

**Q1. What is a virtual waiting room and why does a ticket site need one?**
At on-sale time a huge crowd arrives at once. If all of them hit the hold endpoint, the database pool saturates, requests time out, clients retry, and the load grows (a retry storm). The waiting room turns that burst into a line and lets people through at a rate the inventory tier can take. See [deep-dive.md](../../deep-dive.md) section 7 and [internal/waitingroom/queue.go](../../../internal/waitingroom/queue.go).

**Q2. What is AIMD in one sentence?**
Raise the admission rate by a small fixed step when healthy (here +5 per second, capped at 1000), and halve it (floor 1) as soon as any signal is red. See [internal/waitingroom/controller.go](../../../internal/waitingroom/controller.go).

**Q3. What is the admission token and what does it stop?**
A signed string `v1.<payload>.<signature>` returned once you pass the cursor. `POST .../holds` requires it in `X-Admission-Token`. Without it, or with a forged, expired, or someone else's token, the answer is 403. So people cannot skip the line by calling the hold endpoint directly. See [internal/waitingroom/token.go](../../../internal/waitingroom/token.go).

## L2 Junior

**Q4. Trace `POST /events/1/queue` from request to response.**
`verifier.Middleware` sets claims → `waitingRoomAPI.joinQueue` → `Queue.Join` → `ZAddNX` on `{event:1}:q` → `ZRank` → read cursor. If `rank < cursor`: `issueToken`, respond 200 with `admissionToken`. Otherwise compute `position = rank - cursor` and `etaSeconds = int(position/rate)`, respond 202 with `pollAfterMs: 2000`. Files: [internal/httpapi/waitingroom.go](../../../internal/httpapi/waitingroom.go), [internal/waitingroom/queue.go](../../../internal/waitingroom/queue.go).

**Q5. Trace one AIMD tick.**
`runAIMDLoop` (every 1s, in the server process) → `ListAllEventIDs` → `Controller.Tick(eventID)` → `metrics.P99`, `metrics.ErrorRate`, `pool.Stat` → red flags → rate times 0.5 (min 1) or plus 5 (max 1000) → `SET {event:E}:rate` → `INCRBY {event:E}:cursor` by `int64(rate)` → `InsertWaitingRoomAudit`. Files: [internal/wiring/wiring.go](../../../internal/wiring/wiring.go), [internal/waitingroom/controller.go](../../../internal/waitingroom/controller.go).
Try it:
```
psql "postgres://ticketing:ticketing@localhost:5432/ticketing?sslmode=disable" -c "SELECT rate, cursor_value, pool_utilization, red_pool FROM waiting_room_audit WHERE event_id=1 ORDER BY id DESC LIMIT 5"
```

**Q6. Where do the latency and error numbers come from?**
`holdsAPI.createHold` calls `metrics.Record(time.Since(start), isSystemError(err))` after every hold attempt. `HoldMetrics` is a 200-slot ring buffer (a window of calls, not of time). `isSystemError` returns false for domain outcomes like a seat conflict, so only unexpected errors raise the error rate. See [internal/httpapi/holds.go](../../../internal/httpapi/holds.go) and [internal/waitingroom/metrics.go](../../../internal/waitingroom/metrics.go).

**Q7. What happens when someone re-joins the queue?**
`ZADD NX` leaves their original score alone, so their place is unchanged. If they are already past the cursor, they simply get a new token. `TestQueue_RejoinKeepsOriginalPlaceInLine` covers the queued case. See [internal/waitingroom/integration_test.go](../../../internal/waitingroom/integration_test.go).

## L3 Mid

**Q8. Why a ZSET plus a cursor, not an `INCR` counter?**
[deep-dive.md](../../deep-dive.md) section 7 compares both. `INCR` is O(1) but ties and order depend on call order, and it is one hot key. A ZSET orders by true arrival time and answers rank queries, at O(log N) per op and more memory. The cursor approach also keeps the controller simple: it touches one number, and admission is just `rank < cursor`. Cost: ZRANK on every poll.

**Q9. Why HMAC tokens and not JWTs, and why bind to the `sub`?**
The comment in [internal/waitingroom/token.go](../../../internal/waitingroom/token.go) says one service both issues and verifies, so a JWT library's algorithm-confusion surface buys nothing. Binding to the Cognito `sub` means a leaked or shared token fails verification for any other user (`ErrSubMismatch`). The nonce is carried but not enforced as single use; the file says that is a deliberate cut.

**Q10. Why gate only `POST .../holds`?**
GET, DELETE and extend act on a hold the caller already owns, so re-checking admission would protect nothing. Browsing routes stay open so the queue does not block the seat map. See [internal/httpapi/router.go](../../../internal/httpapi/router.go).

**Q11. What do the tests prove, and what do they not?**
Unit: token round trip, tamper, wrong sub, wrong event, expiry, malformed; ring-buffer wrap forgets old samples. Integration (`-tags=integration`): rejoin keeps place, admission once the cursor passes rank, an all-green tick gives rate 6.0 (1.0 + 5.0), and a pool clamped to 1 connection and held makes the rate go 100, 50, 25, 12.5. The held connection is deliberate: MILESTONES says local Postgres is too fast to saturate a small pool with concurrent curl. Not proven: the latency and error signals as triggers, `RequireAdmission` as Go code (live curl only), several controllers at once.
Try it:
```
go test -tags=integration ./internal/waitingroom/... -v
```

## L4 Senior

**Q12. Does the rate limit actually throttle arrivals?**
Reasoned from the code, so verify it. `Tick` advances the cursor every second for every event, whether or not anyone is queued, and admission is `rank < cursor`. After an idle period the cursor is far ahead of the queue, so new arrivals are admitted at once regardless of the current rate. The rate only bites when arrivals outrun the cursor. MILESTONES hints at this: its first 5,000-arrival run says "cursor pre-advanced (admits all)", and it had to `DEL` the cursor to test the queued path. A real on-sale would need the cursor to start at the sale time, or to stop growing without demand.
Try it:
```
redis-cli GET '{event:1}:cursor'
```

**Q13. What breaks with more than one server replica?**
MILESTONES names the metrics half: `HoldMetrics` and pool stats are per process, so the signals only describe that replica; a shared backend such as CloudWatch would be needed. From reading `wiring.New`, there is also a cursor half: every replica starts its own `runAIMDLoop`, each does `INCRBY` on the same cursor, so admission would be multiplied by the replica count, and the shared rate key is last-writer-wins. I have not tested this. Also `cmd/server-lambda` uses the same `wiring.New`, so it starts the loop too.

**Q14. What are the other failure modes and costs?**
(a) `waiting_room_audit` gets one row per tick per event, forever, including idle events (about 86,400 rows a day per event, from the 1s tick). I found no cleanup. (b) If Redis loses the ZSET or cursor, everyone's place is lost; issued tokens stay valid until their 15-minute expiry because verification is stateless. (c) A token is not single use, so one admitted account can make many hold requests in 15 minutes. (d) No bot defense exists in this phase: one Cognito account is one queue slot, and many accounts are many slots; [deep-dive.md](../../deep-dive.md) section 7 lists mitigations that were not built. (e) Metrics are windowed by call count, so a quiet period leaves old samples in place.

**Q15. What does 100x look like, and what is NOT solved?**
Measured, with caveats from MILESTONES: `Queue.Join` handled 5,000 distinct arrivals in 514.8 ms (admitted path) and 670.5 ms (queued path) at concurrency 200, direct against local Redis on one machine, bypassing HTTP and JWT. That is not a capacity claim. At 100x, each poll is a `ZADD NX` plus `ZRANK` plus two `GET`s per user per two seconds, all on one `{event:E}` hash slot, so one event's keys cannot be spread across Redis cluster shards. Not solved: a waiting-room UI with live position (the client only polls up to 30 seconds), bot defense, multi-replica control, a use-once token store, and audit-table retention.
Try it:
```
go run ./scripts/queue-loadtest -event=1 -n=5000 -concurrency=200
```
