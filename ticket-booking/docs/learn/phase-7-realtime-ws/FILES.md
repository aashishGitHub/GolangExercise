# Phase 7 — Realtime Projector and WebSocket Deltas: Files

Siblings: [BASICS.md](BASICS.md) · [QUESTIONS.md](QUESTIONS.md) · [learn index](../README.md)

> Note: the browser does not use `/ws`. Only [scripts/ws-test-client/main.go](../../../scripts/ws-test-client/main.go) does. MILESTONES lists the frontend WS client as not done.

> Note: MILESTONES says the overflow test applies "exactly 220/220". `TestGapFill_CoversRecentGapAndOverflowTriggersResync` asserts `totalApplied < iterations*2` as the failure, so it checks "at least 220".

> Note: `cmd/server` does not run the projector loop. [internal/wiring/wiring.go](../../../internal/wiring/wiring.go) builds a `projector.Projector` only for reads (`EnsureBitset`, `GapFill`, `SeatCount`). Deltas only flow if `cmd/projector` is running.

## [internal/wsproto/wsproto.go](../../../internal/wsproto/wsproto.go)
- **What it does**: Encodes and decodes the two binary frames, `0x01` SNAPSHOT and `0x02` SPARSE.
- **Control flow**: `projector.apply` → `EncodeSparse`. `wshub.sendInitialFrames` → `EncodeSnapshot`. `projector.GapFill`, `wshub.writeLoop` and the test client → `Decode`.
- **Data flow**: SNAPSHOT is op, version, u16 layout version, u32 event id, u64 seq, u32 seat count, then the packed bitset (20-byte header). SPARSE is op, version, u64 seq, u16 count, then u32 per change: `(state<<30)|ordinal`. All little-endian. `Decode` rejects short buffers, a wrong version, or an unknown op. `OpRun` (0x03) is declared but never produced or decoded.
- **How it is tested**: [internal/wsproto/wsproto_test.go](../../../internal/wsproto/wsproto_test.go), plain unit tests: `TestSnapshotRoundTrip`, `TestSparseRoundTrip` (includes the max 30-bit ordinal), `TestDecode_ShortBufferRejected`, `TestDecode_UnknownOpRejected`, `TestDecode_WrongVersionRejected`.
- **Read next**: [internal/seatmap/seatmap.go](../../../internal/seatmap/seatmap.go).

## [internal/seatmap/seatmap.go](../../../internal/seatmap/seatmap.go)
- **What it does**: The 2-bit-per-seat bitset: `New`, `Get`, `Set`, `ByteLen`, and `StateFromDB`.
- **Control flow**: `projector.EnsureBitset` builds from DB rows with `Set`. `getAvailability` DB fallback does the same. Tests and the client read with `Get`.
- **Data flow**: ordinal → byte `ordinal>>2`, shift `(ordinal&3)*2`. DB status 0,1,2,3 plus `sellable` → wire state. `sellable=false` forces UNAVAILABLE. PENDING_PAYMENT shows as HELD.
- **How it is tested**: [internal/seatmap/seatmap_test.go](../../../internal/seatmap/seatmap_test.go), unit: `TestByteLen`, `TestSetGet_AllOrdinalsInByte`, `TestSetGet_RoundTripAllOrdinals`, `TestSet_DoesNotDisturbNeighbors`, `TestNew_AllFree`, `TestStateFromDB`.
- **Read next**: [internal/projector/projector.go](../../../internal/projector/projector.go).

## [internal/projector/projector.go](../../../internal/projector/projector.go)
- **What it does**: Sole writer of the Redis read model per event: bitset, seq, delta ring, pub/sub notify.
- **Control flow**:
  1. [cmd/projector/main.go](../../../cmd/projector/main.go) ticks every 500 ms → `RunOnce(ctx, 200)`.
  2. `RunOnce` → `q.ListUnprocessedDomainEvents` (only `seat.*` types, `NOT EXISTS` in `processed_events`, `ORDER BY outbox_seq`).
  3. Per event: `stateForEventType` → decode payload → `ordinalOf` (cached seat id to ordinal map per event) → `apply`.
  4. `apply`: `EnsureBitset` first (the truncated-bitset fix) → `GETRANGE` one byte → if state equal, return no-op → `SETRANGE` → `INCR seq` → `EncodeSparse` → pipeline `LPUSH` + `LTRIM 0..199` + `PUBLISH {event:E}:notify`.
  5. On success or a skip, `markProcessed`. On a Redis error, return without marking, so the event is retried.
  6. Read side: `EnsureBitset` (SETNX seed from Postgres), `SeatCount`, `GapFill` (reads the ring, checks the oldest wanted seq equals `sinceSeq+1`, folds last-write-wins per ordinal, one `EncodeSparse` at the current seq, else `ok=false`).
- **Data flow**: `domain_events` rows → state per ordinal → Redis keys `{event:E}:bitset`, `:seq`, `:deltas`, channel `:notify`. Unknown types, bad payloads and unknown seats are logged and marked processed (skipped for good).
- **How it is tested**: [internal/projector/integration_test.go](../../../internal/projector/integration_test.go), build tag `integration`, real Postgres and Redis: `TestProjector_SeatHeldThenReleased` (seq 1 then 2, idle tick processes 0), `TestProjector_ConfirmProducesSoldState`, `TestGapFill_CoversRecentGapAndOverflowTriggersResync` (coalesced frame with seq 3; after 220 more events `GapFill` from seq 1 returns `ok=false`). The `apply` steps are not atomic (no MULTI/EXEC); no test covers a crash between them.
- **Read next**: [internal/wshub/wshub.go](../../../internal/wshub/wshub.go).

## [internal/wshub/wshub.go](../../../internal/wshub/wshub.go)
- **What it does**: Terminates `GET /ws`. Sends a snapshot or gap-fill, then live deltas. Applies the local fidelity patches.
- **Control flow**:
  1. `ServeWS`: parse `event`, verify JWT from the `token` query param (browsers cannot set WS headers), parse optional `sinceSeq` → `upgrader.Upgrade`.
  2. `q.InsertWSConnection` (non-fatal on error).
  3. `h.subscribe` BEFORE building frames. First connection for an event starts one `pumpRedis` goroutine.
  4. `sendInitialFrames`: if `sinceSeq>0` → `proj.GapFill`; if covered, write one frame (or nothing if caught up); else fall to `EnsureBitset` + `SeatCount` + `GetEvent` → `EncodeSnapshot`, with a 128 KB check.
  5. `go writeLoop` (live frames from `c.send`, ping every `IdleTimeout/3`, 1% `chaosDisconnect`) and `readLoop` (read limit 128 KB, 10-minute deadline refreshed by pong).
  6. On exit the deferred `unsubscribe` closes the socket, calls `q.CloseWSConnection`, and cancels the Redis subscription if no conns remain.
- **Data flow**: Redis pub/sub bytes → each conn's buffered channel (64). A full buffer drops the frame with a log line. The snapshot's "event id hash" field is just `uint32(eventID)`.
- **How it is tested**: no `wshub` test file. Verified live by hand with `ws-test-client` (snapshot, live delta, gap fill, ring overflow, chaos disconnect), per MILESTONES. The 10-minute idle timeout was checked by code review, not by waiting. The 128 KB cap is never reached at today's scale.
- **Read next**: [internal/httpapi/catalog.go](../../../internal/httpapi/catalog.go).

> Note: `ws_connections.last_seq_sent` exists and `UpdateWSConnectionSeq` is generated by sqlc, but no Go code calls it (grep). The column stays 0.

## [internal/httpapi/catalog.go](../../../internal/httpapi/catalog.go)
- **What it does**: `getAvailability` serves `GET /events/{id}/availability`: Redis bitset first, Postgres scan as fallback.
- **Control flow**: `getAvailability` → if `c.proj != nil` → `tryRedisAvailability` (`EnsureBitset` + `SeatCount`; any error returns ok=false) → `writeAvailability` with `X-Seatmap-Version`. Else `q.ListEventSeatsForAvailability` → build bitset with `seatmap.Set` → `writeAvailability` without the version header.
- **Data flow**: bytes in the body, `ETag` is the SHA-256 of the bitset (a matching `If-None-Match` gets 304), `X-Seat-Count` always, `X-Seatmap-Version` only on the Redis path.
- **How it is tested**: no `httpapi` test file. Verified live: Redis up gives the version header, `docker stop` Redis gives 200 without it and the same ETag.
- **Read next**: [scripts/ws-test-client/main.go](../../../scripts/ws-test-client/main.go).

## [scripts/ws-test-client/main.go](../../../scripts/ws-test-client/main.go)
- **What it does**: A manual Go WebSocket client that prints raw hex and decoded frames. Not a test binary.
- **Control flow**: flags `-event`, `-token`, `-sinceSeq`, `-addr`, `-count` → dial `ws://addr/ws?...` → read `count` frames → `wsproto.Decode` → print.
- **Data flow**: a Cognito ID token goes in the query string. Frames come out as text.
- **How it is tested**: it is the verification tool itself.
- **Read next**: [migrations/000007_ws_connections.up.sql](../../../migrations/000007_ws_connections.up.sql), then [Phase 8 FILES](../phase-8-waiting-room-aimd/FILES.md).

## [migrations/000007_ws_connections.up.sql](../../../migrations/000007_ws_connections.up.sql) and [migrations/000008_domain_events_outbox_seq.up.sql](../../../migrations/000008_domain_events_outbox_seq.up.sql)
- **What it does**: 0007 creates the `ws_connections` registry (Postgres, by design decision #7). 0008 adds `domain_events.outbox_seq BIGSERIAL` and an index.
- **Control flow**: `make migrate`. The outbox relay and the projector both `ORDER BY outbox_seq`.
- **Data flow**: `wshub` inserts and closes connection rows. Producers get `outbox_seq` automatically at insert.
- **How it is tested**: 0008's effect is covered by the projector overflow test. 0007 has no direct test.
- **Read next**: [internal/projector/projector.go](../../../internal/projector/projector.go).
