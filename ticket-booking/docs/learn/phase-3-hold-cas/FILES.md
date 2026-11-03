# Phase 3 — Hold / Release / Confirm (CAS): Files

Siblings: [BASICS.md](BASICS.md) · [QUESTIONS.md](QUESTIONS.md) · [learn index](../README.md)

Build tags matter here. Without `-tags=integration`, only [internal/inventory/inventory_test.go](../../../internal/inventory/inventory_test.go) and [internal/inventory/best_available_test.go](../../../internal/inventory/best_available_test.go) run. [internal/inventory/integration_test.go](../../../internal/inventory/integration_test.go) and [internal/holdlock/holdlock_test.go](../../../internal/holdlock/holdlock_test.go) both start with `//go:build integration` and need real Postgres and Redis.

> Note: `inventory.go` now also writes outbox rows (`events.Publish`) inside the same transactions. MILESTONES attributes the outbox to Phase 5. This page describes the current code.

## [internal/holdlock/holdlock.go](../../../internal/holdlock/holdlock.go)

- **What it does**: the Redis contention filter. `Acquire` does `SET key holdID NX PX ttl`. `Release` runs a Lua compare-and-delete. Never the arbiter.
- **Control flow**:
  1. `wiring.New` calls `holdlock.New(cfg.RedisAddr)` and gives it to `inventory.New` as a `Locker`.
  2. `AcquireHold` calls `Lock.Acquire(ctx, eventID, seatID, holdID, ttl)` once per seat.
  3. `Acquire` builds the key with `key()`: `{event:%d}:seat:%d:hold` (hash tag for future cluster mode), then `rdb.SetNX`.
  4. Result: `(true, nil)` won, `(false, nil)` key already held, `(false, ErrUnavailable-wrapped)` Redis unreachable.
  5. `Release` runs `releaseScript` through `rdb.Eval`. A `redis.Nil` reply is ignored.
- **Data flow**: `(eventID, seatID, holdID, ttl)` → one Redis string key whose value is the hold ID → bool. Nothing in Postgres.
- **How it is tested**: integration only, [internal/holdlock/holdlock_test.go](../../../internal/holdlock/holdlock_test.go) (hard-coded `localhost:6379`): `TestAcquire_FirstWinsSecondLoses`, `TestRelease_OnlyCurrentHolderCanDelete`, `TestAcquire_ExpiresAfterTTL`, `TestAcquire_UnreachableRedisReturnsErrUnavailable`. The last one only asserts `err != nil`, not `errors.Is(err, ErrUnavailable)`, so its name promises more than it checks.
- **Read next**: [sqlc/queries.sql](../../../sqlc/queries.sql).

## [sqlc/queries.sql](../../../sqlc/queries.sql) (Phase 3 statements)

- **What it does**: the four CAS statements that write `event_seats`: `AcquireHold`, `ReleaseHold`, `ExtendHold`, `ConfirmSeats`, plus `GetSeatIDsByOrdinals`, `InsertHoldsAudit`, `GetSeatsByHoldID`, and `ListAvailableForBestAvailable`.
- **Control flow**:
  1. `AcquireHold` (`:many`) sets `status=1`, `hold_id`, `held_by`, `hold_expires_at = now() + ttl`, `fence_token = fence_token + 1`, `WHERE event_id AND seat_id = ANY(...) AND sellable AND (status = 0 OR (status IN (1,3) AND hold_expires_at < now()))`, and `RETURNING` the winning rows.
  2. `ReleaseHold` (`:execrows`) resets to status 0 where `status IN (1,3) AND hold_id = ...`.
  3. `ExtendHold` (`:execrows`) sets `status=3` and pushes `hold_expires_at` out with `greatest(...)` where the hold is live (`hold_expires_at > now()`).
  4. `ConfirmSeats` (`:execrows`) sets `status=2`, `booking_id`, clears hold columns, joining two `unnest(...) WITH ORDINALITY` arrays so each seat is matched with its own fence, requiring `fence_token = f.fence` and `hold_expires_at > now()`.
- **Data flow**: parameters (event, seat IDs, hold ID, user sub, TTL, fences) → one atomic `UPDATE` → returned rows or a row count. The count or row list is the entire win/lose signal.
- **How it is tested**: through [internal/inventory/integration_test.go](../../../internal/inventory/integration_test.go) against real Postgres. The SQL is not tested in isolation. MILESTONES also records a hand-run psql check that a partial `ConfirmSeats` match rolls back.
- **Read next**: [internal/inventory/inventory.go](../../../internal/inventory/inventory.go).

## [internal/inventory/inventory.go](../../../internal/inventory/inventory.go)

- **What it does**: the correctness core. `Service` has `AcquireHold`, `ReleaseHold`, `ExtendHold`, `ConfirmSeats`. Its package comment states the invariant that no other package writes `event_seats`.
- **Control flow** (`AcquireHold`):
  1. `sortDedup(seatIDs)`. A duplicate gives `ErrDuplicateSeat`. Zero seats gives an error. More than `maxSeatsPerHold` gives `ErrTooManySeats`. All before any I/O.
  2. `holdID := uuid.New()`.
  3. For each seat ascending, `s.lock.Acquire`. On `ok=false`: `releaseRedisBatch`, `audit(... "LOST_REDIS" ...)`, return `ConflictError{[seatID]}`. On error: log and `break` (fail-open).
  4. `s.pool.Begin`, then `qtx.AcquireHold` (the CAS).
  5. If `len(rows) != len(seatIDs)`: rollback, release Redis keys, compute the seats that did not come back, audit `LOST_DB_CAS` for each, return `ConflictError` with all of them.
  6. Else `events.Publish(... "seat.held" ...)` per seat in the same transaction, then `tx.Commit`.
  7. Audit `ACQUIRED` per seat (best effort, after commit).
- **Control flow** (others): `ReleaseHold` releases Redis keys (errors only logged), then a transaction with `qtx.ReleaseHold`; it publishes `seat.released` only if the row count is above 0. `ExtendHold` calls `s.q.ExtendHold` and returns `ErrHoldExpired` if rows are fewer than seats. `ConfirmSeats` sorts seats and fences together, runs `qtx.ConfirmSeats`, and if the count is short rolls back and audits `STALE_FENCE_REJECTED`, returning an error wrapping `ErrHoldExpired`; else publishes `seat.booked` and commits.
- **Data flow**: seat IDs + user sub → Redis keys (filter) → one Postgres transaction (`event_seats` rows, `domain_events` outbox rows) → `*Hold` (hold ID, expiry, seats with fence tokens, prices). `holds_audit` rows are written afterwards through the plain `Querier`.
- **How it is tested**: unit tests in [internal/inventory/inventory_test.go](../../../internal/inventory/inventory_test.go) (validation, `ExtendHold` row counts, sorting) and the race tests in [internal/inventory/integration_test.go](../../../internal/inventory/integration_test.go).
- **Read next**: [internal/inventory/best_available.go](../../../internal/inventory/best_available.go).

## [internal/inventory/best_available.go](../../../internal/inventory/best_available.go)

- **What it does**: picks adjacent seats for a quantity and holds them.
- **Control flow**:
  1. `BestAvailable` rejects `quantity <= 0` and `quantity > maxSeatsPerHold`.
  2. `ListAvailableForBestAvailable` returns candidates (status 0, sellable, `price_cents <= max`) ordered by ordinal, with `row_id`.
  3. `findContiguousRuns(candidates, quantity)` slides a window. A run needs the same `RowID` and consecutive ordinals.
  4. It tries `AcquireHold` on the first up-to-3 runs. Only `ErrSeatTaken` moves to the next run. Any other error returns at once.
  5. Out of runs or attempts gives `ErrNoContiguousSeats`.
- **Data flow**: candidate rows → `[][]int64` runs of seat IDs → `AcquireHold` → `*Hold`.
- **How it is tested**: unit [internal/inventory/best_available_test.go](../../../internal/inventory/best_available_test.go): `TestFindContiguousRuns_WithinOneRow`, `..._BreaksAtRowBoundary`, `..._NoneWhenTooFewCandidates`, `..._SkipsAGapInOrdinals`. Integration: `TestBestAvailable_AcquiresARealContiguousRun`, `TestBestAvailable_NoRunLargeEnoughReturnsErrNoContiguousSeats`. The retry-on-conflict loop has no dedicated test.
- **Read next**: [internal/httpapi/holds.go](../../../internal/httpapi/holds.go).

## [internal/httpapi/holds.go](../../../internal/httpapi/holds.go)

- **What it does**: the HTTP surface for holds: create, get, delete, extend. All inside the authenticated route group.
- **Control flow** (`createHold`):
  1. Router: `authed.With(wq.RequireAdmission).Post("/events/{eventID}/holds", holds.createHold)` when a waiting-room queue is wired (Phase 8), else plain.
  2. Parse `eventID`, read claims with `auth.FromContext`, decode JSON.
  3. If `bestAvailable`: `h.inv.BestAvailable(...)`. Else `resolveOrdinals` (calls `GetSeatIDsByOrdinals`, fails if any ordinal is missing) then `h.inv.AcquireHold(...)`.
  4. Record latency and `isSystemError(err)` into `metrics` if set.
  5. `writeHoldResultOrError`: success is 201 with `holdResponse`; errors go through `writeInventoryError`.
- **Control flow** (others): `getHold` and `deleteHold` look up by `GetSeatsByHoldID`, and check `rows[0].HeldBy.String == claims.Sub`. `getHold` returns 410 for a missing or foreign hold. `deleteHold` returns 204 for a missing hold and for a foreign hold (so it does not leak whether the ID exists), and after a successful release. `extendHold` defaults to 180 seconds.
- **Data flow**: JSON body (ordinals or quantity) → seat IDs → inventory → `Hold` → JSON with `holdId`, `seats`, `expiresAt`, `serverTime`, `totalCents`, `fenceTokens`.
- **How it is tested**: no Go test for the handlers. `curl` output in MILESTONES (201, 409 `SEAT_TAKEN`, 204, 410, re-hold with fence 1 then 2). The Playwright E2E in [web/e2e/hold-flow.spec.ts](../../../web/e2e/hold-flow.spec.ts) later drives it from a browser.
- **Read next**: [internal/inventory/integration_test.go](../../../internal/inventory/integration_test.go).

> Note: `ConflictError.Conflicts` holds seat IDs (database IDs), not ordinals, and the 409 body, `seats[].seatId`, and `fenceTokens` keys are seat IDs too. [plan.md](../../plan.md) says ordinals are the only seat identity the client sees. The web client's `ConflictBody` type has `conflicts: number[]` but I found no code that reads it.

> Note: the `createHold` doc comment says admission-token verification is "not yet wired". The router wires it. The comment is stale.

## [internal/inventory/integration_test.go](../../../internal/inventory/integration_test.go)

- **What it does**: the thesis tests, against real Postgres and Redis. Needs venue 1 seeded (`go run ./scripts/seed-venue`).
- **Control flow**:
  1. Helpers: `seedTestEvent` creates a fresh event per test, `seedTestEventSeats` inserts `event_seats` rows reusing real `seat_id`s.
  2. `TestAcquireHold_ExactlyOneWinnerUnderRace`: 20 parallel subtests, each with 200 goroutines released by `close(start)`. Expects 1 win, 199 `ErrSeatTaken`, and `assertExactlyOneHeld` checks `event_seats` and `holds_audit` directly.
  3. `TestAcquireHold_ExactlyOneWinnerUnderRace_WithRedisFlushed`: same race while a goroutine runs `FlushAll` every 5 ms.
  4. `TestConfirm_ExactlyOneWinner`: 50 concurrent confirms of one hold, 1 success.
  5. `TestExpiredHoldIsReclaimed`: expired row seeded by raw SQL, no Redis, no reaper. `TestStaleFenceRejected`: 150 ms TTL, 300 ms sleep, reclaim, stale confirm fails, real confirm works.
  6. `TestMultiSeatOverlap_LoserSeatsStayAvailableNotOrphaned`, `TestBestAvailable_*`, `TestReleaseHold_IdempotentOnAlreadyGoneHold`.
- **Data flow**: test goroutines → `Service` → real Postgres/Redis → direct SQL assertions on the final rows.
- **How it is tested**: this is the test. Integration, build tag `integration`. Run with `make test-integration` or `go test -tags=integration ./internal/inventory/...`.
- **Read next**: [internal/inventory/inventory_test.go](../../../internal/inventory/inventory_test.go).

> Note: MILESTONES lists a gomock test `TestReleaseHold_RowcountZeroIsSuccessNotError`. It does not exist. A comment in `inventory_test.go` says that coverage moved to the integration test `TestReleaseHold_IdempotentOnAlreadyGoneHold` because `ReleaseHold` now opens its own transaction.

## [internal/inventory/inventory_test.go](../../../internal/inventory/inventory_test.go)

- **What it does**: fast unit tests with a gomock `MockQuerier` and a no-op `fakeLocker`. `Service` is built with a nil pool, which is safe only because the tested paths return before any I/O.
- **Control flow**: `newTestService` creates the controller, mock, and service. Tests: `TestAcquireHold_DuplicateSeatRejectedBeforeAnyIO`, `TestAcquireHold_TooManySeatsRejectedBeforeAnyIO`, `TestAcquireHold_EmptySeatListRejected`, `TestExtendHold_ShortRowcountReturnsErrHoldExpired`, `TestExtendHold_FullRowcountIsSuccess`, `TestConfirmSeats_MismatchedLengthsRejectedBeforeAnyIO`, `TestSortDedup`, `TestSortSeatsAndFencesTogether`.
- **Data flow**: inputs → validation errors or mocked row counts → asserted error values.
- **How it is tested**: this is the test. Unit, no build tag, runs in `make test` and CI.
- **Read next**: [migrations/000003_holds_audit.up.sql](../../../migrations/000003_holds_audit.up.sql), then [Phase 4 FILES](../phase-4-canvas-seatmap-a11y/FILES.md).

## [migrations/000003_holds_audit.up.sql](../../../migrations/000003_holds_audit.up.sql) and [migrations/000004_event_seats_hold_lookup.up.sql](../../../migrations/000004_event_seats_hold_lookup.up.sql)

- **What it does**: 000003 creates `holds_audit` (hold, event, seat, user, `outcome`, fence, expiry, latency) with an index on `(event_id, seat_id, created_at)`. 000004 adds the partial index `event_seats_hold_id_idx ON event_seats (hold_id) WHERE hold_id IS NOT NULL`.
- **Control flow**: `inventory.audit` inserts via `InsertHoldsAudit`. `GetSeatsByHoldID` uses the partial index so `GET/DELETE /holds/{id}` find a hold by ID alone.
- **Data flow**: each hold or release outcome becomes one `holds_audit` row per seat. There is no separate `holds` table. Hold state lives in `event_seats`.
- **How it is tested**: `assertExactlyOneHeld` counts `ACQUIRED` audit rows. The index has no test.
- **Read next**: [Phase 4 FILES](../phase-4-canvas-seatmap-a11y/FILES.md).
