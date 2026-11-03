# Phase 10 — Load Harness: Files

Siblings: [BASICS.md](BASICS.md) · [QUESTIONS.md](QUESTIONS.md) · [Learn index](../README.md) · Scope: [MILESTONES.md Phase 10](../../../MILESTONES.md)

> Note: MILESTONES says WS lag is "time from a worker's `POST /holds` to that ordinal's delta arriving". In code, `onHold` is called after `acquireHold` returns, with `time.Now()` at that moment. So the clock starts at the hold response, not at the request. Only successful holds are recorded.

> Note: no test file exists under `scripts/`. The harness is a test tool, and it has no tests of its own. There is no Makefile target for it. Run it with `go run ./scripts/loadtest` (see the "Load testing" section of the [README](../../../README.md)).

## [scripts/loadtest/main.go](../../../scripts/loadtest/main.go)

- **What it does**: entry point. Parses flags, provisions users, starts the WS observer, starts workers, writes a CSV, prints the report and runs the DB invariant queries.
- **Control flow**:
  1. `main` parses flags (`-workers`, `-users`, `-duration`, `-ramp-up`, `-contention`, `-hot-ordinal`, `-csv`, `-pool-id`, `-client-id`, `-event`, and more). `-pool-id` and `-client-id` are required.
  2. `provisionUsers` returns ID tokens.
  3. `newWSLagObserver()` then `go lagObserver.run(...)` with the first token.
  4. Sets `runner.pickSeat` (hot: constant; otherwise `rng.Intn(30000)`) and `runner.onHold = lagObserver.noteHoldRequested`.
  5. Starts `workers` goroutines. Worker `w` waits `w * rampUp / workers`, then loops `runner.run(ctx, nextSeq(), token)` until `deadline`, sending each `Result` on a channel.
  6. The main goroutine drains the channel into the CSV and a slice, then calls `printReport`.
  7. `printReport` computes percentiles (sort, index `int(p*n)`) and calls `lag.lagPercentiles()` and `runDBInvariants`.
- **Data flow**: flags → many `Result` structs → CSV rows (`seq, startedAt, outcome, seatOrdinal, holdMs, orderMs, totalMs`) + printed summary. Hold percentiles only include journeys where `HoldMs > 0`. The "saga compensation count" query is meant to count all `COMPENSATED` confirm steps in the database, not only this run's. As written in `runDBInvariants` it filters `order_saga_steps` on a column `status`, but the column is `state`, so the query errors and the line is silently skipped (the call site ignores the error). That is a source bug. The "saga compensation count: 0" in MILESTONES cannot have come from this code as it stands. The duplicate-ticket query matches the one in [docs/plan.md](../../plan.md) (same SQL, spacing aside).
- **How it is tested**: not unit-tested. Verified by live runs recorded in MILESTONES (uniform and hot scenarios).
- **Read next**: [journey.go](../../../scripts/loadtest/journey.go).

## [scripts/loadtest/journey.go](../../../scripts/loadtest/journey.go)

- **What it does**: `journeyRunner.run` plays one full customer journey over HTTP and returns a `Result` with an `Outcome`.
- **Control flow**:
  1. `run` → `joinQueue` (`POST /api/v1/events/{id}/queue`, up to 5 attempts, waits `pollAfterMs`) → returns `admissionToken`.
  2. `pickSeat()` → `acquireHold` (`POST /api/v1/events/{id}/holds`, headers `Authorization` and `X-Admission-Token`). 409 means `OutcomeSeatTaken`; any other non-201 means `OutcomeError`.
  3. `onHold(ordinal, time.Now())` tells the WS observer.
  4. `createOrder` (`POST /api/v1/orders`, expects 202) → `pollOrder` (`GET /api/v1/orders/{id}` every 150 ms, 5 s limit).
  5. `pollOrder` maps `TICKETED` to `OutcomeTicketed`, `FAILED`/`COMPENSATED` to `OutcomeOrderFailed`, timeout to `OutcomeError`.
- **Data flow**: token + ordinal → HTTP calls → timings `HoldMs` (hold call only), `OrderMs` (create + poll), `TotalMs` (whole journey).
- **How it is tested**: no unit tests. It drives server endpoints that have their own tests; routes are in [internal/httpapi/router.go](../../../internal/httpapi/router.go).
- **Read next**: [users.go](../../../scripts/loadtest/users.go).

## [scripts/loadtest/users.go](../../../scripts/loadtest/users.go)

- **What it does**: `provisionUsers` makes `n` real users in cognito-local and returns their ID tokens.
- **Control flow**:
  1. [main.go](../../../scripts/loadtest/main.go) calls `provisionUsers(ctx, endpoint, poolID, clientID, n)`.
  2. For each `loadtest-user-<i>@example.com`: `AdminCreateUser` (failure is logged and skipped: "likely exists") → `AdminSetUserPassword` (permanent) → `AdminInitiateAuth` with `AdminUserPasswordAuth`.
- **Data flow**: pool id + client id → `[]string` of ID tokens (JWTs). Workers use `tokens[workerIdx % len(tokens)]`. The pool and client ids come from [scripts/seed-cognito-local.sh](../../../scripts/seed-cognito-local.sh), which writes them to `.env.local`.
- **How it is tested**: no tests. The doc comment states the scope cut: this does not measure identity-creation throughput.
- **Read next**: [wslag.go](../../../scripts/loadtest/wslag.go).

## [scripts/loadtest/wslag.go](../../../scripts/loadtest/wslag.go)

- **What it does**: one real WebSocket client that measures how long a seat delta takes to arrive after a hold.
- **Control flow**:
  1. `main` runs `run(addr, eventID, token, stop)` in a goroutine. It dials `/ws?event=<id>&token=<jwt>`.
  2. Workers call `noteHoldRequested(ordinal, at)`, which stores the time in a map keyed by ordinal.
  3. The read loop decodes frames with `wsproto.Decode`. For `OpSparse` frames it looks up each `ch.Ordinal`, appends the lag, and deletes the entry.
  4. `lagPercentiles` sorts a copy (insertion sort) and picks p50/p95/p99.
- **Data flow**: hold-time map + frames → `lagsMs` slice → percentiles. One map entry per ordinal: a second hold on the same ordinal before the frame arrives overwrites the first. If the dial fails, lag numbers are empty.
- **How it is tested**: no tests. The report prints `n=`, so you can see how few samples back the percentiles (MILESTONES shows `n=29` for the uniform run).
- **Read next**: [internal/wshub/wshub.go](../../../internal/wshub/wshub.go).

## [internal/wshub/wshub.go](../../../internal/wshub/wshub.go)

- **What it does**: the WebSocket hub. Phase 10 changed `writeLoop` so it never closes `c.send`, to fix a crash the load run found.
- **Control flow**:
  1. `ServeWS` registers the connection, starts `writeLoop(c)`, runs `readLoop`, and defers `unsubscribe`.
  2. One `pumpRedis` goroutine per event reads Redis pub/sub and sends each payload to every `c.send` under `sub.mu`, using a non-blocking `select` with a `default` that drops when the buffer is full.
  3. `writeLoop` sends frames and pings. On `chaosDisconnect()` it closes the websocket and returns.
  4. Closing the websocket unblocks `readLoop`'s `ReadMessage`, `ServeWS` returns, and `unsubscribe` removes the connection from `sub.conns`. After that, `pumpRedis` no longer sees it.
- **Data flow**: Redis notify message → `c.send` channel → binary WS frame.
- **How it is tested**: there is no test file in `internal/wshub`. The fix was verified by re-running both load scenarios and a full `make test-integration` pass, per MILESTONES. The race itself has no automated regression test.
- **Read next**: [Phase 11 files](../phase-11-iac-lambda/FILES.md).

## [Makefile](../../../Makefile) (`test-integration` target)

- **What it does**: runs `go test -tags=integration -p 1 ./...`. The `-p 1` (one package at a time) was added in Phase 9, not Phase 10. It is listed because the load harness and the integration suites share the same local Postgres, Redis and MinIO.
- **Control flow**: `make test-integration` → `go test` with tag `integration`.
- **Data flow**: test binaries → shared local Postgres, Redis, MinIO.
- **How it is tested**: this is the test runner itself.
- **Read next**: the "Load testing" section of [README.md](../../../README.md).
