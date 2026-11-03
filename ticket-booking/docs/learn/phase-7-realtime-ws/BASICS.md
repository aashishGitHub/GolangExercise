# Phase 7 — Realtime Projector and WebSocket Deltas: Basics

Siblings: [FILES.md](FILES.md) · [QUESTIONS.md](QUESTIONS.md) · [learn index](../README.md)

Scope: [MILESTONES.md, Phase 7](../../../MILESTONES.md). The browser seat map does not use any of this yet: no file under `web/src` opens a WebSocket (I grepped). The Phase 4 map still polls `GET /events/{id}/availability`. Only the Go test client [scripts/ws-test-client/main.go](../../../scripts/ws-test-client/main.go) talks to `/ws`.

1. **read model** — A copy of the data shaped for fast reading, built from the source of truth. Here the source is Postgres `event_seats`; the copy is a small block of bytes in Redis. Analogy: the departures board is a copy of the airline's real schedule. In this project: [internal/projector/projector.go](../../../internal/projector/projector.go).

2. **projector** — The program that builds and updates a read model by replaying events. It reads `seat.held`, `seat.released`, `seat.booked` rows from `domain_events` and changes the Redis copy. Analogy: the clerk who updates the departures board each time a gate agent calls. In this project: `Projector.RunOnce`, run by [cmd/projector/main.go](../../../cmd/projector/main.go) every 500 ms.

3. **bitset** — A row of bits packed tightly. Each seat gets 2 bits, so one byte holds 4 seats. 30,000 seats take 7,500 bytes. Analogy: one pigeonhole per seat holding a tiny coloured card. In this project: [internal/seatmap/seatmap.go](../../../internal/seatmap/seatmap.go) (`New`, `Get`, `Set`, `ByteLen`).

4. **seat state (2 bits)** — Four values: 0 FREE, 1 HELD, 2 SOLD, 3 UNAVAILABLE. A seat in the database state PENDING_PAYMENT shows as HELD. These numbers are locked because the frontend relies on them. In this project: `StateFromDB` in [internal/seatmap/seatmap.go](../../../internal/seatmap/seatmap.go).

5. **ordinal** — A seat's position number within the event, starting at 0. Wire messages use ordinals, not database seat ids. Analogy: "seat number 4 from the left" rather than the ticket serial number. In this project: `ordinalOf` in [internal/projector/projector.go](../../../internal/projector/projector.go).

6. **sequence number (seq)** — A counter per event that goes up by one every time a seat actually changes. Every update carries the seq it produced. A client uses it to tell "have I missed something?" Analogy: page numbers in a book; a missing page is obvious. In this project: Redis key `{event:E}:seq` in [internal/projector/projector.go](../../../internal/projector/projector.go).

7. **snapshot** — The whole bitset plus the current seq, sent when a client first connects. Frame type `0x01`. For a 30,000-seat event the frame is 7,520 bytes (20 byte header + 7,500 bytes), per MILESTONES. Analogy: handing someone the full map. In this project: `EncodeSnapshot` in [internal/wsproto/wsproto.go](../../../internal/wsproto/wsproto.go).

8. **delta** — A tiny message saying "seat X is now state Y at seq N". Frame type `0x02`, called SPARSE. Analogy: a one-line correction instead of reprinting the map. In this project: `EncodeSparse` in [internal/wsproto/wsproto.go](../../../internal/wsproto/wsproto.go).

9. **wire format** — The exact bytes sent over the network. Here it is binary, little-endian (low byte first), with an op byte and a protocol version byte at the start. Binary is smaller than JSON. In this project: package comment in [internal/wsproto/wsproto.go](../../../internal/wsproto/wsproto.go).

10. **0x03 RUN frame** — A planned frame for "a whole range changed". It is declared (`OpRun`) but never sent, because every event here changes exactly one seat. MILESTONES calls this an honest scope cut. In this project: [internal/wsproto/wsproto.go](../../../internal/wsproto/wsproto.go).

11. **WebSocket** — A long-lived two-way connection between browser and server. The server can push messages without being asked. Analogy: a phone call instead of sending letters. In this project: `Hub.ServeWS` in [internal/wshub/wshub.go](../../../internal/wshub/wshub.go), route `GET /ws` in [internal/httpapi/router.go](../../../internal/httpapi/router.go).

12. **pub/sub** — Redis feature where one side PUBLISHes to a named channel and every SUBSCRIBEr gets a copy. Analogy: a radio station. In this project: the projector publishes on `{event:E}:notify`; `pumpRedis` in [internal/wshub/wshub.go](../../../internal/wshub/wshub.go) listens.

13. **fan-out** — Sending one message to many receivers. The hub makes one Redis subscription per event, then copies each message to every connected client in Go. So Redis load does not grow with the number of viewers. In this project: `pumpRedis` in [internal/wshub/wshub.go](../../../internal/wshub/wshub.go).

14. **ring buffer (delta ring)** — A list that keeps only the newest N items and drops the oldest. Here N is 200 deltas per event (`ringCap`). Analogy: a dashcam that overwrites old footage. In this project: `LPUSH` + `LTRIM` in `apply`, [internal/projector/projector.go](../../../internal/projector/projector.go).

15. **gap fill (coalescing)** — A client that reconnects says "I last saw seq 4". If the ring still has everything after 4, the server sends ONE combined delta, keeping only the final state per seat. Analogy: "here is what changed while you were out", not a replay of every moment. In this project: `Projector.GapFill`.

16. **resync** — If the ring no longer reaches back far enough, the server sends a fresh snapshot instead. The client starts clean. One snapshot, not a storm of partial replays. In this project: `Hub.sendInitialFrames` in [internal/wshub/wshub.go](../../../internal/wshub/wshub.go).

17. **at-least-once and "ignore old seq"** — A client may see the same update twice. The rule: ignore anything with seq at or below what you already applied. Analogy: skipping a page you already read. In this project: the comment above `h.subscribe` in [internal/wshub/wshub.go](../../../internal/wshub/wshub.go). Note: the browser client that would apply this rule does not exist yet.

18. **subscribe before snapshot** — The hub subscribes to live updates first, then builds the snapshot. Otherwise an update published in between would be lost with nothing to notice. MILESTONES says this was caught in review before it ever ran. In this project: `ServeWS` in [internal/wshub/wshub.go](../../../internal/wshub/wshub.go).

19. **idempotent consumer (processed_events)** — The projector marks each event as processed per consumer name. Re-running does not apply it twice. In addition, `apply` skips an event if the bit already has the new state. In this project: `ListUnprocessedDomainEvents` in [sqlc/queries.sql](../../../sqlc/queries.sql) and `markProcessed`.

20. **ordering by outbox_seq** — The projector must apply changes in real order, so it sorts by a strict counter, not by timestamp. A real bug here was fixed in migration 0008. In this project: [migrations/000008_domain_events_outbox_seq.up.sql](../../../migrations/000008_domain_events_outbox_seq.up.sql).

21. **Redis is a cache, not the arbiter** — `GET /availability` reads the Redis bitset first and falls back to a Postgres scan if Redis errors. Redis down means slower or less fresh reads, never a wrong booking. In this project: `getAvailability` in [internal/httpapi/catalog.go](../../../internal/httpapi/catalog.go).

22. **local fidelity patches** — The local stack has no real AWS API Gateway WebSocket, so the hub copies its rough edges on purpose: 10-minute idle timeout, 128 KB frame cap, and a random 1% forced disconnect. That way reconnect code runs on every local run. In this project: constants at the top of [internal/wshub/wshub.go](../../../internal/wshub/wshub.go).
