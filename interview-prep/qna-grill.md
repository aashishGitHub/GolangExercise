# Q&A grill — Go, backend, ControlPlane

**How to use:** cover the answer, answer aloud in 45–60 seconds, then open. Mark each question:

- **G** — green: concise and correct.
- **A** — amber: right idea, missing mechanism/tradeoff.
- **R** — red: could not answer or guessed.

Review only amber/red. Aim for 20 random questions per day; do not read the whole bank in one sitting.

---

## A. Go fundamentals

<details>
<summary><strong>Q1. What is a zero value, and why does it matter?</strong></summary>

Every type has a defined zero value (`0`, `false`, `""`, `nil`, zeroed struct/array). A variable is
never undefined. Design types so the zero value is useful: `sync.Mutex`, `bytes.Buffer`, and a config
struct with sane defaults need no constructor.

</details>

<details>
<summary><strong>Q2. Nil slice vs empty slice?</strong></summary>

Both have length 0 and work with `append`/`range`. A nil slice has no backing array and marshals to JSON
`null`; `[]T{}` marshals to `[]`. Use an empty slice when the API contract says array, not null.

</details>

<details>
<summary><strong>Q3. What is a slice, and why can `append` mutate the caller's data?</strong></summary>

A slice is `{pointer, len, cap}`. Copying it copies the header. If capacity is available, `append`
writes into the shared backing array, so another slice can observe the change. If capacity is exceeded,
it allocates a new array. Always use the returned slice; copy for isolation.

</details>

<details>
<summary><strong>Q4. When does a map write panic?</strong></summary>

Writing to a nil map panics. Concurrent writes (or a write concurrent with a read) are a data race and
can panic with "concurrent map writes". Reads from a nil map are safe and return the zero value.

</details>

<details>
<summary><strong>Q5. String vs byte vs rune?</strong></summary>

A string is an immutable byte sequence, usually UTF-8. `s[i]` is a byte; `for range` yields rune and byte
index. `len(s)` is bytes, not characters. `[]byte(s)` and `string(b)` copy; use `strings.Builder` for
concatenation in a loop.

</details>

<details>
<summary><strong>Q6. Value vs pointer receiver — how do you choose?</strong></summary>

Use a pointer receiver when the method mutates, the struct is large, or nil is meaningful. Use a value
receiver for small immutable values. `T` has value-receiver methods; `*T` has both. If one method needs a
pointer, be consistent for that type.

</details>

<details>
<summary><strong>Q7. How do interfaces work in Go?</strong></summary>

Implementation is implicit: a type implements an interface by having the methods. Define small
interfaces at the consumer, return concrete types from constructors, and accept interfaces at
boundaries. Do not create an interface only to mock it.

</details>

<details>
<summary><strong>Q8. Why is a typed nil pointer not equal to a nil interface?</strong></summary>

An interface holds `(type, value)`. Assigning a nil `*MyError` to `error` stores the type `*MyError`, so
the interface is non-nil even though the pointer is nil. Return an untyped `nil` explicitly.

</details>

<details>
<summary><strong>Q9. Type assertion vs type switch?</strong></summary>

A type assertion `v.(T)` extracts one type; use the comma-ok form to avoid a panic. A type switch
`switch x := v.(type)` handles multiple types cleanly. For errors, use `errors.As` rather than a type
assertion because it unwraps.

</details>

<details>
<summary><strong>Q10. `errors.Is` vs `errors.As`?</strong></summary>

`errors.Is` walks the wrap chain comparing values/sentinels. `errors.As` walks the chain looking for a
type and assigns it to a target. Use `Is` for identity, `As` when you need fields from a typed error.

</details>

<details>
<summary><strong>Q11. Why wrap with `%w` instead of `%v`?</strong></summary>

`%w` preserves the original error in the chain, so `errors.Is`/`errors.As` still work. `%v` keeps only
the message and discards identity. Wrap at boundaries; do not add five layers with no new information.

</details>

<details>
<summary><strong>Q12. When is `panic` acceptable?</strong></summary>

For programmer errors/impossible states or init failures, not expected I/O or validation failures.
Recover at process boundaries (HTTP middleware, message handler) so one bad input does not kill the
service. A panic in an unrecovered goroutine kills the whole process.

</details>

<details>
<summary><strong>Q13. Why can a deferred function change a named return value?</strong></summary>

Named results are variables in the function scope. A deferred closure runs after the return values are
assigned but before the function returns, so it can modify them. Useful for panic-to-error recovery;
otherwise surprising.

</details>

<details>
<summary><strong>Q14. When should you not use generics?</strong></summary>

When an interface expresses the behavior more clearly, there is only one concrete type, or the type
parameters add no real duplication reduction. Go favors simple concrete code; generics are a tool, not a
style.

</details>

<details>
<summary><strong>Q15. Embedding vs inheritance?</strong></summary>

Embedding promotes fields/methods (composition), not subtype polymorphism. The outer type does not
become a `Base` for interface purposes; it just has the promoted methods. Prefer a named field when the
relationship should be explicit.

</details>

<details>
<summary><strong>Q16. Why functional options?</strong></summary>

They keep constructors readable and extensible: `New(pool, WithNotifier(n))` instead of a long argument
list or a config struct with many zero-value ambiguities. They are useful when options are optional and
the set may grow.

</details>

<details>
<summary><strong>Q17. JSON tags and `omitempty` — what is the trap?</strong></summary>

`omitempty` omits zero values, so `0`, `false`, and `""` disappear; you cannot distinguish "absent" from
"explicitly zero". Use a pointer or a custom type when presence matters. `time.Time` marshals as
RFC3339Nano with variable fractional digits; use a custom type for a fixed wire format.

</details>

<details>
<summary><strong>Q18. What makes a good table-driven test?</strong></summary>

Cases with names, `t.Run` subtests, no shared mutable state, and assertions on behavior not
implementation. Use `t.Cleanup` for teardown, `httptest` for HTTP, and real dependencies when the
behavior is in the DB/broker.

</details>

<details>
<summary><strong>Q19. What do `go vet`, `gofmt`, and `go test -race` each catch?</strong></summary>

`gofmt` formats; `go vet` catches suspicious constructs (printf args, lock copying, lost cancel);
`-race` detects unsynchronized memory accesses that occur during the test. `-race` is evidence, not a
proof of absence.

</details>

<details>
<summary><strong>Q20. Why `internal/` and `//go:embed`?</strong></summary>

`internal/` prevents imports from outside the module, keeping implementation packages private.
`//go:embed` ships a file (e.g. `schema.sql`) inside the binary, so one boot path applies the schema
without a separate migration step (a demo tradeoff; production should use versioned migrations).

</details>

---

## B. Concurrency

<details>
<summary><strong>Q21. Goroutine vs OS thread?</strong></summary>

Goroutines are cheap, growable-stack units scheduled by the Go runtime onto OS threads (M:N). Threads
are OS-scheduled and expensive. Goroutines start around 2 KB and grow; a thread has a larger fixed stack.

</details>

<details>
<summary><strong>Q22. What is G-M-P?</strong></summary>

G is a goroutine, M an OS thread, P a logical processor with a run queue. An M must hold a P to run Go
code; blocking syscalls can hand off the P; idle Ps steal work. `GOMAXPROCS` controls Ps.

</details>

<details>
<summary><strong>Q23. Why does `GOMAXPROCS` matter in containers?</strong></summary>

On Linux, Go 1.25+ reads the cgroup CPU bandwidth limit for `GOMAXPROCS` by default and updates it when
the limit changes, so it no longer sizes itself to host CPUs. Older versions use the CPUs visible to the
process, which may be the host; there, set it from the cgroup limit or use `automaxprocs`. A manual
`GOMAXPROCS` setting disables the automatic behavior.

</details>

<details>
<summary><strong>Q24. Unbuffered vs buffered channel?</strong></summary>

Unbuffered is a rendezvous: send blocks until a receiver is ready. Buffered decouples up to capacity;
send blocks only when full. Buffering changes timing, not the need for a receiver or a stop path.

</details>

<details>
<summary><strong>Q25. Who closes a channel, and what happens after close?</strong></summary>

The sender closes. Send on closed panics; receive returns zero, `ok=false`; close of a closed/nil
channel panics. Multiple senders need a `WaitGroup` and one closer goroutine.

</details>

<details>
<summary><strong>Q26. What does a nil channel do in a select?</strong></summary>

It blocks forever, so its case is disabled. Setting a channel variable to nil is a common way to stop
selecting on an input after it closes.

</details>

<details>
<summary><strong>Q27. If two select cases are ready, which runs?</strong></summary>

One is chosen uniformly at random. There is no priority. If you need priority, nest selects or use an
explicit state machine.

</details>

<details>
<summary><strong>Q28. What is `context` for?</strong></summary>

Cancellation, deadlines, and request-scoped values across API boundaries. First parameter, named `ctx`,
never stored in a struct. Always `defer cancel()` for derived contexts. Use values only for request
metadata with an unexported key.

</details>

<details>
<summary><strong>Q29. Why `defer cancel()` if the function is about to return?</strong></summary>

`cancel` releases the timer and the parent's reference to the child context. Without it, the context and
timer can live until the deadline, leaking memory/goroutines.

</details>

<details>
<summary><strong>Q30. Mutex or channel?</strong></summary>

Mutex for guarding in-place state; channel for handing off ownership or signaling completion. "Channels
for handoff, locks for in-place state." Do not build a counter server out of channels.

</details>

<details>
<summary><strong>Q31. When is `RWMutex` useful?</strong></summary>

Read-mostly shared state where readers can proceed concurrently. It is not automatically faster; under
heavy write contention or many readers, it can be worse than a plain mutex. Measure.

</details>

<details>
<summary><strong>Q32. When use `sync/atomic`?</strong></summary>

One counter, flag, or an immutable snapshot swapped with `atomic.Pointer[T]`. Atomics protect one word;
two related fields still need a mutex. Prefer typed atomics (`atomic.Int64`) over raw functions.

</details>

<details>
<summary><strong>Q33. Why must `WaitGroup.Add` happen before `go`?</strong></summary>

`Add` and `Wait` must not race. If `Add` is inside the goroutine, `Wait` can return before the counter
increments. Call `Add` in the parent before starting the goroutine.

</details>

<details>
<summary><strong>Q34. `sync.Once` vs `sync.Pool`?</strong></summary>

`Once` runs a function exactly once and blocks other callers until it returns. `Pool` reuses temporary
objects to reduce allocation; it is not a cache and items can be dropped at GC.

</details>

<details>
<summary><strong>Q35. What is happens-before?</strong></summary>

The partial order of memory operations the Go memory model guarantees. Channel send/receive, mutex
unlock/lock, `Once`, and atomics create edges. Without an edge, concurrent access is a data race.

</details>

<details>
<summary><strong>Q36. Data race vs race condition?</strong></summary>

A data race is unsynchronized concurrent access with at least one write; it is undefined behavior. A
race condition is a timing-dependent correctness bug that can exist even with locking (e.g. a
read-modify-write split across two locked calls). `-race` finds the former, not the latter.

</details>

<details>
<summary><strong>Q37. How do you bound concurrency?</strong></summary>

Fixed worker pool, `errgroup.SetLimit`, or a buffered-channel semaphore. Never spawn one goroutine per
item without a limit. Size CPU-bound work near `GOMAXPROCS`; size I/O-bound work to what downstream can
handle.

</details>

<details>
<summary><strong>Q38. What does `errgroup.WithContext` do?</strong></summary>

Returns a group and a derived context. The first non-nil error cancels the context, and `Wait` returns
that error. It does not stop goroutines automatically; each goroutine must observe `ctx.Done()`.

</details>

<details>
<summary><strong>Q39. Fan-out vs fan-in vs pipeline?</strong></summary>

Fan-out: multiple workers read the same input. Fan-in: merge multiple outputs; ordering is lost. Pipeline:
stages connected by channels, each stage owns/closes its output. All need cancellation on both input and
output.

</details>

<details>
<summary><strong>Q40. Common goroutine leak patterns?</strong></summary>

Blocked send/receive with no receiver, missing context cancellation, `time.After` in a loop, tickers not
stopped, unbounded goroutines per request, and a producer that ignores cancellation after a worker error.
Every goroutine needs a stop path.

</details>

<details>
<summary><strong>Q41. Why is `time.After` in a loop a problem?</strong></summary>

Each call creates a timer; in a long-running loop, timers/channels accumulate until they fire. Use a
`time.Ticker` or `time.NewTimer` with `Stop`/`Reset`.

</details>

<details>
<summary><strong>Q42. What happens if a goroutine panics?</strong></summary>

If nobody recovers in that goroutine, the whole process crashes. `recover` only works in a deferred
function in the panicking goroutine. Recover at HTTP/message boundaries; do not swallow silently.

</details>

<details>
<summary><strong>Q43. How do you shut down gracefully?</strong></summary>

Signal context → fail readiness → stop accepting → `srv.Shutdown` with a deadline → cancel background
workers → close DB/broker. Let a second signal hard-exit. Do not close resources before in-flight work
finishes.

</details>

<details>
<summary><strong>Q44. Why does the ControlPlane consumer process sequentially?</strong></summary>

It wants bounded unacked deliveries and simple ordering/failure classification. The DB transaction is the
correctness boundary, not a goroutine per message. Parallel consumers need per-tenant ordering, bounded
concurrency, and more careful inbox/lock behavior.

</details>

<details>
<summary><strong>Q45. Why is Go concurrency not the fix for the slug race?</strong></summary>

The race is across processes/replicas and survives an in-process mutex. The DB unique index is the
arbiter: one INSERT commits, the other gets `23505`. Go only decides which goroutine sends the statement.

</details>

---

## C. HTTP and API design

<details>
<summary><strong>Q46. 201 vs 202?</strong></summary>

201: the resource exists synchronously at response time; include `Location`. 202: the request was
accepted, but work is asynchronous; show the current state so the client is not guessing. ControlPlane:
POST 201, PATCH/DELETE 202.

</details>

<details>
<summary><strong>Q47. 400 vs 422 vs 409?</strong></summary>

400: malformed request (invalid JSON/type/missing body). 422: well-formed but fails business validation
(blank name, bad slug). 409: valid request conflicts with current state (duplicate slug, stale version,
wrong status).

</details>

<details>
<summary><strong>Q48. Why an idempotency key?</strong></summary>

It prevents duplicate side effects when a client retries an unsafe request after a timeout. Store
key + response; same key + same payload returns the stored response; same key + different payload is a
conflict. It complements, not replaces, optimistic concurrency.

</details>

<details>
<summary><strong>Q49. PATCH vs PUT?</strong></summary>

PUT replaces the resource (or creates at a known URI) and is idempotent. PATCH applies a partial change;
not inherently idempotent, but a version-guarded patch can be. ControlPlane uses PATCH for name + version.

</details>

<details>
<summary><strong>Q50. Version vs ETag for optimistic concurrency?</strong></summary>

Both prevent lost updates. A body `version` is explicit and easy for JSON clients; `ETag` + `If-Match`
is HTTP-native and works for any representation. Pick one and be consistent. ControlPlane uses `version`.

</details>

<details>
<summary><strong>Q51. Why is a DELETE body questionable?</strong></summary>

Some proxies/clients do not forward DELETE bodies reliably. `If-Match` or a query parameter is more
interoperable. ControlPlane accepts a body version for simplicity; a public API should prefer `If-Match`.

</details>

<details>
<summary><strong>Q52. Which HTTP server timeouts matter?</strong></summary>

`ReadHeaderTimeout` (slowloris), `ReadTimeout`, `WriteTimeout` (0 for streaming), `IdleTimeout`, and
`MaxHeaderBytes`. Pass `r.Context()` and per-call deadlines to the DB/broker; a socket timeout does not
stop the handler by itself.

</details>

<details>
<summary><strong>Q53. How does graceful shutdown work?</strong></summary>

`signal.NotifyContext` for SIGINT/SIGTERM; on cancellation, `srv.Shutdown(ctx)` stops accepting and
drains in-flight until the deadline. Then cancel workers and close DB/broker. A second signal can force
exit.

</details>

<details>
<summary><strong>Q54. Why does middleware order matter?</strong></summary>

Request ID should be outer so logs/errors correlate. Recoverer should wrap handlers so a panic becomes
500. Auth/rate limit should reject before expensive body decoding. Body limit before JSON. Timeout
around business work. Order is a security/performance decision.

</details>

<details>
<summary><strong>Q55. How do you decode JSON safely?</strong></summary>

Cap the body with `http.MaxBytesReader`, use `json.Decoder`, reject trailing data, and validate fields.
`DisallowUnknownFields` is strict and can break forward compatibility; use it for internal contracts,
not necessarily public APIs. Map syntax/type errors to 400, business rules to 422.

</details>

<details>
<summary><strong>Q56. Keyset vs OFFSET pagination?</strong></summary>

OFFSET scans/discards rows and drifts under concurrent writes. Keyset uses an indexed `id > cursor`,
stable under writes. Use UUIDv7/time-ordered IDs, an opaque cursor, and an index matching the filter/sort.

</details>

<details>
<summary><strong>Q57. Liveness vs readiness?</strong></summary>

Liveness: is the process alive? Keep it shallow; a DB outage should not restart every pod. Readiness: can
it serve traffic now? Check critical dependencies. ControlPlane's `/healthz` checks Postgres + RabbitMQ,
which is a readiness signal.

</details>

<details>
<summary><strong>Q58. Why an error envelope with a code?</strong></summary>

Clients branch on a stable machine-readable `code`, humans read `message`, support uses `request_id`, and
field validation uses `details`. Do not leak SQL/driver details; log the real cause and return
`internal_error`.

</details>

<details>
<summary><strong>Q59. When 429 vs 503?</strong></summary>

429: the client exceeded a rate limit; include `Retry-After`. 503: the service/dependency is unavailable
or shutting down; also consider `Retry-After`. Do not use 500 for expected backpressure.

</details>

---

## D. Postgres and transactions

<details>
<summary><strong>Q60. What is ACID?</strong></summary>

Atomicity (all-or-nothing), Consistency (constraints/invariants hold), Isolation (concurrent transactions
don't see invalid intermediate states), Durability (committed data survives crash). Postgres gives you
the tools; your schema/transactions still have to use them correctly.

</details>

<details>
<summary><strong>Q61. What isolation levels does Postgres support?</strong></summary>

Read Committed (default), Repeatable Read, Serializable. Read Committed takes a snapshot per statement;
Repeatable Read per transaction; Serializable adds SSI predicate checks and can abort with `40001`.
Higher isolation needs retry logic.

</details>

<details>
<summary><strong>Q62. How does Read Committed handle two concurrent UPDATEs?</strong></summary>

The second UPDATE blocks on the row lock, then re-evaluates its `WHERE` against the updated row
(EvalPlanQual). That is why a conditional `UPDATE ... WHERE version = $v AND status = ...` is safe: the
loser sees the new row and matches zero rows.

</details>

<details>
<summary><strong>Q63. What is a lost update?</strong></summary>

Two transactions read the same value, both compute a new value, and the second overwrites the first. A
conditional UPDATE with a version token, a row lock, or an atomic increment prevents it. A read-then-write
without either is a bug.

</details>

<details>
<summary><strong>Q64. What is write skew?</strong></summary>

Two transactions each read a condition, then write in a way that invalidates the condition together.
Classic: two doctors each see two on call and both go off call. Snapshot isolation allows it; Serializable
or a materialized constraint/row lock prevents it.

</details>

<details>
<summary><strong>Q65. Why is a unique index the arbiter for slug creation?</strong></summary>

It is evaluated atomically at INSERT time. Two concurrent inserts: one commits, the other gets `23505`.
There is no read-then-write gap, and it works across processes/replicas. A pre-check SELECT is a TOCTOU
race.

</details>

<details>
<summary><strong>Q66. How do you map a unique violation?</strong></summary>

Use `errors.As` to get `*pgconn.PgError` and check `Code == "23505"`. Map it to the domain error (e.g.
`tenant_already_exists`). Do not string-match the message. Let unknown codes become 500 with a logged
cause.

</details>

<details>
<summary><strong>Q67. Why a conditional UPDATE instead of SELECT-then-UPDATE?</strong></summary>

One statement is atomic: `WHERE id=$1 AND version=$2 AND status='active'`. Two concurrent requests cannot
both match the same version; the second sees the incremented row and matches zero. SELECT-then-UPDATE has
a lost-update window.

</details>

<details>
<summary><strong>Q68. A conditional UPDATE returns zero rows. How do you disambiguate?</strong></summary>

Re-read the row and check in order: missing → 404; status not allowed → 409
`tenant_update_not_allowed`; status allowed but version differs → 409 `tenant_version_conflict`. In a
50-way PATCH race, losers often see `updating`, so they get `tenant_update_not_allowed`; that is correct.

</details>

<details>
<summary><strong>Q69. What does `SELECT ... FOR UPDATE` do?</strong></summary>

Locks the selected rows until the transaction ends. Concurrent writers block. Use it when you must read,
decide, and write atomically across multiple statements. Keep the transaction short; lock ordering must be
consistent.

</details>

<details>
<summary><strong>Q70. What does `SKIP LOCKED` add?</strong></summary>

Concurrent claimers skip rows already locked by another transaction. Multiple relay/worker replicas claim
disjoint batches instead of blocking. It is for work queues, not for queries that must see every row.

</details>

<details>
<summary><strong>Q71. How do you prevent deadlocks?</strong></summary>

Consistent lock order (ControlPlane: tenant → task), short transactions, and retry on `40P01`. A deadlock
is detected by Postgres, which aborts one transaction; retry the whole transaction, not just the
statement.

</details>

<details>
<summary><strong>Q72. Why a partial unique index?</strong></summary>

It enforces an invariant over a subset of rows, e.g. at most one open task per tenant:
`UNIQUE (tenant_id) WHERE status IN ('accepted','in_progress')`. Historical done/failed tasks do not
conflict. It is a safety net behind the service guards.

</details>

<details>
<summary><strong>Q73. UUIDv7 vs UUIDv4 vs UUIDv5?</strong></summary>

v7 is time-ordered: good for B-tree locality and keyset pagination. v4 is random: fine for uniqueness,
worse for range locality. v5 is deterministic (namespace + name): good for idempotency keys, not row IDs.
ControlPlane uses v7 for rows and v5 for message IDs.

</details>

<details>
<summary><strong>Q74. Why `timestamptz` and a custom JSON format?</strong></summary>

`timestamptz` stores an absolute instant and avoids timezone ambiguity. Go's default `time.Time` JSON uses
RFC3339Nano with variable fractional digits and `+00:00`; ControlPlane wants a fixed
`2006-01-02T15:04:05.000Z` wire format, so it has a custom type.

</details>

<details>
<summary><strong>Q75. How do you index keyset pagination?</strong></summary>

For `WHERE id > $1 ORDER BY id LIMIT $2`, the primary key index works. For `WHERE status=$1 AND id>$2
ORDER BY id`, use `(status, id)`. Verify with `EXPLAIN (ANALYZE, BUFFERS)`; do not guess.

</details>

<details>
<summary><strong>Q76. How do you size a connection pool?</strong></summary>

Start small, measure, respect `max_connections`, and account for all replicas/workers. Little's Law:
concurrency ≈ throughput × latency. More connections increase contention/memory; they do not
automatically increase throughput. Use PgBouncer if connection count is the bottleneck.

</details>

<details>
<summary><strong>Q77. Why does the service layer own transaction boundaries?</strong></summary>

A use case spans multiple store calls (tenant + task, inbox + task + tenant). If each call opened its own
transaction, a crash between them could leave partial state. The service composes; the store takes a `DB`
interface so it works on a pool or a transaction.

</details>

<details>
<summary><strong>Q78. Why `defer tx.Rollback` after `Begin`?</strong></summary>

It guarantees cleanup on every error/panic path. After a successful commit, rollback is a no-op. Without
it, an early return can leak a transaction and hold locks/connections.

</details>

<details>
<summary><strong>Q79. What is an N+1 query, and how do you fix it?</strong></summary>

One query for N parents + N queries for children. Fix with a join, `IN`, batched queries, or a
DataLoader-style batching layer. Verify with query logs/`EXPLAIN`; do not optimize by intuition.

</details>

<details>
<summary><strong>Q80. Why not `CREATE TABLE IF NOT EXISTS` in production?</strong></summary>

It is idempotent for a fresh demo but does not version schema changes, cannot safely alter/drop columns,
and has no rollback/audit. Production needs a migration tool (goose, golang-migrate, Atlas) with reviewed,
ordered migrations and a deploy strategy.

</details>

---

## E. Messaging and ControlPlane

<details>
<summary><strong>Q81. Why is provisioning asynchronous?</strong></summary>

It takes minutes and can fail/retry; holding an HTTP request open is fragile. The API commits durable
intent (tenant + task) and returns; a worker does the long work and reports progress.

</details>

<details>
<summary><strong>Q82. Why a transactional outbox?</strong></summary>

Publish-before-commit can make the worker act on nonexistent state; commit-before-publish can lose the
event on a crash. A DB transaction cannot atomically include RabbitMQ, and RabbitMQ is not XA. The outbox
row commits with the state; the relay publishes only committed rows.

</details>

<details>
<summary><strong>Q83. What exactly is the outbox in ControlPlane?</strong></summary>

The `tasks` table. A task row with `published_at IS NULL` is an unpublished event. It is both the durable
work record and the event to publish; there is no separate outbox table.

</details>

<details>
<summary><strong>Q84. Why is `published_at` separate from `tasks.status`?</strong></summary>

`status` is the business state (accepted/in_progress/done/failed); `published_at` is delivery state (has
the relay handed it to the broker?). Mixing them would make one field serve two lifecycles and break
retries/auditing.

</details>

<details>
<summary><strong>Q85. Why must claim + publish + mark share one transaction?</strong></summary>

Postgres row locks are transaction-scoped. Outside a transaction, the claim lock is released when the
SELECT ends, so another relay can claim the same rows. Keeping the transaction open makes the claim
exclusive and marks published only after the broker confirms.

</details>

<details>
<summary><strong>Q86. Why publisher confirms?</strong></summary>

A plain publish returning nil only means the bytes reached the socket. A confirm means the broker accepted
responsibility. The relay must not mark `published_at` before the confirm, or a crash after marking can
lose the event.

</details>

<details>
<summary><strong>Q87. Why `mandatory=true` and returns?</strong></summary>

A confirm does not prove routing: RabbitMQ can confirm an unroutable message. `mandatory` returns the
message via `basic.return` when no queue matches. The publisher treats a return as an error, not a silent
drop.

</details>

<details>
<summary><strong>Q88. At-least-once vs exactly-once?</strong></summary>

At-least-once: a message may be delivered more than once but not lost. Exactly-once across a DB and broker
needs distributed transactions or a shared log; RabbitMQ + Postgres cannot do it here. The design uses
at-least-once + idempotent apply + order tolerance for an effectively-once business effect.

</details>

<details>
<summary><strong>Q89. Why are duplicates acceptable?</strong></summary>

The consumer is idempotent: the inbox primary key rejects the same `message_id`, and the rank guard
rejects stale/different messages. Duplicates cost work, not correctness. Exactly-once is not achievable
with these components.

</details>

<details>
<summary><strong>Q90. Relay crashes between confirm and commit — what happens?</strong></summary>

The row remains `published_at IS NULL`; on restart the relay republishes. That is a duplicate delivery,
which the worker/consumer tolerate. Never a lost event, never a publish before commit.

</details>

<details>
<summary><strong>Q91. Broker down for an hour — what happens?</strong></summary>

The API still accepts; outbox rows accumulate. The relay retries; when the broker returns, it drains the
backlog. Tenants stay in-flight until published/processed. Monitor oldest unpublished age, not just queue
count.

</details>

<details>
<summary><strong>Q92. Does the outbox grow forever?</strong></summary>

In this demo, published task rows are retained as history, so yes. Production would archive/partition
published rows after retention while keeping unpublished rows. Never delete unpublished rows; monitor
their age.

</details>

<details>
<summary><strong>Q93. Multiple relays — how do they avoid double-claiming?</strong></summary>

`FOR UPDATE SKIP LOCKED` inside the relay transaction. Each relay locks a batch; others skip locked rows
and claim different ones. The lock is held through publish + mark + commit.

</details>

<details>
<summary><strong>Q94. Does the outbox guarantee ordering?</strong></summary>

No global ordering. A single relay processes a batch in `id` order, but multiple relays, retries, and
worker concurrency can reorder. The rank guard is the order-tolerance mechanism. Strict per-tenant order
needs a sequence/partition key and a single consumer.

</details>

<details>
<summary><strong>Q95. Why deterministic `message_id`?</strong></summary>

A worker retry or broker redelivery of the same logical update must produce the same inbox key. Random
UUIDs per attempt defeat deduplication. `UUIDv5(task_id + "/" + status)` is stable and scopes the key to
one task/status.

</details>

<details>
<summary><strong>Q96. How exactly is the UUIDv5 built?</strong></summary>

`uuid.NewSHA1(uuid.NameSpaceURL, []byte(taskID.String()+"/"+status)).String()`. Same task + same status →
same UUID; `done` and `failed` differ; the worker's `occurred_at` is irrelevant to identity.

</details>

<details>
<summary><strong>Q97. Why an inbox table?</strong></summary>

It is a durable idempotency ledger: `message_id` primary key + `ON CONFLICT DO NOTHING`. Brokers don't
guarantee dedup under at-least-once. The inbox is transactional with the state change and auditable.

</details>

<details>
<summary><strong>Q98. Why insert the inbox row first?</strong></summary>

It serializes concurrent duplicates on the `message_id` primary key. The second delivery blocks until the
first transaction ends, then sees zero rows. If inserted after the state change, two deliveries could both
do state work before discovering the duplicate.

</details>

<details>
<summary><strong>Q99. Explain the rank guard.</strong></summary>

`accepted=0`, `in_progress=1`, `done/failed=2`. Apply iff `rank(new) > rank(current)`. Duplicate terminal
is `2 > 2` false → stale. Late `in_progress` after `done` is `1 > 2` false → stale. `accepted → done` is
`2 > 0` true, allowed if `in_progress` was lost.

</details>

<details>
<summary><strong>Q100. Why not a from→to transition table?</strong></summary>

It needs an explicit self-loop for every duplicate and a stale transition for every reordered pair. The
rank comparison encodes the forward-only invariant directly and handles `accepted → done` without special
cases.

</details>

<details>
<summary><strong>Q101. Why both inbox and rank guard?</strong></summary>

Inbox deduplicates the same logical message (same `message_id`). Rank guard handles different messages
arriving out of order (e.g. new `in_progress` after `done`). They solve different failure modes.

</details>

<details>
<summary><strong>Q102. Why is `accepted → done` allowed?</strong></summary>

The rank guard is monotonic, not a strict step machine. If `in_progress` is lost or delayed, the first
terminal outcome should still apply. The first terminal wins; a later `in_progress` is stale.

</details>

<details>
<summary><strong>Q103. Explain the terminal tenant mapping.</strong></summary>

Deploy done: provisioning → active; update done: updating → active; destroy done: destroying →
destroyed. Any failed: → failed. The update is guarded on the expected in-flight status; if the tenant
has moved on, zero rows match and the stale outcome is dropped.

</details>

<details>
<summary><strong>Q104. Why bump the tenant version on a terminal outcome?</strong></summary>

The version is the optimistic-concurrency token. A terminal outcome changes state, so a client holding
the old token must get `tenant_version_conflict` on its next write. It also signals async completion.

</details>

<details>
<summary><strong>Q105. Why is the message's `tenant_id` not trusted?</strong></summary>

The task row is the source of truth. A malformed/malicious message could claim a different tenant; the
consumer loads the tenant from `task.tenant_id` and ignores the claim. This limits blast radius.

</details>

<details>
<summary><strong>Q106. Poison vs transient — how do you classify?</strong></summary>

Permanent/poison: malformed JSON, invalid schema, unknown status, unknown task/tenant, recovered panic.
They will never succeed on retry → `Nack(requeue=false)` → DLQ. Transient: connection reset, timeout, pool
exhaustion → bounded in-process retry, then requeue. Requeueing poison is an infinite hot loop.

</details>

<details>
<summary><strong>Q107. Why `x-delivery-limit=5`?</strong></summary>

It caps broker-side redelivery of a message that keeps failing transiently. After 5 attempts, RabbitMQ
dead-letters it. Without a cap, a persistent failure loops forever and consumes capacity.

</details>

<details>
<summary><strong>Q108. Why quorum queues?</strong></summary>

Replicated, durable, designed for at-least-once work, and support a delivery limit. A classic non-durable
queue can lose messages on broker restart. Quorum queues require `durable=true`; queue type and delivery
limit are table arguments.

</details>

<details>
<summary><strong>Q109. Why reconnect with backoff + jitter?</strong></summary>

AMQP connections are expensive and channels die with them. A supervisor reconnects, re-declares the
idempotent topology, and hands out fresh channels. Backoff avoids hammering a down broker; jitter prevents
all replicas reconnecting in lockstep.

</details>

<details>
<summary><strong>Q110. Why does the consumer process sequentially?</strong></summary>

Bounded unacked deliveries and simple ordering/failure classification. The DB transaction is the
correctness boundary, not a goroutine per message. Parallel consumers need per-tenant ordering, bounded
concurrency, and more careful inbox/lock behavior.

</details>

<details>
<summary><strong>Q111. Why does the worker have no DB connection?</strong></summary>

The worker is untrusted and stateless. It consumes a task, does the work, and publishes progress. The
consumer is the authority on task existence, tenant mapping, and state transitions. This limits blast
radius.

</details>

<details>
<summary><strong>Q112. Why prefetch 1 on the worker?</strong></summary>

Tasks are long-running and sequential. Prefetch 1 means at most one unacked task in flight, so a crash
causes exactly one redelivery. It trades throughput for predictable recovery.

</details>

<details>
<summary><strong>Q113. Why ack only after the terminal update is confirmed?</strong></summary>

If the worker acks before the terminal update is durably accepted, a crash can lose the outcome. Ack after
confirm means the broker has the update before the task is removed. A lost ack redelivers the task;
deterministic IDs make the replay a no-op.

</details>

<details>
<summary><strong>Q114. What if a task is stuck in `in_progress` forever?</strong></summary>

This build has no reaper/heartbeat, so it stays stuck and the tenant stays in-flight. Production needs a
TTL/lease, a reaper that re-publishes/fails stale tasks, and provider reconciliation. Name it as a known
gap.

</details>

<details>
<summary><strong>Q115. What would you change at 10x?</strong></summary>

Batch relay confirms and monitor oldest unpublished age; shard/partition workers by tenant; add a
stuck-task reaper; archive published rows; retention for `processed_messages`; metrics/tracing; auth and
per-tenant rate limits; worker sharding for noisy neighbors. Keep the invariants; change the scale knobs.

</details>

<details>
<summary><strong>Q116. Kafka or SQS instead of RabbitMQ?</strong></summary>

RabbitMQ's per-message ack, requeue, and DLX map directly to the poison/transient model. Kafka is a
partitioned log with offsets; poison handling needs retry/DLQ topics. SQS is managed with visibility
timeouts and DLQ; it maps well to the outbox, but routing/ordering semantics differ. The inbox/idempotency
design survives any of them.

</details>

---

## F. System design and tradeoffs

<details>
<summary><strong>Q117. What is the 8-step backend framework?</strong></summary>

Requirements → API → data/invariants → concurrency/transactions → messaging/delivery → failure modes →
operations/security → tradeoffs. Start with the invariant, then the mechanism that protects it.

</details>

<details>
<summary><strong>Q118. How do you gather requirements in a system-design interview?</strong></summary>

Users/personas, core flows, read/write ratio, latency/SLO, consistency needs, scale, retention, security,
and explicit non-goals. Confirm one or two assumptions before designing; do not spend 10 minutes on
requirements.

</details>

<details>
<summary><strong>Q119. Availability vs consistency — how do you choose?</strong></summary>

Ask what breaks. For tenant state, correctness wins: a stale write must fail, not overwrite. For a feed or
analytics, eventual consistency may be fine. Name the business cost of each; "it depends" is not an
answer.

</details>

<details>
<summary><strong>Q120. Monolith or services?</strong></summary>

Start with a modular monolith unless there is a concrete scaling/ownership/deploy reason to split.
ControlPlane is one deployable unit with HTTP + relay + consumer, plus a separate worker process. Splitting
adds network failure modes, contracts, and ops overhead.

</details>

<details>
<summary><strong>Q121. When would you not use an outbox?</strong></summary>

When the side effect is idempotent and loss is acceptable, when the event is not tied to a DB transaction,
or when the system already has a transactional log (e.g. Kafka transactions/CDC). The outbox adds latency,
a relay, duplicate delivery, and retention work; use it when "publish after commit, never on rollback"
matters.

</details>

<details>
<summary><strong>Q122. When would you not use RabbitMQ?</strong></summary>

When you need long retention/replay/partitioned ordering (Kafka), a fully managed simple queue (SQS), or
request/response RPC. RabbitMQ is strong for routing, per-message ack, and poison/DLX; it is not a
database or an event log.

</details>

<details>
<summary><strong>Q123. What do you monitor for this system?</strong></summary>

Outbox oldest unpublished age; publish latency/confirm failures; consumer lag/unacked; DLQ depth; retry
rate; task age in accepted/in_progress; HTTP latency/error rate by code; DB pool saturation/lock waits;
tenant state distribution. Alert on symptoms (stuck tenants, lag), not just CPU.

</details>

<details>
<summary><strong>Q124. How do you secure a multi-tenant control plane?</strong></summary>

AuthN/AuthZ (tenant-scoped RBAC), TLS, secrets management, least-privilege DB/broker credentials, input
validation, per-tenant rate limits, audit logs, and blast-radius isolation. Never trust a tenant ID from
the message/body; resolve it from the authenticated principal or the task row.

</details>

<details>
<summary><strong>Q125. What is blast radius, and how do you limit it?</strong></summary>

The impact of one failure. Limit it with per-tenant rate limits/quotas, worker sharding, separate queues
for noisy tenants, timeouts/circuit breakers, DLQs, and not sharing a transaction/lock across unrelated
tenants. Name the failure domain explicitly.

</details>

<details>
<summary><strong>Q126. How do you answer a tradeoff question?</strong></summary>

State the decision, the alternative, the failure mode the alternative creates, the cost of your choice,
and the condition under which you would switch. "It depends" without a criterion is a non-answer.

</details>

---

## G. Testing, operations, and behavioral

<details>
<summary><strong>Q127. Why did the ControlPlane lifecycle test flake?</strong></summary>

The running compose relay polled the same Postgres database as the test and could claim a test-created
outbox row, publishing it to the dev vhost where the in-process worker was not listening. A separate
RabbitMQ vhost alone did not fix the shared Postgres claim. The fix separated the test database and vhost,
and an advisory lock serialized test packages.

</details>

<details>
<summary><strong>Q128. Why test against real Postgres/RabbitMQ?</strong></summary>

The behaviors under test are constraints, row locks, `SKIP LOCKED`, publisher confirms, and DLQ routing.
Mocks would only test your mock. Use real dependencies for integration; use unit tests for pure domain
logic. Isolate test resources from the dev stack.

</details>

<details>
<summary><strong>Q129. What does the race detector prove?</strong></summary>

It detects unsynchronized memory accesses that occur during the test run. It does not prove the absence of
races, and it does not detect logical race conditions/TOCTOU. Run it in CI and on concurrency tests.

</details>

<details>
<summary><strong>Q130. What does the full lifecycle test cover?</strong></summary>

Create → active → patch → active → delete → destroyed → recreate 409, with real Postgres and RabbitMQ,
in-process relay/consumer/worker. It covers the state machine, outbox, consumer, and terminal outcomes
end to end. It does not start an HTTP server; the HTTP API is exercised manually/with handlers.

</details>

<details>
<summary><strong>Q131. Hardest bug story — outline?</strong></summary>

Symptom → hypotheses → instrumentation → root cause (shared test DB/vhost, relay claiming test rows) →
fix (separate resources + advisory lock) → regression test → systemic lesson (isolate test environments,
not just queue names). Keep it concrete and own your part.

</details>

<details>
<summary><strong>Q132. Disagreement story — outline?</strong></summary>

Two options and stakes → steelman the other side → evidence (correctness/cost/ops) → decision → commit
even if not your first choice → reflection. ControlPlane example: outbox vs publish-in-request; rank guard
vs from→to table.

</details>

<details>
<summary><strong>Q133. Failure story — outline?</strong></summary>

What happened → your concrete contribution (no passive voice) → immediate mitigation → root cause →
durable fix → what changed in your process. Do not pick a failure that suggests you cannot do the job;
pick one where the fix changed your behavior.

</details>

<details>
<summary><strong>Q134. Leadership without authority — outline?</strong></summary>

Gap → who needed convincing → artifact (RFC, demo, checklist, migration plan, mentorship) → adoption
metric → why it stuck. Use a real metric or a concrete behavior change.

</details>

<details>
<summary><strong>Q135. Tight deadline — outline?</strong></summary>

Constraint → how you cut scope without cutting the invariant → what you deferred and how you communicated
it → result/debt → what you would sequence differently. ControlPlane example: correctness core first,
non-goals documented, hardening gaps named.

</details>

<details>
<summary><strong>Q136. Why Go?</strong></summary>

I/O-bound, concurrency-heavy service: HTTP, Postgres, RabbitMQ, reconnect loops, workers. Go's explicit
concurrency and small deployment footprint fit. The correctness mechanism is still Postgres, not
goroutines; Go issues the right statements with bounded concurrency and stops cleanly.

</details>

<details>
<summary><strong>Q137. Why this role/company?</strong></summary>

Three specifics: the problem domain, the scale/constraints, and the engineering bar. If you don't know
enough, say what you want to learn and ask a question. Do not generic-flatter or recite the website.

</details>

<details>
<summary><strong>Q138. What questions should you ask?</strong></summary>

Hardest invariant and how it's enforced; where async messaging is used and what guarantee they rely on;
on-call/alert pain; biggest funded debt; what success looks like in 90 days; how design decisions are
made; cross-team API change process. Pick 3–4.

</details>

---

## Scoring sheet

| Section | Green | Amber | Red |
|---|---:|---:|---:|
| A. Go fundamentals | | | |
| B. Concurrency | | | |
| C. HTTP/API | | | |
| D. Postgres | | | |
| E. Messaging/ControlPlane | | | |
| F. System design | | | |
| G. Testing/behavioral | | | |

Target before Thursday: no reds in A–E; at most two ambers. Behavioral can be amber until Day 6.
