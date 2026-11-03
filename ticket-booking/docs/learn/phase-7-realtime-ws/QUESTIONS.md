# Phase 7 — Realtime Projector and WebSocket Deltas: Questions

Siblings: [BASICS.md](BASICS.md) · [FILES.md](FILES.md) · [learn index](../README.md)

Scope: [MILESTONES.md Phase 7](../../../MILESTONES.md). Background on caching and CQRS: [deep-dive.md](../../deep-dive.md) section 8. The browser does not yet connect to `/ws`.

## L1 Beginner

**Q1. Why push seat changes over a WebSocket instead of polling?**
Polling asks "anything new?" on a timer, mostly getting "no". A WebSocket is a long-lived connection, so the server pushes a change the moment it happens. Each push is a few bytes. See [internal/wshub/wshub.go](../../../internal/wshub/wshub.go). Today the browser still polls `/availability`; only the Go test client uses `/ws`.

**Q2. What are a snapshot, a delta and a sequence number?**
Snapshot (`0x01`): the full 2-bit-per-seat bitset plus the current seq. Delta (`0x02` SPARSE): one or more `(ordinal, new state)` pairs at one seq. Seq: a per-event counter that goes up by one on each real change, so a client can spot a missing update. See [internal/wsproto/wsproto.go](../../../internal/wsproto/wsproto.go).

**Q3. How big is a seat map on the wire?**
2 bits per seat. 30,000 seats is 7,500 bytes, and with the 20-byte header a snapshot frame is 7,520 bytes (MILESTONES live output). A single delta with one change is 16 bytes. See `ByteLen` in [internal/seatmap/seatmap.go](../../../internal/seatmap/seatmap.go).

## L2 Junior

**Q4. Trace one seat hold until a connected client sees a delta.**
`inventory.AcquireHold` commits the seat and a `seat.held` row in `domain_events` → `cmd/projector` tick (500 ms) → `Projector.RunOnce` → `ListUnprocessedDomainEvents` → `apply` (`SETRANGE`, `INCR seq`, `EncodeSparse`, `LPUSH`/`LTRIM`/`PUBLISH`) → `wshub.pumpRedis` receives on `{event:E}:notify` → copies to each conn's `send` channel → `writeLoop` → `WriteMessage`. Files: [internal/projector/projector.go](../../../internal/projector/projector.go), [internal/wshub/wshub.go](../../../internal/wshub/wshub.go).
Try it:
```
go run ./cmd/projector
```

**Q5. What happens when a client connects with `sinceSeq=4`?**
`ServeWS` → `subscribe` → `sendInitialFrames` → `proj.GapFill(eventID, 4)`. If the ring still holds seq 5 onward, the client gets one combined SPARSE frame at the current seq. If already caught up, nothing is sent. Otherwise `ok=false` and the hub sends a full snapshot. See [internal/wshub/wshub.go](../../../internal/wshub/wshub.go).
Try it:
```
go run ./scripts/ws-test-client -event=1 -token=YOUR_ID_TOKEN -sinceSeq=4 -count=1
```

**Q6. How does the projector avoid applying an event twice?**
Two layers. `ListUnprocessedDomainEvents` skips rows already in `processed_events` for consumer `projector`. And `apply` reads the current 2-bit state and returns "no change" if it already equals the new state, with no seq bump. See [internal/projector/projector.go](../../../internal/projector/projector.go).

**Q7. What does `GET /availability` do if Redis is down?**
`tryRedisAvailability` returns ok=false on any Redis error, so the handler falls back to the Postgres scan. It returns 200 with the same ETag, but without the `X-Seatmap-Version` header. See [internal/httpapi/catalog.go](../../../internal/httpapi/catalog.go).

## L3 Mid

**Q8. Why does the projector read `domain_events` and not `event_seats`?**
One input, one order. The source comment says the read model has "exactly one input: the same outbox every other consumer reads". Reading `event_seats` directly would need change detection and would have no per-event ordering. The cost is lag: the projector polls every 500 ms. See [internal/projector/projector.go](../../../internal/projector/projector.go).

**Q9. Why subscribe to live deltas before building the snapshot?**
If you snapshot first and subscribe second, a delta published in between is lost, and nothing marks it missing. Subscribing first means such deltas queue in the conn's buffered channel; some may duplicate what the snapshot already shows. The client rule "ignore seq at or below what I applied" makes duplicates harmless. MILESTONES says this was caught in review before it ran. See `ServeWS` in [internal/wshub/wshub.go](../../../internal/wshub/wshub.go).

**Q10. Why did `outbox_seq` have to be added, and what proves the fix?**
Ordering by `created_at` is not a strict order for fast inserts. A projector applying hold then release in the wrong order treats the second as a duplicate no-op, so the map silently drifts. MILESTONES: 220 transitions applied only 112 to 217 before the fix. After it, all were applied. The test asserts at least 220 applied. See [migrations/000008_domain_events_outbox_seq.up.sql](../../../migrations/000008_domain_events_outbox_seq.up.sql) and `TestGapFill_CoversRecentGapAndOverflowTriggersResync` in [internal/projector/integration_test.go](../../../internal/projector/integration_test.go).
Try it:
```
go test -tags=integration ./internal/projector/... -v
```

**Q11. What is tested, and what is only verified by hand?**
Unit: frame encode/decode (`TestSnapshotRoundTrip`, `TestSparseRoundTrip`, three `TestDecode_*` rejects) and the bitset (`TestSetGet_*`). Integration: projector held/released, confirm to SOLD, gap-fill coalescing and overflow resync. By hand with `ws-test-client`, per MILESTONES: live delta, chaos disconnect, Redis-down fallback. No `wshub` test exists. The 10-minute idle timeout was not waited out. See [internal/wsproto/wsproto_test.go](../../../internal/wsproto/wsproto_test.go).
Try it:
```
go test ./internal/wsproto/... -v
```

## L4 Senior

**Q12. Where can the read model silently drift?**
Reasoned from the code, not reproduced. (a) `apply` does `SETRANGE`, then `INCR`, then a pipeline, with no `MULTI`/`EXEC`. A crash after `SETRANGE` leaves the bit changed with no seq bump or delta. The retried event then finds the state already equal, takes the no-op path, and the live delta is never published. A fresh snapshot is still correct, but connected clients stay stale. MILESTONES names the non-atomic window and defers it as theoretical with one projector. (b) If Redis is flushed, the seq key resets while `processed_events` still says "done". A client holding a higher seq could reconnect with a `sinceSeq` at or above the new current seq, and `GapFill` would answer "already caught up". Verify before relying on this. See [internal/projector/projector.go](../../../internal/projector/projector.go).

**Q13. What happens to a slow or lossy client?**
`pumpRedis` does a non-blocking send into a 64-slot channel. When full, the frame is dropped and logged, with the comment "client will gap on next frame and resync". The server never detects or forces that resync. A client has to notice a seq jump itself. No such client exists in `web/src` yet, so this contract is untested end to end. A 1% chaos disconnect (`ChaosDisconnectRate`) forces reconnects on every local run. See [internal/wshub/wshub.go](../../../internal/wshub/wshub.go).

**Q14. What breaks at 100x?**
Reasoned from constants, not measured. The projector handles one batch of 200 events per 500 ms tick, so about 400 events per second at best. The ring holds 200 deltas, which at high churn covers only seconds, so a reconnect storm turns into a snapshot storm (7.5 KB each at 30k seats). Fan-out copies every frame to every connection in Go on one process; one Redis subscription per event per server instance. The per-process `ordinals` map in `Projector` has no lock and is shared by concurrent connections on the server; I have not run `-race` on it. Multiple projector replicas would need the Lua/transaction fix that MILESTONES defers.

**Q15. What is NOT solved or built?**
The `0x03` RUN frame (declared, never emitted). A browser WS client (the map still polls). Atomic `apply` for multiple replicas. A wall-clock test of the 10-minute idle timeout. `ws_connections.last_seq_sent` is never written (`UpdateWSConnectionSeq` has no caller). The 128 KB frame cap is enforced but not exercised at current sizes. And `cmd/server` does not run the projector, so a forgotten `cmd/projector` means a snapshot that never changes.
