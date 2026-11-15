# ControlPlane walkthrough — file map and "show me the line"

Use this on Day 4 and before the demo. The goal is not to memorize line numbers; it is to know **which
file owns which invariant** and to navigate there in under 10 seconds when asked.

Repo: `C:\PROJECT\aashishGithub\Avid\ControlPlane` (GitHub: `aashishGitHub/ControlPlane`).

---

## 1. The 30-second tour

**Description.** The tour is a package map, not a file list: HTTP, relay, and consumer live in one control-plane process; the worker is a separate stateless process with no database access; domain holds pure rules; store holds SQL and constraints; messaging owns AMQP. The sentence to say is "Postgres is the source of truth and the arbiter; RabbitMQ is the transport."

```text
cmd/controlplane ── HTTP API + outbox relay + update consumer (one process)
cmd/worker       ── simulator: consumes tasks, publishes progress (no DB)
internal/domain  ── pure rules: states, ranks, slug, errors, timestamps
internal/httpapi ── routing, decoding, validation, status/error mapping
internal/service ── use cases; transaction boundaries; disambiguation
internal/store   ── SQL; takes a DB interface (pool or tx)
internal/outbox  ── claim unpublished tasks, publish, mark
internal/messaging ── AMQP topology, publisher confirms, consumer, reconnect
internal/worker  ── worker simulator logic
internal/testutil ── dedicated test DB + cross-package advisory lock
scripts/         ── race demo and fault injection
docs/            ── PRD, DESIGN, M3/M4 Q&A
```

Say: *"The API commits intent, the outbox relay publishes it, the worker simulates the provider, and the
consumer applies progress idempotently. Postgres is the source of truth and the arbiter; RabbitMQ is the
transport."*

---

## 2. File-by-file map

**Description.** This map answers "which file owns which invariant?" rather than "what does every file do?" For each package, know the boundary it protects and the function an interviewer would ask you to open. The entrypoints show how one process wires HTTP, relay, and consumer, while the worker stays deliberately database-free.

### Entrypoints

| File | What it does | What to say |
|---|---|---|
| `cmd/controlplane/main.go` | Loads env, opens pool, creates `ConnManager`, relay, service, starts relay + consumer goroutines, starts `http.Server`, waits for signal, calls `Shutdown` with 10s deadline | "One deployable unit: HTTP, relay, and consumer. `signal.NotifyContext` makes Docker's SIGTERM a graceful stop." |
| `cmd/worker/main.go` | Flags with env defaults (`FAIL_RATE`, delays, duplicate/reorder), builds `worker.Options`, runs the worker | "The worker has no DB; it only consumes tasks and publishes progress. The flags are the fault-injection surface." |

Actual server config in `cmd/controlplane/main.go` sets **only `ReadHeaderTimeout`**. It does not set
`ReadTimeout`, `WriteTimeout`, or `IdleTimeout` — name that as a hardening gap if asked. The recommended
shape is in [`examples/go-service-patterns.md`](go-service-patterns.md#2-http-server--timeouts-and-shutdown).

### Domain — no infrastructure imports

| File | Key functions | Invariant |
|---|---|---|
| `internal/domain/statemachine.go` | `TaskUpdateAllowed`, `IsTaskTerminal`, `ParseTaskStatus`, `ParseTaskType`, `TenantOutcome`, `CanUpdateTenant`, `CanDeleteTenant` | Tasks move forward by rank; terminal outcomes map to tenant states |
| `internal/domain/errors.go` | `ErrorCode`, `Error`, `NewError` | Stable machine-readable codes, no HTTP knowledge |
| `internal/domain/slug.go` | `ValidSlug` | 3–28 chars, lowercase, starts with a letter, no trailing hyphen |
| `internal/domain/timestamp.go` | `FormatTimestamp`, `Timestamp`, `MarshalJSON`, `UnmarshalJSON` | Fixed `2006-01-02T15:04:05.000Z` output; lenient RFC3339 input |

The rank map is the whole order-tolerance mechanism:

```go
var taskRank = map[TaskStatus]int{
	TaskAccepted:   0,
	TaskInProgress: 1,
	TaskDone:       2,
	TaskFailed:     2,
}
```

`TaskUpdateAllowed(current, next)` is `taskRank[next] > taskRank[current]`.

`TenantOutcome` returns the **expected current** tenant status (the optimistic guard) and the **next**
status:

| Task type | Expected current | Done → | Failed → |
|---|---|---|---|
| deploy | `provisioning` | `active` | `failed` |
| update | `updating` | `active` | `failed` |
| destroy | `destroying` | `destroyed` | `failed` |

### HTTP API

| File | Key functions | Notes |
|---|---|---|
| `internal/httpapi/router.go` | `New`, `BrokerHealth`, `handleHealthz` | chi routes; `/healthz` pings Postgres and RabbitMQ |
| `internal/httpapi/tenant_handlers.go` | `handleCreateTenant`, `handleListTenants`, `handleGetTenant`, `handleUpdateTenant`, `handleDeleteTenant`, `handleListTenantTasks`, `decodeJSON` | 201 create, 202 patch/delete; validation details |
| `internal/httpapi/task_handlers.go` | `handleListTasks`, `handleGetTask` | Global task listing with keyset pagination |
| `internal/httpapi/errors.go` | `errorStatus`, `writeError`, `validationError`, `writeJSON` | Domain code → status; unknown error → 500 + log |
| `internal/httpapi/pagination.go` | `parseCursor`, `parseLimit`, `nextCursor` | Cursor = last UUID; limit default 20, max 100 |
| `internal/httpapi/wire.go` | `tenantWire`, `taskWire`, `mutationResponse`, list responses | Wire types separated from store types |

Routes:

```text
GET    /healthz
POST   /v1/tenants
GET    /v1/tenants?status=&cursor=&limit=
GET    /v1/tenants/{id}
PATCH  /v1/tenants/{id}          {name, version}
DELETE /v1/tenants/{id}          {version}
GET    /v1/tenants/{id}/tasks
GET    /v1/tasks?status=&type=&cursor=&limit=
GET    /v1/tasks/{id}
```

Status contract:

| Request | Status | Why |
|---|---|---|
| POST create | 201 + `Location` | Tenant exists synchronously |
| PATCH | 202 | Only starts async update work |
| DELETE | 202 | Only starts async destroy work |
| GET | 200 | — |
| Bad JSON / wrong type | 400 `malformed_request` | Syntactic |
| Blank name / bad slug / missing version | 422 `validation_error` + details | Business validation |
| Duplicate slug | 409 `tenant_already_exists` | Conflict |
| Stale version / wrong status | 409 `tenant_version_conflict` / `tenant_update_not_allowed` | Conflict |

**Gaps to name:** `decodeJSON` does not cap the body or reject trailing JSON; there are no dedicated
`httpapi` unit tests; DELETE takes its version in a body (less interoperable than `If-Match`).

### Service — transaction boundaries

| File | Key functions | Notes |
|---|---|---|
| `internal/service/tenant.go` | `Service`, `Option`, `WithNotifier`, `CreateTenant`, `UpdateTenant`, `DeleteTenant`, `GetTenant`, `ListTenants`, `ListTenantTasks`, `disambiguateNoMatch`, `refetchTenant`, `mustUUIDv7` | Owns the multi-statement transactions; notifies relay after commit |
| `internal/service/task.go` | `TaskUpdate`, `ApplyResult`, `ApplyTaskUpdate`, `GetTask`, `ListTasks` | Idempotent consumer transaction |

`CreateTenant`:

1. Build tenant `provisioning`, version 1, UUIDv7.
2. Build deploy task `accepted`, UUIDv7.
3. `WithTx`: insert tenant, insert task.
4. Map `23505` → `tenant_already_exists`.
5. Notify relay **after** commit.
6. Refetch for DB-assigned timestamps.

`UpdateTenant` / `DeleteTenant`:

1. Conditional UPDATE (`id + version + status`).
2. If matched, insert the task in the same transaction.
3. If zero rows, `disambiguateNoMatch`: 404 → status 409 → version 409.
4. Notify after commit.

`ApplyTaskUpdate`:

1. Insert inbox row; duplicate → return `ApplyDuplicate`.
2. Load task (tenant from the task row, not the message).
3. Lock tenant, then task.
4. Terminal or non-forward → `ApplyStale`.
5. Update task status.
6. If terminal, guarded tenant update (`WHERE status = expected`).
7. Commit; return `ApplyApplied`.

**Gap to name:** `Service` holds the concrete `*pgxpool.Pool`; handlers depend on the concrete
`*service.Service`, so HTTP unit tests would need a refactor to a small interface.

### Store — SQL and constraints

| File | Key functions | Notes |
|---|---|---|
| `internal/store/pool.go` | `DB`, `ErrNoRows`, `IsUniqueViolation`, `Open`, `WithTx` | `DB` is satisfied by pool and tx; embedded schema |
| `internal/store/schema.sql` | tables, checks, indexes | `UNIQUE(slug)`, status checks, partial open-task index, unpublished index, inbox PK |
| `internal/store/tenant.go` | `CreateTenant`, `GetTenantByID`, `GetTenantForUpdate`, `ListTenants`, `UpdateTenant`, `DeleteTenant` | Conditional UPDATE; keyset list |
| `internal/store/task.go` | `CreateTask`, `GetTaskByID`, `GetTaskForUpdate`, `ListTasksByTenant`, `ListTasks` | `published_at` starts NULL |
| `internal/store/outbox.go` | `ClaimUnpublishedTasks`, `MarkTaskPublished` | `FOR UPDATE OF t SKIP LOCKED`; join tenant data in the claim |
| `internal/store/inbox.go` | `InsertProcessedMessage`, `UpdateTaskStatus`, `UpdateTenantStatusGuarded` | Inbox `ON CONFLICT DO NOTHING`; guarded tenant outcome |

Schema invariants:

```sql
slug text NOT NULL UNIQUE CHECK (slug ~ '^[a-z][-a-z0-9]{1,26}[a-z0-9]$')
name text NOT NULL CHECK (btrim(name) <> '')
status text NOT NULL CHECK (status IN (...))
version bigint NOT NULL DEFAULT 1
```

```sql
CREATE INDEX tasks_unpublished_idx ON tasks (id) WHERE published_at IS NULL;
CREATE UNIQUE INDEX tasks_one_open_per_tenant
  ON tasks (tenant_id) WHERE status IN ('accepted','in_progress');
```

The partial unique index is a **safety net**, not the primary mechanism. The service status guard is the
primary mechanism; the index makes a second open task impossible even if a future code path forgets the
guard.

**Gap to name:** schema is applied with `CREATE ... IF NOT EXISTS` at boot; no versioned migrations, no
rollback, no safe `ALTER` story.

### Outbox relay

| File | Key functions | Notes |
|---|---|---|
| `internal/outbox/relay.go` | `Relay`, `NewRelay`, `Notify`, `Run`, `RunOnce` | Non-blocking wake + 500ms poll; batch 100; max 10 batches per wake |

`RunOnce` sequence:

```text
open channel → confirm mode →
BEGIN
  claim 100 rows with FOR UPDATE SKIP LOCKED
  for each: publish (mandatory + confirm) → mark published_at
COMMIT
```

Why one transaction: the row lock must survive the broker I/O. Why `Notify`: post-commit fast path; the
poll is the safety net if the process crashes between commit and notify. Why `SKIP LOCKED`: concurrent
relay replicas claim disjoint batches instead of blocking.

**The subtle risk to name first:** the Postgres transaction is held open across network I/O to RabbitMQ.
A slow broker means long locks and lower relay throughput. Bound the batch, set publish timeouts, monitor
oldest-unpublished age and lock waits. This is a deliberate tradeoff.

### Messaging

| File | Key functions | Notes |
|---|---|---|
| `internal/messaging/connection.go` | `ConnManager`, `NewConnManager`, `connect`, `run`, `Channel`, `Ping`, `Close` | One supervised connection; backoff + jitter; re-declares topology on reconnect |
| `internal/messaging/topology.go` | `DeclareTopology`, exchange/queue constants | Direct exchanges; quorum queues; DLX + delivery limit on updates |
| `internal/messaging/publisher.go` | `Publisher`, `NewPublisher`, `PublishTask`, `PublishUpdate`, `publish` | Confirm mode; `mandatory=true`; waits for return or ack |
| `internal/messaging/consumer.go` | `ConsumeUpdates`, `consumeOnce`, `processDelivery`, `withTransientRetry`, `isPermanent`, `invokeHandler` | Manual ack; poison vs transient; panic recovery at handler boundary |
| `internal/messaging/wire.go` | `SchemaVersion`, `TaskMsg`, `TaskUpdateMsg`, `TaskUpdate`, `NewMessageID`, `Parse`, routing keys | Deterministic message ID; closed-vocabulary parsing |
| `internal/messaging/backoff.go` | `withJitter` | Half fixed + half random |

Topology:

```text
cp.tasks (direct)
  ├── task.deploy  ─┐
  ├── task.update  ─┼──> worker.tasks (quorum, no DLQ)
  └── task.destroy ─┘

cp.task-updates (direct)
  ├── task.update.accepted    ─┐
  ├── task.update.in_progress ─┤
  ├── task.update.done        ─┼──> cp.task-updates (quorum, delivery limit 5)
  └── task.update.failed      ─┘         │
                                          └── DLX → cp.task-updates.dlq
```

Why direct: the routing vocabulary is closed; every key has an explicit binding. `mandatory=true` turns
a missing binding into a return instead of a silent drop.

### Worker

| File | Key functions | Notes |
|---|---|---|
| `internal/worker/worker.go` | `Options`, `DefaultOptions`, `Validate`, `Worker`, `New`, `Run`, `consumeOnce`, `handleDelivery`, `process`, `randomDelay`, `shouldDuplicate`, `parseTask`, `newUpdate`, `reasonFor` | Prefetch 1; ack after terminal publish; fault injection |

Worker flow:

```text
consume worker.tasks (prefetch 1)
  parse schema/id/type (invalid → Nack false)
  random delay
  publish in_progress
  publish done/failed
  optional duplicate terminal publish
  ack the task
```

Fault knobs:

| Knob | Effect |
|---|---|
| `FAIL_RATE=1` | Every deploy/update/destroy ends `failed` |
| `DUPLICATE_RATE=1` | Terminal update published twice; inbox must dedup |
| `REORDER=true` | Terminal first, then `in_progress`; rank guard must drop the late progress |
| `MIN_DELAY_MS` / `MAX_DELAY_MS` | Window for racing manual updates |

**Gaps to name:** the worker publishes `in_progress` *after* the simulated delay, not before long work;
`worker.tasks` has no DLQ/delivery limit; no per-delivery panic recovery; reconnect backoff does not reset
after a long success.

### Tests and scripts

| File | Proves |
|---|---|
| `internal/domain/*_test.go` | Slug rules, timestamp format, task/tenant transitions |
| `internal/service/concurrency_test.go` | 50 concurrent creates → one winner, 49 conflicts; 50 concurrent PATCHes → one winner, version +1 |
| `internal/service/testhelpers_test.go` | Integration pool and truncation |
| `internal/messaging/outbox_integration_test.go` | Commit publishes once, rollback publishes nothing, claim skips locked rows |
| `internal/messaging/consumer_integration_test.go` | Duplicate/stale/poison, DLQ, topology |
| `internal/messaging/lifecycle_integration_test.go` | Create → active → patch → active → delete → destroyed → recreate 409 |
| `internal/messaging/testhelpers_test.go` | Broker/test DB setup |
| `internal/testutil/db.go` | Dedicated `controlplane_test` DB + Postgres advisory lock |
| `scripts/race-demo.sh` | Live 50+50 race proof |
| `scripts/publish-update.sh` | Duplicate/stale/poison injection via RabbitMQ HTTP API |

### Config and docs

| File | Notes |
|---|---|
| `compose.yaml` | Postgres 16, RabbitMQ 3.13 management, controlplane, worker; healthchecks |
| `Dockerfile` | Multi-stage, `TARGET` build arg for controlplane/worker |
| `Makefile` | `up`, `down -v`, `logs`, `test`, `test-race`, `worker-restart`, `race-demo`; `check` incomplete |
| `docs/PRD.md` | Requirements, functional/non-functional, non-goals |
| `docs/DESIGN.md` | Schema, state machines, API, messaging, failure matrix, decisions |
| `docs/M3-RELEVANT-QNA.md` | Outbox/inbox/DLQ/reconnect drill answers |
| `docs/M4-RELEVANT-QNA.md` | Worker/fault injection/tests/compose drill answers |
| `README.md` | Quick start, API, manual verification, known limitations |

---

## 3. "Show me the line" — 18 prompts

**Description.** These prompts simulate a code-review interview: the interviewer names an invariant, and you navigate to the line that enforces it. The answer should identify the mechanism and the failure mode it prevents, not just the file path. Practice until each answer takes under ten seconds to locate and thirty seconds to explain.

For each: open the file, find the function, point at the statement. Do not scroll aimlessly.

### 1. "Where do you prevent two tenants with the same slug?"

- `internal/store/schema.sql` → `slug text NOT NULL UNIQUE`.
- `internal/store/tenant.go` → `CreateTenant` does a plain INSERT.
- `internal/service/tenant.go` → `CreateTenant` maps `store.IsUniqueViolation(err)` to
  `ErrTenantAlreadyExists`.

Say: *"There is no pre-check SELECT. The unique index is evaluated atomically at INSERT; the loser gets
23505. That works across replicas, unlike an in-process lock."*

### 2. "Where do you prevent a lost update on PATCH?"

- `internal/store/tenant.go` → `UpdateTenant`: one `UPDATE ... WHERE id=$1 AND version=$3 AND
  status='active' ... version = version + 1 RETURNING ...`.
- `internal/service/tenant.go` → `UpdateTenant` and `disambiguateNoMatch`.

Say: *"One statement checks the version and status atomically. Zero rows means one of three causes;
the service re-reads to disambiguate 404, wrong status, or stale version."*

### 3. "Where do you ensure at most one open task per tenant?"

- `internal/store/schema.sql` → `tasks_one_open_per_tenant` partial unique index.
- `internal/service/tenant.go` → the status guards (`CanUpdateTenant`, `CanDeleteTenant`) are the primary
  mechanism.

Say: *"The service guard is the primary mechanism; the partial index is the safety net. It only covers
accepted/in_progress, so history rows do not conflict."*

### 4. "Where does a task become an event?"

- `internal/store/task.go` → `CreateTask` inserts with `published_at = NULL`.
- `internal/store/outbox.go` → `ClaimUnpublishedTasks` selects `WHERE published_at IS NULL`.
- `internal/outbox/relay.go` → `RunOnce` publishes and marks.

Say: *"The task row is the outbox. `status` is the business lifecycle; `published_at` is the delivery
lifecycle. They are separate on purpose."*

### 5. "Where is the row lock held across publish?"

- `internal/outbox/relay.go` → `RunOnce`: `store.WithTx` wraps claim → publish → mark.
- `internal/store/outbox.go` → comments on `ClaimUnpublishedTasks` and `MarkTaskPublished`.

Say: *"Postgres locks are transaction-scoped. If I commit after the claim, another relay can claim the
same row. The transaction stays open through the broker confirm."*

### 6. "Where do you wait for the broker to accept the message?"

- `internal/messaging/publisher.go` → `NewPublisher` calls `ch.Confirm(false)`, `NotifyPublish`,
  `NotifyReturn`; `publish` calls `PublishWithContext(..., mandatory=true, ...)` and selects on
  `returns`/`confirms`.

Say: *"A nil from Publish only means the bytes hit the socket. The confirm means the broker accepted
responsibility; the return catches unroutable messages."*

### 7. "Where do you deduplicate a redelivered update?"

- `internal/store/inbox.go` → `InsertProcessedMessage`: `INSERT ... ON CONFLICT (message_id) DO NOTHING`.
- `internal/service/task.go` → `ApplyTaskUpdate` checks `inserted` before any state work.

Say: *"The inbox row and the state change commit together. Insert-first serializes concurrent
duplicates on the primary key."*

### 8. "Where do you reject an out-of-order update?"

- `internal/domain/statemachine.go` → `taskRank` and `TaskUpdateAllowed`.
- `internal/service/task.go` → `if domain.IsTaskTerminal(locked.Status) ||
  !domain.TaskUpdateAllowed(...) { result = ApplyStale; return nil }`.

Say: *"The rank guard is monotonic. A late in_progress after done is rank 1 > 2, false. A duplicate
terminal is 2 > 2, false. accepted → done is 2 > 0, true, so a lost in_progress does not block the
terminal outcome."*

### 9. "Where does a terminal task update the tenant?"

- `internal/domain/statemachine.go` → `TenantOutcome`.
- `internal/service/task.go` → after `UpdateTaskStatus`, if terminal, call
  `UpdateTenantStatusGuarded`.
- `internal/store/inbox.go` → `UPDATE tenants ... WHERE id=$1 AND status=$2`.

Say: *"The tenant update is guarded on the expected in-flight status. If the tenant moved on, zero rows
match and the older outcome is dropped instead of corrupting newer state."*

### 10. "Where do you avoid deadlocks?"

- `internal/store/tenant.go` → `GetTenantForUpdate` comment.
- `internal/store/task.go` → `GetTaskForUpdate` comment.
- `internal/service/task.go` → tenant lock before task lock.

Say: *"Both the API and consumer touch tenant before task. Consistent lock order removes the ABBA
cycle. Transactions are short and bounded."*

### 11. "Where do you classify a poison message?"

- `internal/messaging/consumer.go` → `processDelivery` and `isPermanent`.
- `internal/messaging/wire.go` → `TaskUpdateMsg.Parse` rejects schema/ID/status.

Say: *"Malformed JSON, unknown schema, unknown status, missing task/tenant, and recovered panics are
permanent. Connection/timeouts are transient. Poison goes Nack(requeue=false); transient gets bounded
in-process retries then requeue."*

### 12. "Where is the DLQ configured?"

- `internal/messaging/topology.go` → `updatesArgs`:
  `x-queue-type=quorum`, `x-delivery-limit=5`, `x-dead-letter-exchange=""`,
  `x-dead-letter-routing-key=cp.task-updates.dlq`.
- The DLQ queue itself is declared durable.

Say: *"The default exchange with the DLQ name as routing key is how a queue dead-letters to a queue.
`worker.tasks` has no DLQ or delivery limit in this build — that is a named gap."*

### 13. "Where do you reconnect after the broker drops?"

- `internal/messaging/connection.go` → `ConnManager.run`: waits on `NotifyClose`, sets `conn=nil`,
  reconnects with capped exponential backoff + jitter, re-declares topology in `connect`.
- `internal/messaging/consumer.go` → `ConsumeUpdates` also restarts `consumeOnce` with backoff.
- `internal/worker/worker.go` → `Run` restarts `consumeOnce` with backoff.

Say: *"The manager owns one connection; channels are not cached because they die with it. Topology is
re-declared before the connection is visible, so no publisher races the bindings."*

### 14. "Where is the transaction boundary?"

- `internal/store/pool.go` → `WithTx`: begin, fn, commit/rollback.
- `internal/service/tenant.go` → create/update/delete wrap tenant + task in one `WithTx`.
- `internal/service/task.go` → inbox + task + tenant outcome in one `WithTx`.

Say: *"Store functions take a DB and never decide the boundary. The service composes the use case; one
transaction protects the invariant."*

### 15. "Where is the worker kept untrusted?"

- `internal/service/task.go` → `TaskUpdate` deliberately omits `tenant_id`; comment says the consumer
  looks it up from the task row.
- `internal/store/task.go` → `GetTaskByID` / `GetTaskForUpdate` return the authoritative `TenantID`.
- `cmd/worker/main.go` → no `DATABASE_URL`, no pool.

Say: *"The worker can only publish progress. It cannot write tenant state, and the consumer ignores the
message's tenant claim. That limits blast radius."*

### 16. "Where is test isolation?"

- `internal/testutil/db.go` → `OpenDB`: dedicated `controlplane_test` database, `ensureDatabase`,
  `acquireLock` with `pg_advisory_lock`.
- `internal/messaging/testhelpers_test.go` → separate vhost for broker tests.
- `internal/service/testhelpers_test.go` → integration pool.

Say: *"The running compose relay polls the dev database. If tests share it, the relay can claim a test
outbox row. Separate DB and vhost, plus an advisory lock, fixed the flake."*

### 17. "Where does /healthz check dependencies?"

- `internal/httpapi/router.go` → `handleHealthz` pings `pool` then `broker.Ping`.
- `internal/messaging/connection.go` → `Ping` opens/closes a channel.

Say: *"It reports Postgres or RabbitMQ unavailable with 503. That is a readiness signal, not a liveness
probe: a broker outage should not restart the API."*

### 18. "Where is the race proven?"

- `internal/service/concurrency_test.go` → `TestCreateTenantSlugRace` and
  `TestUpdateTenantVersionRace`.
- `scripts/race-demo.sh` → live 50 POSTs + 50 PATCHes.
- `internal/messaging/lifecycle_integration_test.go` → end-to-end lifecycle.

Say: *"The tests use real Postgres because the guarantee is a database property. The live script proves
the same invariant through HTTP."*

---

## 4. Five-minute code tour (when they say "show me the code")

**Description.** The five-minute tour follows one event through the system instead of opening every file: schema constraints, tenant/task insert, conditional update, outbox claim/publish/mark, publisher confirms, idempotent consumer, ack policy, and the race test. Each stop is one invariant and one sentence. Resist explaining the router.

Do not open every file. Follow the event:

1. **`internal/store/schema.sql`** — point at `UNIQUE(slug)`, the status checks, the partial open-task
   index, `published_at`, and the inbox PK. (45s)
2. **`internal/service/tenant.go` `CreateTenant`** — show the tenant + task insert in one `WithTx`, the
   23505 mapping, notify-after-commit. (60s)
3. **`internal/store/tenant.go` `UpdateTenant`** — show the conditional UPDATE and `RETURNING`. (45s)
4. **`internal/outbox/relay.go` `RunOnce`** — show claim → publish → mark inside one transaction. (60s)
5. **`internal/messaging/publisher.go` `publish`** — show confirm + mandatory + return. (45s)
6. **`internal/service/task.go` `ApplyTaskUpdate`** — show inbox-first, tenant/task locks, rank guard,
   terminal outcome. (60s)
7. **`internal/messaging/consumer.go` `processDelivery`** — show ack-after-handler, poison vs transient.
   (45s)
8. **`internal/service/concurrency_test.go`** — show the 50-goroutine race and the final version
   assertion. (30s)

That is ~5 minutes and covers every invariant. Resist the urge to explain the router.

---

## 5. Ten-minute deep tour

**Description.** The deep tour adds the state machine, outbox locking join, AMQP topology, reconnect semantics, worker failure knobs, and test isolation. Use it only when the interviewer wants depth; each item should still be one mechanism, one failure mode, and one tradeoff. The known-limitations list is part of the tour, not an afterthought.

Add:

- `internal/domain/statemachine.go` — walk the rank table and `TenantOutcome` mappings.
- `internal/store/outbox.go` — `FOR UPDATE OF t SKIP LOCKED` and the join.
- `internal/messaging/topology.go` — exchanges, bindings, quorum, DLX, delivery limit.
- `internal/messaging/connection.go` — `NotifyClose` closure semantics and topology re-declare.
- `internal/worker/worker.go` — prefetch 1, ack-after-terminal-publish, reorder/duplicate knobs.
- `internal/testutil/db.go` — advisory lock and the flake story.
- `README.md` known limitations — name them before they ask.

---

## 6. Navigation commands

From the repo root:

```bash
# Find the invariant, not a line number.
rg -n "SKIP LOCKED|FOR UPDATE|published_at" internal
rg -n "23505|IsUniqueViolation|version = version" internal
rg -n "ON CONFLICT|message_id|taskRank|TenantOutcome" internal
rg -n "Nack|Ack|isPermanent|delivery-limit" internal
rg -n "WithTx|Begin\(" internal

# Show a function's doc and signature.
go doc ./internal/service Service.ApplyTaskUpdate

# Run one proof.
go test ./internal/service -run TestCreateTenantSlugRace -count=1 -v
go test ./internal/messaging -run TestFullLifecycleInProcess -count=1 -v
```

VS Code: `Ctrl+P` for file name, `Ctrl+Shift+F` for content, `Ctrl+Shift+O` for symbols in a file.

---

## 7. The blank-file test

**Description.** The blank-file test checks whether you know the package boundaries without the editor: for each package, write one sentence and its key functions from memory. If you cannot, reopen only that package. This is a retrieval test, not a reading exercise, and it exposes packages you recognize but cannot explain.

Close the repo. For each package, write one sentence and the key function names. If you cannot, reopen
only that package.

| Package | One sentence | Key names |
|---|---|---|
| `domain` | Pure state rules and wire value types | `TaskUpdateAllowed`, `TenantOutcome`, `ValidSlug`, `Timestamp` |
| `httpapi` | Decode, validate, map codes to status | `New`, `handleCreateTenant`, `writeError`, `nextCursor` |
| `service` | Own transactions and disambiguate outcomes | `CreateTenant`, `UpdateTenant`, `ApplyTaskUpdate` |
| `store` | SQL over a `DB`; constraints are the arbiters | `WithTx`, `UpdateTenant`, `ClaimUnpublishedTasks`, `InsertProcessedMessage` |
| `outbox` | Claim, publish, mark in one transaction | `RunOnce`, `Notify` |
| `messaging` | Topology, confirms, ack policy, reconnect | `DeclareTopology`, `PublishTask`, `ConsumeUpdates`, `ConnManager` |
| `worker` | Simulate provider work and publish progress | `Run`, `handleDelivery`, `process` |
| `testutil` | Isolate integration tests | `OpenDB`, `TruncateAll`, `acquireLock` |

---

## 8. What not to claim

**Description.** These are the claims that turn a strong demo into a credibility problem: production readiness, exactly-once delivery, an authoritative worker, a free outbox, general-purpose `SKIP LOCKED`, Go preventing database races, liveness versus readiness, real migrations, and observability you do not have. Name the gap before the interviewer finds it.

- "Production-ready." It is a take-home with deliberate gaps.
- "Exactly-once delivery." It is at-least-once + idempotent apply + order tolerance.
- "The worker is authoritative." It has no DB; the consumer is.
- "The outbox is free." It costs relay latency, duplicate delivery, and retention work.
- "`SKIP LOCKED` is a general read pattern." It is a queue claim.
- "Go prevents the slug/version race." Postgres does; Go just sends the statements.
- "`/healthz` is a liveness probe." It checks dependencies; that is readiness.
- "We have migrations." `CREATE IF NOT EXISTS` at boot is not a migration system.
- "We have metrics/tracing." JSON logs + RabbitMQ UI only.
- "The worker queue has a DLQ." It does not; `cp.task-updates` does.

---

## 9. End-of-day checkpoint

- [ ] Draw the package map from memory.
- [ ] Name the file that owns each of the 10 invariants.
- [ ] Run the five-minute code tour once with a timer.
- [ ] Answer 10 "show me the line" prompts without searching.
- [ ] Name all known limitations in under 45 seconds.
- [ ] Run one targeted test and explain what it proves.
