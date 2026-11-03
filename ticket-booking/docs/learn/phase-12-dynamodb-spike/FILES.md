# Phase 12 — DynamoDB Spike: Files

Siblings: [BASICS.md](BASICS.md) · [QUESTIONS.md](QUESTIONS.md) · [Learn index](../README.md) · Scope: [MILESTONES.md Phase 12](../../../MILESTONES.md) · Write-up: [dynamodb-comparison.md](../../dynamodb-comparison.md)

> Note: there is no `_test.go` file in `cmd/dynamo-spike`. The "race test" and "load test" are modes of the binary (`-mode=race`, `-mode=loadtest`), run by hand against `dynamodb-local`. They are not `go test` tests and not in CI. They need `dynamodb-local` running, so they do not run under `make test`.

> Note: the recorded runs in MILESTONES and the write-up do not show the `error rate:` line, which `printLoadTestReport` prints. Errors count in the latency list as well. I cannot tell from the docs whether those runs had zero errors.

> Note: the spike's `ConfirmSeats` conditions on `status = HELD AND hold_id = :holdId` and has no expiry check. The Postgres `ConfirmSeats` query in [sqlc/queries.sql](../../../sqlc/queries.sql) also requires `hold_expires_at > now()`. The comparison doc does not mention this difference.

## [cmd/dynamo-spike/main.go](../../../cmd/dynamo-spike/main.go)

- **What it does**: entry point. Parses flags, builds a DynamoDB client, ensures the table exists, dispatches to race or load test.
- **Control flow**:
  1. `main` reads flags: `-endpoint` (default `http://localhost:8000`), `-mode` (`race` or `loadtest`), `-seats` (30000), `-workers` (200), `-duration` (15s), `-contention` (`uniform` or `hot`), `-multi-seat` (1).
  2. `newClient` uses static fake credentials `local`/`local`, region `us-east-1`, and sets `BaseEndpoint` if `-endpoint` is non-empty.
  3. `ensureTable` runs.
  4. `-mode=race` calls `runRaceTest`; `-mode=loadtest` calls `runLoadTest` with `loadTestConfig`; anything else prints "unknown -mode".
- **Data flow**: flags → config struct → calls. No persistence of its own beyond the DynamoDB table.
- **How it is tested**: not tested. Verified by live runs (MILESTONES). MILESTONES runs used `-workers=50`, not the default 200.
- **Read next**: [table.go](../../../cmd/dynamo-spike/table.go).

## [cmd/dynamo-spike/table.go](../../../cmd/dynamo-spike/table.go)

- **What it does**: creates the table `ticketing-spike-event-seats` and seeds seats.
- **Control flow**:
  1. `ensureTable`: `DescribeTable`; if `ResourceNotFoundException`, `CreateTable` (partition key `event_id` string, sort key `seat_id` number, `BillingModePayPerRequest`), then `NewTableExistsWaiter` with a 30-second timeout.
  2. `seedSeats(ctx, client, eventID, n)` is called by `runLoadTest`. It does one `PutItem` per seat, sequentially, setting `status = FREE`.
- **Data flow**: item shape `{event_id: S, seat_id: N, status: S}`. Hold adds `held_by`, `hold_id`, `hold_expires_at` (epoch milliseconds, number).
- **How it is tested**: not tested. Note `seedSeats` overwrites any old state, so every load run starts all-FREE.
- **Read next**: [hold.go](../../../cmd/dynamo-spike/hold.go).

## [cmd/dynamo-spike/hold.go](../../../cmd/dynamo-spike/hold.go)

- **What it does**: the actual model from `docs/script.md`: `AcquireHoldSingle`, `AcquireHoldMulti`, `ConfirmSeats`, `anyConditionFailed`, and `ErrConflict`.
- **Control flow**:
  1. `AcquireHoldSingle`: one `UpdateItem` with `ConditionExpression` `attribute_not_exists(#status) OR #status = :free OR (#status = :heldCheck AND hold_expires_at < :now)`. `ConditionalCheckFailedException` becomes `ErrConflict`.
  2. `AcquireHoldMulti`: builds one `Update` per seat with the same condition, calls `TransactWriteItems` in a loop of up to 5 attempts. On `TransactionCanceledException`: if `anyConditionFailed` then return `ErrConflict` (no retry); else sleep `(1<<attempt)*10ms` plus random jitter and retry. Other errors return wrapped. After 5 attempts it returns an "exhausted" error.
  3. `ConfirmSeats`: `TransactWriteItems` of `SET #status = :booked REMOVE hold_id, hold_expires_at` with condition `#status = :held AND hold_id = :holdId`.
- **Data flow**: `eventID, seatIDs, holdID, heldBy, ttl` → condition expression + attribute values (`:now` and `:expiresAt` come from the client's `time.Now()`, in milliseconds) → item attributes updated. Expiry is therefore decided with the caller's clock, not the database's.
- **How it is tested**: no unit tests. `AcquireHoldSingle` is exercised by `-mode=race` and `-mode=loadtest`; `AcquireHoldMulti` by `-mode=loadtest -multi-seat=4`. `ConfirmSeats` and the retry/backoff branch: I found no caller in `cmd/dynamo-spike` that invokes `ConfirmSeats`, so it is untested here. MILESTONES' "`hold_id`-conditioned confirm" is implemented but not exercised by the race or load modes.
- **Read next**: [race.go](../../../cmd/dynamo-spike/race.go).

## [cmd/dynamo-spike/race.go](../../../cmd/dynamo-spike/race.go)

- **What it does**: `runRaceTest`, the thesis proof: 200 goroutines race for one seat, 20 rounds.
- **Control flow**:
  1. [main.go](../../../cmd/dynamo-spike/main.go) calls `runRaceTest(ctx, client, *seatCount)` (the seat count is ignored).
  2. Each round: `PutItem` resets seat 1 of event `race-test-event` to `FREE`; creates 200 goroutines blocked on `<-start`; `close(start)`; each calls `AcquireHoldSingle`; results counted with `atomic.AddInt64`.
  3. After `wg.Wait()`: `wins != 1 || conflicts != racers-1` means FAIL and `log.Fatalf`.
- **Data flow**: one seat item → 200 conditional updates → counters → log line `race round N/20: wins=1 conflicts=199 [PASS]`.
- **How it is tested**: it is the test, run by hand. It checks counts returned by the function, and does not read the item back afterwards. The Postgres equivalent, `TestAcquireHold_ExactlyOneWinnerUnderRace` in [internal/inventory/integration_test.go](../../../internal/inventory/integration_test.go) (build tag `integration`), does read the database back (`assertExactlyOneHeld`).
- **Read next**: [loadtest.go](../../../cmd/dynamo-spike/loadtest.go).

## [cmd/dynamo-spike/loadtest.go](../../../cmd/dynamo-spike/loadtest.go)

- **What it does**: `runLoadTest` runs `workers` goroutines for `duration`, each repeatedly acquiring holds, then prints percentiles and conflict rate.
- **Control flow**:
  1. `runLoadTest` calls `seedSeats` for event `loadtest-event`.
  2. `pickSeats`: for `hot`, always seats `0..multiSeat-1`; for uniform, a random start with `multiSeat` consecutive seats.
  3. Each worker loop: `AcquireHoldSingle` if `multiSeat == 1`, otherwise `AcquireHoldMulti`; times the call; sends a `journeyResult` (ok, conflict, or error) on a channel.
  4. `printLoadTestReport` sorts latencies and picks index `int(p*n)`, prints total, p50/p95/p99, conflict rate, error rate.
- **Data flow**: attempts → latency + outcome → report. Holds are never released, so the free pool depletes over a run (the write-up explains why uniform conflict rates are high).
- **How it is tested**: not tested; the harness is the measurement. Results are in the table in [dynamodb-comparison.md](../../dynamodb-comparison.md).
- **Read next**: [docker-compose.yml](../../../docker-compose.yml).

## [docker-compose.yml](../../../docker-compose.yml) (`dynamodb-local` service)

- **What it does**: defines `amazon/dynamodb-local:2.5.4` with `-jar DynamoDBLocal.jar -inMemory -sharedDb`, port 8000, profile `spike`.
- **Control flow**: `docker compose --profile spike up -d dynamodb-local` starts it. A plain `docker compose up -d` (or `make compose-up`) does not.
- **Data flow**: `-inMemory` means data is lost when the container stops.
- **How it is tested**: not applicable. It is infrastructure for the manual runs.
- **Read next**: [dynamodb-comparison.md](../../dynamodb-comparison.md), then [QUESTIONS.md](QUESTIONS.md).
