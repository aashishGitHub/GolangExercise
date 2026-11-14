# Cheatsheets — the final 48 hours

Read one page at a time, out loud. Do not highlight. If a line does not trigger the mechanism, open the
day file for that section.

---

## 1. Go core — one page

### Types and zero values

- Every type has a zero value: `0`, `false`, `""`, `nil`, zeroed struct/array.
- Design for a useful zero value: `sync.Mutex`, `bytes.Buffer`, config with defaults.
- nil slice works with `append`/`range`; JSON `null` vs `[]`. Use `[]T{}` when the contract says array.
- Slice = `{ptr, len, cap}`. `append` can mutate the caller's backing array. Always use the returned
  slice; copy for isolation.
- Map: read nil is safe; write nil panics; concurrent write is a data race.
- String is immutable bytes (UTF-8). `s[i]` is a byte; `for range` yields rune + byte index; `len` is
  bytes. Use `strings.Builder` in a loop.
- Value receiver for small immutable values; pointer receiver for mutation/large structs/nil semantics.
  Be consistent per type.
- Embedding is composition, not inheritance. Promoted methods do not create a subtype relationship.

### Interfaces and errors

- Implicit implementation. Small interfaces at the consumer; return concrete types from constructors.
- Typed nil in an interface is non-nil: `(type, value)` both matter. Return untyped `nil`.
- Type assertion with comma-ok; type switch for multiple; `errors.As` for typed errors (it unwraps).
- `errors.Is` for identity/sentinels; `errors.As` for fields.
- `%w` preserves the chain; `%v` discards it. Wrap at boundaries, not five times.
- `panic` for programmer errors/impossible states, not expected I/O or validation.
- Recover only at process boundaries (HTTP, message handler); a panic in an unrecovered goroutine kills
  the process.
- Deferred functions can change named returns; useful for panic-to-error, otherwise surprising.

### Generics, JSON, tooling

- Generics when the algorithm is identical across types; not when an interface expresses behavior better
  or there is one concrete type.
- `omitempty` drops `0`, `false`, `""`; use a pointer/custom type when presence matters.
- `time.Time` JSON is RFC3339Nano with variable precision; use a custom wire type for a fixed format.
- Functional options keep constructors readable and extensible.
- `internal/` prevents outside imports; `//go:embed` ships files in the binary.
- `gofmt` formats; `go vet` catches suspicious constructs; `-race` detects executed data races.
- Table tests: named cases, `t.Run`, `t.Cleanup`, no shared mutable state, behavior not implementation.

### Five sentences that sound senior

1. "The zero value should be usable; a constructor should be optional, not mandatory."
2. "I return concrete types and accept interfaces, so the consumer owns the abstraction."
3. "I wrap with `%w` at boundaries so `errors.Is`/`As` still work."
4. "A typed nil is not a nil interface; that is a classic production bug."
5. "`-race` is evidence, not proof; it only sees accesses that execute."

---

## 2. Concurrency — one page

### Model

- G = goroutine, M = OS thread, P = logical processor. An M needs a P to run Go code; blocking syscalls
  can hand off the P; idle Ps steal work. `GOMAXPROCS` controls Ps.
- Goroutines start small (~2 KB) and grow; threads have larger fixed stacks.
- In containers on Go 1.25+, `GOMAXPROCS` follows the cgroup CPU bandwidth limit on Linux. Older versions
  may see host CPUs; set it from the cgroup limit or use `automaxprocs`.
- Happens-before: channel send/receive, mutex unlock/lock, `Once`, atomics. Without an edge, concurrent
  access with a write is a data race (undefined behavior).
- Data race ≠ race condition. A logical TOCTOU can exist with perfect locking.

### Channels and select

- Unbuffered = rendezvous; buffered = decouple up to capacity. Buffering changes timing, not the need for
  a receiver or stop path.
- Sender closes. Send on closed panics; receive returns zero, `ok=false`; close nil/closed panics.
- Multiple senders: `WaitGroup` + one closer goroutine.
- nil channel blocks forever → disables a select case.
- Ready select cases are chosen uniformly at random; no priority.
- Directional channels document intent: `<-chan T`, `chan<- T`.

### Context and sync

- `context` for cancellation, deadlines, request-scoped values. First param, named `ctx`, not stored.
- Always `defer cancel()` for derived contexts; it releases the timer/parent reference.
- Values only for request metadata with an unexported key; not for dependencies.
- Mutex for in-place state; channel for handoff/signaling. "Channels for handoff, locks for state."
- `RWMutex` for read-mostly; not automatically faster. Measure.
- Atomics for one word/flag/snapshot; two related fields need a mutex.
- `WaitGroup.Add` before `go`, never inside the goroutine.
- `Once` runs once and blocks others until done; `Pool` reuses temporary objects, not a cache.

### Patterns

- Bounded pool: W workers, producer owns/close channel, every send selects on `ctx`.
- `errgroup.WithContext`: first error cancels siblings; goroutines must observe `ctx`.
- Fan-in loses ordering. Pipeline: each stage owns/closes its output; cancel on both input and send.
- Semaphore: buffered channel or `semaphore.Weighted`; `SetLimit` bounds goroutines, not resources.
- Retry: classify transient, bound attempts, backoff + jitter, respect context.
- Graceful shutdown: signal → fail readiness → `Shutdown` deadline → cancel workers → close deps →
  second signal hard-exits.

### Leaks and race tests

Leak patterns:

1. Blocked send/receive with no receiver.
2. Missing `cancel`.
3. `time.After` in a long loop (use ticker/reused timer).
4. Ticker not stopped.
5. Unbounded goroutine per request.
6. `WaitGroup.Add` inside the goroutine.
7. `for range ch` with no sender close.
8. Fire-and-forget goroutine using `r.Context()` after the response.

Race test:

```bash
go test -race ./... -run TestConcurrent -count=100
```

Use `WaitGroup`/channels for synchronization, not `time.Sleep`. Assert the invariant (exactly one winner,
exactly N increments). `-race` proves only what executed.

### ControlPlane tie-in

- The slug/version race is across processes; a Go mutex is invisible to another replica. Postgres is the
  arbiter.
- The consumer processes sequentially to bound unacked deliveries and keep classification simple.
- The relay's transaction spans broker I/O deliberately; that is a lock-duration tradeoff.
- The worker has no DB; it is untrusted.

---

## 3. HTTP and API — one page

### Status codes

| Code | Meaning | ControlPlane |
|---|---|---|
| 200 | OK | GET |
| 201 | Created; resource exists now; `Location` | POST create |
| 202 | Accepted; async work started | PATCH, DELETE |
| 400 | Malformed syntax/type/empty body | `malformed_request` |
| 401/403 | Unauthenticated / not allowed | Not implemented |
| 404 | Missing resource/route | `tenant_not_found`, `task_not_found` |
| 409 | Valid request conflicts with state | duplicate slug, stale version, wrong status |
| 413 | Body too large | Hardening gap |
| 422 | Well-formed but business-invalid | blank name, bad slug, missing version |
| 429 | Rate limited; `Retry-After` | Not implemented |
| 500 | Unexpected server error | `internal_error`, logged |
| 503 | Dependency unavailable/shutting down | `/healthz` when DB/broker down |

### Server

- `ReadHeaderTimeout` (slowloris), `ReadTimeout`, `WriteTimeout` (0/long for SSE), `IdleTimeout`,
  `MaxHeaderBytes`.
- Socket timeout ≠ query timeout. Pass `r.Context()`; set statement timeout/per-query deadline.
- Middleware order: request ID → recoverer → logger → real IP (trusted proxy only) → body limit → auth →
  rate limit → timeout.
- JSON: `MaxBytesReader`, `Decoder`, reject trailing data, validate. Map syntax/type to 400, business to
  422. `DisallowUnknownFields` is a compatibility decision.
- Error envelope: stable `code`, human `message`, optional `details`, `request_id`. Never leak SQL/driver
  text.
- Pagination: keyset `id > cursor ORDER BY id LIMIT n`; opaque cursor; `next_cursor` only on a full page.
  Index matching filter/sort.
- Idempotency key for unsafe retries: store key + response; same key + same payload → stored response;
  different payload → conflict. Complements optimistic concurrency.
- 201 vs 202: 201 means it exists; 202 means "accepted, not finished". Show current state in the body.
- Liveness = process alive (shallow); readiness = can serve (dependencies). Do not restart every pod for
  a DB outage.

### Five sentences

1. "201 because the row exists; 202 because only async work started."
2. "The body carries the version token; a stale write gets 409, not a lost update."
3. "Keyset pagination is stable under writes; OFFSET drifts and scans."
4. "The error code is the contract; the message is for humans."
5. "A timeout middleware cancels the context; the handler and query must observe it."

---

## 4. Postgres — one page

### ACID and isolation

- Atomicity (all-or-nothing), Consistency (constraints/invariants), Isolation (no invalid intermediate
  states), Durability (survives crash).
- Postgres: Read Committed (default, snapshot per statement), Repeatable Read (per transaction),
  Serializable (SSI, can abort `40001`).
- Read Committed concurrent UPDATE: the second blocks on the row lock, then re-evaluates its `WHERE`
  against the updated row (EvalPlanQual). That is why a conditional UPDATE is safe.
- Lost update: read, compute, write; the second overwrites the first. Prevent with a version token,
  row lock, or atomic increment.
- Write skew: two transactions read a condition and write in a way that invalidates it together.
  Serializable or a materialized constraint/row lock prevents it.
- Deadlock: consistent lock order, short transactions, retry `40P01` (whole transaction).

### The four mechanisms

1. **Unique index** — atomic at INSERT; loser gets `23505`. No pre-check SELECT (TOCTOU). Map by
   SQLSTATE, not message.
2. **Conditional UPDATE** — one statement checks `id + version + status`, increments version, returns
   the row. Zero rows → re-read to disambiguate.
3. **Row lock** — `SELECT ... FOR UPDATE`; transaction-scoped. Use when read-decide-write spans multiple
   statements. Keep it short; lock in a consistent order.
4. **`SKIP LOCKED`** — claim disjoint batches for a queue. Not for reads that must see every row.

### Indexes and queries

- Keyset: `WHERE id > $1 ORDER BY id LIMIT $2`; PK index. With status: `(status, id)`.
- Partial index: `UNIQUE (tenant_id) WHERE status IN ('accepted','in_progress')` enforces one open task.
- Partial unpublished index: `ON tasks (id) WHERE published_at IS NULL`.
- `EXPLAIN (ANALYZE, BUFFERS)`; look for index scan vs seq scan + sort. Tiny tables may seq scan.
- N+1: one parent query + N child queries → join/`IN`/batch.
- Pool: `max_connections` shared by all replicas; Little's Law is a starting point; more connections ≠
  more throughput. PgBouncer when connection count is the bottleneck.
- `timestamptz` stores an absolute instant; use it. Custom JSON format for the wire.
- UUIDv7 = time-ordered, good B-tree/keyset locality. UUIDv4 = random. UUIDv5 = deterministic idempotency
  key.

### Five sentences

1. "The unique index is the arbiter; a pre-check SELECT is a race."
2. "One conditional UPDATE, not SELECT-then-UPDATE; zero rows is a signal, not an error."
3. "Read Committed re-evaluates the WHERE after the row lock, so the loser matches zero rows."
4. "`SKIP LOCKED` is a queue claim, not a general read."
5. "Version increments on every mutation so a stale client cannot overwrite newer state."

---

## 5. Messaging and ControlPlane — one page

### Guarantees

- At-least-once: may deliver more than once, never lost (barring retention/DLQ mistakes).
- Exactly-once across Postgres + RabbitMQ is not available here. Use at-least-once + idempotent apply +
  order tolerance for an effectively-once business effect.
- Outbox: commit state + event together; relay publishes only committed rows.
- Inbox: durable idempotency ledger keyed by deterministic `message_id`.
- Rank guard: monotonic task status; different messages out of order are stale.

### The ControlPlane flow

```text
POST → service tx: tenant (provisioning) + task (accepted, published_at NULL)
     → 201
     → notify relay (fast path; 500ms poll is safety net)
relay tx: claim FOR UPDATE SKIP LOCKED → publish (mandatory + confirm) → mark published_at
worker: consume (prefetch 1) → in_progress → done/failed → ack after terminal publish
consumer tx: inbox insert-first → lock tenant → lock task → rank guard → task status
             → terminal? guarded tenant update → commit → ack
```

### Numbers to know

| Thing | Value |
|---|---|
| Relay batch | 100 |
| Relay poll | 500 ms |
| Relay max batches per wake | 10 |
| Consumer prefetch | 10 |
| Worker prefetch | 1 |
| Update delivery limit | 5 |
| Consumer transient attempts | 3 (base 100 ms) |
| Consumer reconnect backoff | 250 ms → 10 s |
| ConnManager reconnect | 500 ms → 30 s |
| HTTP `ReadHeaderTimeout` | 5 s |
| HTTP shutdown deadline | 10 s |
| Pagination default / max | 20 / 100 |
| Slug length | 3–28 |
| Race tests | 50 goroutines |
| Message ID | UUIDv5(task_id + "/" + status) |
| Row IDs | UUIDv7 |

### Queues and exchanges

- `cp.tasks` (direct) → `worker.tasks` (quorum, **no DLQ**), bindings `task.deploy`, `task.update`,
  `task.destroy`.
- `cp.task-updates` (direct) → `cp.task-updates` (quorum, delivery limit 5, DLX), bindings
  `task.update.accepted`, `.in_progress`, `.done`, `.failed`.
- DLQ: `cp.task-updates.dlq`.
- `mandatory=true` catches a missing binding; a confirm alone does not prove routing.

### Crash matrix

| Crash point | Result | Recovery |
|---|---|---|
| Before API commit | No state | Retry |
| After commit, before relay | Unpublished row | Poll/notify |
| During claim/publish | Rollback; unpublished | Republish |
| After publish, before confirm | Unpublished | Duplicate publish |
| After confirm, before mark | Unpublished | Duplicate publish; inbox dedups |
| After mark, before commit | Mark rolls back | Duplicate publish |
| Consumer before commit | Rollback | Redelivery |
| Consumer after commit, before ack | Committed | Redelivery; inbox no-op |

### Poison vs transient

| Class | Examples | Action |
|---|---|---|
| Poison | malformed JSON, unknown schema/status, task/tenant missing, panic | `Nack(requeue=false)` → DLQ |
| Transient | connection reset, timeout, pool exhaustion, deadlock | bounded retry, then requeue |
| Success/no-op | applied, duplicate, stale | ack |

### Five sentences

1. "At-least-once plus idempotent apply; not exactly-once."
2. "The task row is the outbox; `published_at` is delivery state, `status` is business state."
3. "The inbox dedups the same message; the rank guard handles a different message out of order."
4. "Ack after the transaction commits; a lost ack is a no-op replay."
5. "Poison is permanent; retrying it is an infinite hot loop."

---

## 6. ControlPlane decisions — one page

| Decision | Alternative | Why | Cost / when to switch |
|---|---|---|---|
| Task row as outbox | Separate outbox table | One durable work record; no dual-write | History and outbox share a table; archive published rows |
| Relay holds tx across publish | Claim then commit, publish outside | Row lock must survive broker I/O | Long locks under slow broker; bound batch/timeouts |
| Publisher confirms + mandatory | Fire-and-forget publish | Confirm means broker accepted; mandatory catches unroutable | Extra round trip; batch at scale |
| At-least-once + inbox | Exactly-once fantasy | Components cannot do XA | Duplicate work; retention for inbox |
| Rank guard | from→to transition table | One comparison encodes forward-only + idempotency | Allows accepted→done; document it |
| Inbox insert-first | After state change | Serializes concurrent duplicates on PK | Holds lock through transaction |
| Tenant → task lock order | Any order | Matches API path; avoids ABBA | Keep transactions short |
| Conditional UPDATE | SELECT FOR UPDATE then UPDATE | One atomic round trip; no lost update | Zero-row disambiguation re-read |
| Unique index | Pre-check SELECT | Atomic across replicas | Maps 23505; no friendly pre-validation |
| Tombstone (`destroyed`) | Delete row | Slug stays unique forever via UNIQUE | Table growth; archival |
| UUIDv7 rows | UUIDv4 | B-tree locality, keyset cursor | Not secrecy; IDs are guessable |
| UUIDv5 message IDs | Random per attempt | Redelivery/retry produces same inbox key | Identity tied to task+status |
| Async provisioning | Long HTTP request | Minutes-long work, retries, crash tolerance | Client must poll; relay latency |
| RabbitMQ | Kafka/SQS | Per-message ack/requeue/DLX map to poison model | Not a replay log; throughput tradeoff |
| Quorum queues | Classic durable | Replicated, durable, delivery limit | Resource cost |
| One open task/tenant | Concurrent lifecycle ops | Prevents state fights | Partial unique index is a safety net |

### Known gaps (name first)

- No auth/authz/TLS/RBAC; local `guest`/`guest`.
- No migration tool; `CREATE IF NOT EXISTS` only.
- No stuck-task reaper/heartbeat/provider reconciliation.
- No metrics/tracing; logs + RabbitMQ UI only.
- `worker.tasks` has no DLQ/delivery limit/panic recovery.
- No dedicated `httpapi`/`worker` unit tests.
- `make check` incomplete (`lint` placeholder; Windows `fmt-check`).
- Worker publishes `in_progress` after the delay, not before long work.
- Worker reconnect backoff does not reset after a long success.
- No idempotency key on POST; unique slug makes a retry a 409 rather than returning the original 201.
- DELETE version in body is less interoperable than `If-Match`.
- `published_at` rows retained forever; inbox has no retention.
- Relay transaction spans broker I/O; lock duration risk.
- `/healthz` checks the broker, so a broker outage marks the API unready even though writes still commit.

---

## 7. Demo — the five-minute script

| Time | Say / do |
|---|---|
| 0:00 | "A tenant provisioning control plane that stays correct when the request commits, the worker crashes, the broker redelivers, and progress arrives out of order." |
| 0:30 | Draw: Client → API → Postgres; task row → relay → RabbitMQ → worker → update queue → consumer → Postgres. Mark the outbox row. |
| 1:30 | `POST /v1/tenants`; show 201, `provisioning`, version 1, task `accepted`. "Tenant + task committed together; `published_at` is NULL." |
| 2:30 | Poll to `active`; show version 2. "The terminal outcome moved it; the version bump is the async completion signal." |
| 3:30 | Run `bash scripts/race-demo.sh`; show one 201 / 49 conflicts and one 202 / 49 rejected. "Postgres is the arbiter, not a Go mutex." |
| 4:30 | Duplicate + stale + poison; show version unchanged and DLQ +1. "Inbox, rank guard, dead-letter." |
| 5:00 | "At-least-once plus idempotent apply; the cost is relay latency and duplicate work. Biggest gaps: no reaper, no auth, no metrics, no worker DLQ." |

Commands and recovery: [`controlplane-demo.md`](controlplane-demo.md). Do not debug live for more than
60 seconds.

---

## 8. Behavioral — six one-liners

1. **Ownership/ambiguity:** "I turned a broad control-plane exercise into named invariants, state
   machines, an outbox, and a lifecycle test; I wrote the design and decisions table before the code."
2. **Hardest bug:** "The lifecycle test flaked because the compose relay polled the same Postgres as the
   test; separating the test DB/vhost and adding an advisory lock fixed the class, not the symptom."
3. **Disagreement:** "Outbox vs publish-in-request; I named the failure mode (commit succeeds, event
   lost), chose the simplest mechanism that protects the invariant, and documented the relay cost."
4. **Failure/incident:** pick a real one; own your concrete contribution, mitigation, root cause, durable
   fix, behavior change. No passive voice.
5. **Influence:** design doc/decisions table, test isolation fix, CI gate, migration playbook, mentorship
   — use the one with a real adoption metric.
6. **Deadline:** fixed brief; prioritized the correctness core, documented non-goals, named hardening
   gaps instead of pretending they did not exist.

### 90-second intro

> "I'm a senior engineer with 13 years across product engineering, currently building production-shaped
> Go services with Postgres and RabbitMQ. My most relevant recent work is a tenant provisioning control
> plane: transactional outbox, idempotent consumer, optimistic locking, and a lifecycle test against real
> Postgres and RabbitMQ. I'm looking for a role where correctness and system design matter, not just
> shipping endpoints. I can go deeper on the control plane, the concurrency model, or the database design
> — whichever is most useful."

### Why Go?

> "The service is I/O-bound and concurrency-heavy: HTTP, Postgres, RabbitMQ, reconnect loops, workers.
> Go's explicit concurrency and small deployment footprint fit. But the correctness mechanism is
> Postgres, not goroutines — I use Go to issue the right statements with bounded concurrency and stop
> cleanly."

---

## 9. If you get stuck — phrases that buy time

- "Let me start with the invariant, then the mechanism that protects it."
- "There are two failure modes here; let me separate them."
- "The simple option is X; it breaks when Y; so I chose Z and paid A."
- "I don't remember the exact API, but the mechanism is…"
- "I would verify that with `EXPLAIN`/a test rather than guess."
- "That's a gap in the current build; here's what production needs."
- "Let me draw it instead of describing it."

Never bluff a fact. Name what you know, what you would check, and the tradeoff.

---

## 10. Final 48 hours

### Wednesday night

- [ ] One-page architecture + create/update sequence from memory.
- [ ] 12 decisions with alternative + failure mode.
- [ ] 20 random Q&A at 4/5.
- [ ] Crash matrix without notes.
- [ ] Five limitations named.
- [ ] Stop by 18:00.

### Thursday morning (technical round)

- [ ] Read this file once.
- [ ] 5 random Q&A.
- [ ] Draw architecture once.
- [ ] Test mic/camera/screen share; water; notifications off.
- [ ] Stop. No new topics.

### Friday morning (demo)

- [ ] `docker compose up -d --build --wait`; `/healthz`.
- [ ] Run the 5-minute version once.
- [ ] Check RabbitMQ UI, DLQ, backup recording/screenshots.
- [ ] Do not run `docker compose down -v` before the demo.
- [ ] Stop. No refactors.

**If you only remember five things:** structure, invariant, failure mode, tradeoff, honesty.
