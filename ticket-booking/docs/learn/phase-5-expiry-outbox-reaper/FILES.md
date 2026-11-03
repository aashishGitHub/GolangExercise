# Phase 5 — Expiry + Outbox + Reaper: Files

Siblings: [BASICS.md](BASICS.md) · [QUESTIONS.md](QUESTIONS.md) · [learn index](../README.md)

> Note: MILESTONES describes `cmd/hold-reaper` as doing the scan and release. In the code today the scan lives in [internal/reaper/reaper.go](../../../internal/reaper/reaper.go) (extracted in Phase 11). `cmd/hold-reaper` is only the ticker. Code wins.

> Note: MILESTONES calls `processed_events` a table shared with "the outbox relay". The relay does not use it. It tracks sent rows with `domain_events.published_at`. Only the Phase 7 projector writes `processed_events`.

## [internal/events/events.go](../../../internal/events/events.go)
- **What it does**: `Publish` inserts one `domain_events` row. It is the outbox writer.
- **Control flow**:
  1. `inventory.Service.AcquireHold`, `ReleaseHold`, `ConfirmSeats` call `events.Publish(ctx, qtx, ...)`.
  2. `qtx` is `db.New(tx)`, so the insert joins the CAS transaction. The caller must pass that transactional querier. Passing the pool querier would turn the outbox into a plain audit log.
  3. `Publish` marshals the payload and calls `q.InsertDomainEvent`.
- **Data flow**: Go map payload → JSON bytes → row `(event_id uuid.New(), aggregate_id, event_type, schema_version=1, payload)`. `published_at` stays NULL.
- **How it is tested**: no `events_test.go`. Covered indirectly by [internal/inventory/integration_test.go](../../../internal/inventory/integration_test.go) `TestReleaseHold_IdempotentOnAlreadyGoneHold`, which counts `seat.released` rows. Integration (`//go:build integration`, needs real Postgres and Redis).
- **Read next**: [internal/inventory/inventory.go](../../../internal/inventory/inventory.go).

## [internal/inventory/inventory.go](../../../internal/inventory/inventory.go)
- **What it does**: The only writer of `event_seats`. Phase 5 added an outbox write inside each of its three transactions.
- **Control flow**:
  1. `AcquireHold`: Redis filter → `pool.Begin` → `qtx.AcquireHold` (CAS) → one `events.Publish("seat.held")` per seat → `tx.Commit`.
  2. `ReleaseHold`: Redis `lock.Release` (best effort) → `pool.Begin` → `qtx.ReleaseHold` → if `n > 0`, `events.Publish("seat.released")` per seat → `tx.Commit` → `audit`.
  3. `ConfirmSeats`: CAS → on full match `events.Publish("seat.booked")` per seat → commit.
  4. Callers of `ReleaseHold`: the HTTP release handler, [internal/reaper/reaper.go](../../../internal/reaper/reaper.go), and the order saga (Phase 6).
- **Data flow**: seat ids + hold id in → `event_seats` row changes and `domain_events` rows out, same commit. Rollback drops both. A second `ReleaseHold` of a gone hold gives rowcount 0, so no event.
- **How it is tested**: [integration_test.go](../../../internal/inventory/integration_test.go): `TestReleaseHold_IdempotentOnAlreadyGoneHold` (one `seat.released` row after two releases) and `TestExpiredHoldIsReclaimed` (passive expiry, no reaper). Integration tag. Unit tests in [inventory_test.go](../../../internal/inventory/inventory_test.go) do not touch the outbox. Its comment says why: release now opens a real transaction, so a mock cannot cover it.
- **Read next**: [internal/outbox/relay.go](../../../internal/outbox/relay.go).

## [internal/outbox/relay.go](../../../internal/outbox/relay.go)
- **What it does**: `Relay.RunOnce` sends one batch (25 rows) of unpublished events to EventBridge and marks them published.
- **Control flow**:
  1. [cmd/outbox-relay/main.go](../../../cmd/outbox-relay/main.go) ticks every 2s and calls `relay.RunOnce`. The Lambda twin calls it once per invocation.
  2. `RunOnce` → `q.ListUnpublishedDomainEvents(batchSize)` (ordered by `outbox_seq`).
  3. For each row: marshal `Envelope` → `eb.PutEvents` (Source `ticketing.inventory`, DetailType = event type).
  4. On Go error or `FailedEntryCount > 0`: log and `continue`. The row stays unpublished.
  5. Else `q.MarkDomainEventPublished`.
- **Data flow**: `domain_events` row → `Envelope` JSON → EventBridge `Detail` → `published_at = now()`. Crash between step 3 and 5 means a resend next tick (at-least-once).
- **How it is tested**: no test file in `internal/outbox`. Verified live only, per MILESTONES Phase 5 (moto EventBridge, "published 25 event(s)" over several ticks). `Publisher` is a small interface so a fake is easy, but none was written.
- **Read next**: [internal/reaper/reaper.go](../../../internal/reaper/reaper.go).

## [internal/reaper/reaper.go](../../../internal/reaper/reaper.go)
- **What it does**: `RunOnce` actively releases every expired hold. UX freshness only.
- **Control flow**:
  1. [cmd/hold-reaper/main.go](../../../cmd/hold-reaper/main.go) ticks every 3s, or [cmd/hold-reaper-lambda/main.go](../../../cmd/hold-reaper-lambda/main.go) calls it once.
  2. `q.ListExpiredHolds` (distinct `event_id, hold_id` where status is 1 or 3 and `hold_expires_at < now()`).
  3. Per hold: `q.ListSeatIDsForHold` → skip if empty → `uuidFromPg` → `inv.ReleaseHold(..., "system:hold-reaper")`.
  4. Log per hold, count releases.
- **Data flow**: expired `event_seats` rows → seat ids → `ReleaseHold` → rows set to AVAILABLE + `seat.released` outbox rows → later published by the relay.
- **How it is tested**: no test file in `internal/reaper`. Live verification in MILESTONES Phase 5: reaper off, passive expiry still reclaims; reaper on, about 90 old holds swept, final count of expired-but-held rows is 0.
- **Read next**: [cmd/hold-reaper/main.go](../../../cmd/hold-reaper/main.go), then [cmd/outbox-relay/main.go](../../../cmd/outbox-relay/main.go).

## [cmd/hold-reaper/main.go](../../../cmd/hold-reaper/main.go) and [cmd/outbox-relay/main.go](../../../cmd/outbox-relay/main.go)
- **What it does**: Local ticker entrypoints. They only wire dependencies and loop.
- **Control flow**: `config.Load()` → `pgxpool.New` → build `db.New(pool)` → (reaper) `holdlock.New` + `inventory.New`, or (relay) AWS config + `eventbridge.NewFromConfig` with `EventBusEndpoint` override → `time.NewTicker` loop → `RunOnce`.
- **Data flow**: env (`DATABASE_URL`, `REDIS_ADDR`, `EVENT_BUS_ENDPOINT`, `EVENT_BUS_NAME`, `HOLD_TTL_SECONDS`) via [internal/config/config.go](../../../internal/config/config.go) → clients → `RunOnce`.
- **How it is tested**: not tested. They are thin `main` packages.
- **Read next**: [migrations/000005_domain_events.up.sql](../../../migrations/000005_domain_events.up.sql).

## [migrations/000005_domain_events.up.sql](../../../migrations/000005_domain_events.up.sql)
- **What it does**: Creates `domain_events` (the outbox) and `processed_events` (per-consumer dedup). Index `domain_events_unpublished_idx` is partial on `published_at IS NULL`.
- **Control flow**: applied by `make migrate`. [migrations/000008_domain_events_outbox_seq.up.sql](../../../migrations/000008_domain_events_outbox_seq.up.sql) later adds `outbox_seq BIGSERIAL`.
- **Data flow**: `inventory` inserts rows → relay reads/updates `published_at` → Phase 7 projector reads and writes `processed_events`.
- **How it is tested**: indirectly by every integration test that inserts events. No migration-specific test.
- **Read next**: [Phase 6 FILES](../phase-6-payment-saga/FILES.md).

> Note: `event_seats_expiry_idx` (the reaper's scan index) is created in [migrations/000002_event_seats.up.sql](../../../migrations/000002_event_seats.up.sql), not in 0005.
