# Phase 8 — Virtual Waiting Room and AIMD: Files

Siblings: [BASICS.md](BASICS.md) · [QUESTIONS.md](QUESTIONS.md) · [learn index](../README.md)

> Note: [internal/httpapi/holds.go](../../../internal/httpapi/holds.go) still has a comment on `createHold` saying admission is "Phase 8; not yet wired here". That comment is stale. [internal/httpapi/router.go](../../../internal/httpapi/router.go) mounts `wq.RequireAdmission` on `POST /events/{eventID}/holds`.

> Note: [deep-dive.md](../../deep-dive.md) section 7 sketches an increase rule of "p99 under 250 ms and pool under 60%". The code increases whenever no signal is red (p99 at most 500 ms, pool at most 80%, errors at most 1%). Code wins.

## [internal/waitingroom/queue.go](../../../internal/waitingroom/queue.go)
- **What it does**: `Queue.Join` puts a user in the line and says admitted-or-queued.
- **Control flow**:
  1. `httpapi.waitingRoomAPI.joinQueue` → `Queue.Join(ctx, eventID, sub)`.
  2. `ZAddNX` on `{event:E}:q` (score is `time.Now().UnixMicro()`).
  3. `ZRank` for the sub, then `cursorValue` (`{event:E}:cursor`, 0 if missing).
  4. If `rank < cursor`: `issueToken` → `JoinResult{Admitted: true, Token}`.
  5. Else `currentRate` (`{event:E}:rate`, `DefaultRate` 1.0 if missing) → `Position = rank - cursor`, `EtaSeconds = int(position / rate)`.
- **Data flow**: sub in → Redis ZSET entry, never removed by this code → result out. A user who is already admitted gets a fresh token on every `Join` call.
- **How it is tested**: [internal/waitingroom/integration_test.go](../../../internal/waitingroom/integration_test.go), build tag `integration`, real Redis and Postgres: `TestQueue_JoinBeforeCursorStaysQueued` (rank 0 is not below cursor 0), `TestQueue_AdmittedOnceCursorPassesRank`, `TestQueue_RejoinKeepsOriginalPlaceInLine`. Scale check by [scripts/queue-loadtest/main.go](../../../scripts/queue-loadtest/main.go) (a tool, not a test).
- **Read next**: [internal/waitingroom/controller.go](../../../internal/waitingroom/controller.go).

## [internal/waitingroom/controller.go](../../../internal/waitingroom/controller.go)
- **What it does**: `Controller.Tick` is one AIMD step for one event.
- **Control flow**:
  1. `runAIMDLoop` in [internal/wiring/wiring.go](../../../internal/wiring/wiring.go) ticks every `TickInterval` (1s), calls `q.ListAllEventIDs`, then `controller.Tick(ctx, eventID)` per event.
  2. `Tick`: `metrics.P99()`, `metrics.ErrorRate()`, `pool.Stat()` (acquired / max).
  3. Red flags: latency above 500 ms, pool above 0.80, errors above 0.01.
  4. Any red: `rate = max(rate*0.5, MinRate)`. Else `rate = min(rate+AdditiveStep, MaxRate)`. `MinRate` 1.0, `MaxRate` 1000.0, `AdditiveStep` 5.0.
  5. `rdb.Set` rate → `rdb.IncrBy` cursor by `int64(rate * 1s)` → `q.InsertWaitingRoomAudit`.
- **Data flow**: three live signals plus the previous rate → new rate → cursor advance → one audit row. The cursor advances every tick whether or not anyone is queued.
- **How it is tested**: same integration file: `TestController_AllGreenIncreasesRate` (rate becomes `DefaultRate + AdditiveStep`, one audit row), `TestController_ClampedPoolTriggersRateHalvingWithinThreeTicks` (pool clamped to 1 conn and held; rate 100 goes to 50, 25, 12.5). The latency and error signals feeding red are not integration-tested here; only the pool signal is.
- **Read next**: [internal/waitingroom/metrics.go](../../../internal/waitingroom/metrics.go).

## [internal/waitingroom/metrics.go](../../../internal/waitingroom/metrics.go)
- **What it does**: `HoldMetrics`, a mutex-guarded ring buffer of latencies and failure flags. Gives `P99` and `ErrorRate`.
- **Control flow**: `holdsAPI.createHold` → `metrics.Record(time.Since(start), isSystemError(err))` after every hold attempt. `Controller.Tick` → `P99()` / `ErrorRate()` → `snapshot()` copies under the lock.
- **Data flow**: (duration, failed) in → overwrite slot `idx` → on read, sort a copy and take index `int(n*0.99)`. Window is the last 200 calls (`NewHoldMetrics(200)` in wiring), not a time window.
- **How it is tested**: [internal/waitingroom/metrics_test.go](../../../internal/waitingroom/metrics_test.go), plain unit tests: `TestHoldMetrics_P99AndErrorRate`, `TestHoldMetrics_EmptyIsZero`, `TestHoldMetrics_RingBufferWrapsAndForgetsOldSamples`.
- **Read next**: [internal/waitingroom/token.go](../../../internal/waitingroom/token.go).

## [internal/waitingroom/token.go](../../../internal/waitingroom/token.go) and [internal/waitingroom/middleware.go](../../../internal/waitingroom/middleware.go)
- **What it does**: Issue and verify admission tokens. `RequireAdmission` enforces them on `POST .../holds`.
- **Control flow**: `Queue.Join` → `issueToken(sub, eventID, pos)` (payload `{sub,eid,pos,iat,exp,nonce}`, HMAC-SHA256 signed with `WAITING_ROOM_SECRET`). On a hold request: `verifier.Middleware` (sets claims) → `RequireAdmission` → parse `eventID` → read `X-Admission-Token` → `VerifyToken(token, claims.Sub, eventID)` → next handler, or 403 `not_admitted`.
- **Data flow**: `VerifyToken` checks `v1` prefix and 3 parts, signature with `hmac.Equal`, event id, then sub (`ErrSubMismatch`), then expiry (`ErrTokenExpired`). The nonce is carried but never checked against a use-once store (comment says so).
- **How it is tested**: [internal/waitingroom/token_test.go](../../../internal/waitingroom/token_test.go), unit: `TestToken_IssueAndVerifyRoundTrip`, `TestToken_TamperedSignatureRejected`, `TestToken_WrongSubjectRejected`, `TestToken_WrongEventRejected`, `TestToken_ExpiredRejected`, `TestToken_MalformedTokenRejected`. `RequireAdmission` itself has no Go test; MILESTONES verified it live with curl (no token 403, valid token 201, tampered 403, other user's token 403).
- **Read next**: [internal/httpapi/waitingroom.go](../../../internal/httpapi/waitingroom.go).

> Note: `WAITING_ROOM_SECRET` defaults to `local-dev-only-insecure-secret` in [internal/config/config.go](../../../internal/config/config.go). Fine for local, wrong for anything real.

## [internal/httpapi/waitingroom.go](../../../internal/httpapi/waitingroom.go)
- **What it does**: `POST /events/{eventID}/queue`.
- **Control flow**: route is in the authed group of [internal/httpapi/router.go](../../../internal/httpapi/router.go) → `joinQueue` → parse event id → claims → `Queue.Join` → admitted: 200 `{admissionToken}`; queued: 202 `{position, etaSeconds, pollAfterMs: 2000}`; error: 500.
- **Data flow**: JWT sub in → Queue result → JSON out.
- **How it is tested**: no `httpapi` test. Verified live per MILESTONES.
- **Read next**: [internal/wiring/wiring.go](../../../internal/wiring/wiring.go).

## [internal/wiring/wiring.go](../../../internal/wiring/wiring.go)
- **What it does**: Builds the app for both `cmd/server` and `cmd/server-lambda`. For Phase 8 it creates `HoldMetrics`, `Queue`, `Controller`, and starts `go runAIMDLoop`.
- **Control flow**: `wiring.New` → `NewHoldMetrics(200)` → `waitingroom.New(rdb, cfg.WaitingRoomSecret)` → `NewController(q, rdb, pool, metrics)` → `go runAIMDLoop(ctx, q, controller)` → `httpapi.NewRouter(..., wq, metrics, ...)`.
- **Data flow**: the same `metrics` object is written by the hold handler and read by the controller, in one process.
- **How it is tested**: not tested directly.
- **Read next**: [migrations/000009_waiting_room_audit.up.sql](../../../migrations/000009_waiting_room_audit.up.sql), then [web/src/api/queue.ts](../../../web/src/api/queue.ts).

## [web/src/api/queue.ts](../../../web/src/api/queue.ts) and [scripts/queue-loadtest/main.go](../../../scripts/queue-loadtest/main.go)
- **What it does**: `joinQueue` and `waitForAdmission` call the queue endpoint and poll at the server's `pollAfterMs`, up to 30 seconds, then throw. `queue-loadtest` calls `Queue.Join` directly with N distinct synthetic subs (flags `-event`, `-n`, `-concurrency`, `-addr`).
- **Control flow**: `EventPage.tsx` flow → `waitForAdmission` → repeated `joinQueue` → token → `X-Admission-Token` on hold requests via [web/src/api/holds.ts](../../../web/src/api/holds.ts). The load tool skips HTTP and auth on purpose.
- **Data flow**: ID token in → admission token out. The load tool prints admitted/queued/errors and elapsed time.
- **How it is tested**: no frontend test for `queue.ts`. The load tool is the verification for the 5,000-arrival claim (numbers in MILESTONES: one local machine, direct Redis, not a capacity claim).
- **Read next**: [Phase 9 FILES](../phase-9-qr-reminders/FILES.md).
