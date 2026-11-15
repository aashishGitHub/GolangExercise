# Day 3 — HTTP, API design, Postgres, and transaction boundaries

**Goal:** whiteboard the full request lifecycle and defend every concurrency choice with a failure
mode. This is the day that makes the ControlPlane demo sound like engineering, not a feature tour.

**You should be able to answer:** 201 vs 202, 400 vs 422 vs 409, idempotency keys, optimistic
concurrency, pool sizing, transaction boundaries, unique-index arbitration, conditional updates,
`FOR UPDATE SKIP LOCKED`, isolation levels, and keyset pagination.

---

## Timebox (120 min)

| Time | Block |
|---|---|
| 0:00–0:10 | Cold recall: draw `POST /v1/tenants` end to end |
| 0:10–0:55 | Mechanisms below |
| 0:55–1:25 | Active practice: SQL + HTTP drills |
| 1:25–1:45 | Grill |
| 1:45–1:55 | Explain-back: "why the DB is the arbiter" |
| 1:55–2:00 | Log weak spots |

---

## 1. HTTP server correctness

### Timeouts are not optional

**Description.** `http.Server` has separate timeouts for reading headers, reading the full request, writing the response, and idle keep-alive connections. `ReadHeaderTimeout` protects against slowloris; `WriteTimeout` must be disabled or handled per-route for streaming. A socket timeout does not cancel the handler's work, so context propagation is still required.

```go
srv := &http.Server{
    Addr:              ":8080",
    Handler:           router,
    ReadHeaderTimeout: 5 * time.Second,  // slowloris protection
    ReadTimeout:       15 * time.Second, // headers + body
    WriteTimeout:      30 * time.Second, // response write; 0 for streaming
    IdleTimeout:       60 * time.Second, // keep-alive reuse
    MaxHeaderBytes:    1 << 20,
}
```

- `ReadHeaderTimeout` is the most important one for public HTTP.
- `WriteTimeout` breaks long-lived SSE/WebSocket connections; set it to 0 or use a per-route deadline.
- A timeout without context propagation only closes the socket; the handler may keep working. Pass
  `r.Context()` and per-call timeouts to the DB/broker.
- `http.TimeoutHandler` buffers the whole response and is not suitable for streaming.

### Graceful shutdown

**Description.** On SIGTERM, stop accepting new connections, let in-flight requests finish within a deadline, then close the database and broker. `srv.Shutdown(ctx)` performs the stop-and-drain step; it does not wait forever, so the context deadline is the bound. In Kubernetes, fail readiness before draining.

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

go func() {
    <-ctx.Done()
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    _ = srv.Shutdown(shutdownCtx)
}()

if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
    return err
}
```

`Shutdown` stops accepting new connections and waits for in-flight requests until the context expires.
Close the DB/broker after the HTTP server and background workers stop. In Kubernetes, fail readiness
first, then drain; do not rely on a hard kill.

### Middleware order

**Description.** Middleware wraps the handler outside-in, so order changes behavior. Request ID should run first for correlation, recoverer should catch panics before logging, auth and rate limits should reject before expensive body work, and the body limit should run before JSON decoding. A timeout wraps the business handler.

Typical order, outside in:

1. Request ID — every later log/error can correlate.
2. Recoverer — a panic becomes a 500, not a process crash.
3. Access log — latency, status, request ID.
4. Rate limit / auth — reject cheaply before business work.
5. Body limit — cap memory before decoding.
6. Timeout — bound the handler.
7. Business handler.

Order is a design decision: if auth is inside the body limit, an unauthenticated attacker can still
make you buffer a large body. If recoverer is inside logging, the log may never see the panic.

### JSON decoding

**Description.** Decode into a typed request struct, cap the body with `http.MaxBytesReader`, and reject trailing JSON so `{} {}` is not silently accepted. `DisallowUnknownFields` is a strictness choice: it catches typos but breaks clients that add forward-compatible fields. Malformed JSON is 400; well-formed JSON that fails business rules is 422.

```go
r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
dec := json.NewDecoder(r.Body)
dec.DisallowUnknownFields()

var req createTenantRequest
if err := dec.Decode(&req); err != nil {
    // malformed_request -> 400
}

// Reject trailing data so `{} {}` is not silently accepted.
if err := dec.Decode(&struct{}{}); err != io.EOF {
    // malformed_request -> 400
}
```

`DisallowUnknownFields` is a tradeoff: it catches typos, but it breaks clients that add fields for
forward compatibility. Use it for strict internal contracts; be lenient for public APIs where additive
fields are expected.

Distinguish:

- **400 malformed** — invalid JSON, wrong type, missing body.
- **422 validation** — well-formed JSON that violates business rules (blank name, bad slug).
- **409 conflict** — valid request, but current state forbids it (duplicate slug, stale version,
  wrong status).
- **404 not found** — resource does not exist.
- **500 internal** — unexpected; log the cause, do not leak SQL/driver details.
- **503 unavailable** — dependency down or shutting down; include `Retry-After` when possible.

### Error envelope

**Description.** An error response should carry a stable machine-readable `code`, a human-readable `message`, optional field-level `details`, and a `request_id`. Clients branch on the code; humans read the message; support searches by request ID. Never leak SQL, driver, or stack details in the public response.

A stable machine-readable code is more useful than a prose message:

```json
{
  "error": {
    "code": "tenant_version_conflict",
    "message": "tenant version conflict",
    "details": {},
    "request_id": "..."
  }
}
```

Clients branch on `code`; humans read `message`; support searches by `request_id`. Keep `details` for
field-level validation, not stack traces.

---

## 2. API design decisions

### 201 vs 202

**Description.** `201 Created` means the resource exists when the response is sent, and should include `Location`. `202 Accepted` means the request was accepted but the work is asynchronous, so the response should expose the current state. The distinction is about what is true at response time, not about whether the handler returned quickly.

- **201 Created** — the resource exists synchronously when the response is sent. Include `Location`.
- **202 Accepted** — the request was accepted, but the work is asynchronous. The response should show
  the current state (`updating`) so the client is not guessing.

ControlPlane: `POST` is 201 because tenant + deploy task are committed before responding. `PATCH` and
`DELETE` are 202 because they only start an async worker task.

### PUT vs PATCH

**Description.** `PUT` replaces the whole resource (or creates it at a known URI) and should be idempotent. `PATCH` applies a partial update; it is not automatically idempotent, but a version-guarded patch can be. Choose by the client's intent, not by which verb is shorter.

- `PUT` replaces the resource (or creates it at a known URI); it should be idempotent.
- `PATCH` applies a partial update. It is not automatically idempotent, but a well-designed patch with
  a version token can be.

### Optimistic concurrency: version vs ETag

**Description.** Optimistic concurrency prevents lost updates by making the write conditional on the version the client read. A body `version` field is explicit and easy for JSON clients; `ETag` plus `If-Match` is the HTTP-native equivalent and works for any representation. A stale version must return 409, not silently overwrite.

ControlPlane uses a `version` integer in the body:

```json
{"name": "Acme Inc", "version": 2}
```

The UPDATE is conditional on `version = $v`; a stale version returns 409. This is simple and explicit.

The HTTP-native alternative is `ETag` + `If-Match`:

```http
GET /v1/tenants/123
ETag: "2"

PATCH /v1/tenants/123
If-Match: "2"
```

Both prevent lost updates. A body version is easier for JSON clients; `If-Match` is more standard and
works for any representation. Pick one and be consistent. A `DELETE` body is less universally
supported by proxies than a header; ControlPlane accepts it, but `If-Match` is the safer public API.

### Idempotency keys for unsafe retries

**Description.** An idempotency key is a client-generated identifier stored by the server with the original response. If the same key arrives again with the same payload, the server returns the stored result instead of repeating side effects; a different payload is a conflict. It solves duplicate requests after a timeout, while versioning solves lost updates on an existing resource.

A client may retry a `POST` after a timeout and not know whether the first request committed. An
`Idempotency-Key` header lets the server store the key and the original response:

- Same key + same request → return the stored response.
- Same key + different payload → 409/422.
- Key expires after a retention window; store it in a table with a unique constraint.

This is different from optimistic concurrency: idempotency prevents duplicate side effects; versioning
prevents lost updates.

### Pagination: keyset, not OFFSET

**Description.** Keyset pagination uses the last seen sort key as the cursor: `WHERE id > $cursor ORDER BY id LIMIT $n`. It stays stable when rows are inserted or deleted and does not scan and discard earlier rows the way `OFFSET` does. The cursor should be opaque, validated, and tied to the sort order and index.

```sql
SELECT id, slug, name, status, version, created_at, updated_at
FROM tenants
WHERE id > $1
  AND ($2 = '' OR status = $2)
ORDER BY id
LIMIT $3;
```

- `OFFSET` gets slower as the offset grows and drifts when rows are inserted/deleted between pages.
- Keyset uses the last seen key; with UUIDv7 (time-ordered), `id` is a stable cursor.
- The cursor should be opaque to clients (base64/encoded), validated on input, and tied to the sort
  order. Changing sort order invalidates old cursors.
- For filtered lists, index `(status, id)`; for unfiltered, `(id)`.

### Validation and normalization

**Description.** Validate input at the HTTP boundary, enforce invariants in the domain and database, and normalize once so two layers do not disagree. A regex check in the handler is not a substitute for a unique index or a `CHECK` constraint, because only the database sees every concurrent writer.

Validate at the edge, enforce invariants in the database. The slug regex belongs in the domain and the
DB `CHECK`; the unique index is the final arbiter. Normalize case/whitespace deliberately; do not let
two layers disagree.

---

## 3. pgx and transaction boundaries

### Pool, not a global connection

**Description.** `pgxpool.Pool` is a concurrency-safe pool that should be created once and shared; opening a connection per request defeats pooling and exhausts the database. Size it as a queueing problem, not "bigger is better": more connections add contention and memory. Set max lifetime, idle time, and health checks so stale connections are recycled.

```go
pool, err := pgxpool.New(ctx, databaseURL)
if err != nil { ... }
defer pool.Close()

if err := pool.Ping(ctx); err != nil { ... }
```

`pgxpool.Pool` is concurrency-safe and should be shared. Do not open a connection per request.

Pool sizing is a queueing problem, not "bigger is better":

- Each connection consumes DB memory and a backend process.
- More connections increase contention and context switching.
- Start with a small pool and measure; use Little's Law as a sanity check:
  `concurrency ≈ throughput × latency`.
- Respect the database's `max_connections`; if you use PgBouncer, know whether it is transaction or
  session pooling (prepared statements and session state behave differently).
- Set `MaxConnLifetime`, `MaxConnIdleTime`, and a health check period so stale connections are recycled.

### The `DB` interface and `WithTx`

**Description.** `*pgxpool.Pool` and `pgx.Tx` both implement `Exec`, `Query`, and `QueryRow`, so a small `DB` interface lets store functions run standalone or inside a caller-owned transaction. The service layer decides where the transaction begins and ends; the store never commits. `defer tx.Rollback` guarantees cleanup on every error or panic path.

```go
type DB interface {
    Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
    Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
    QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
```

Both `*pgxpool.Pool` and `pgx.Tx` satisfy it. Store functions take `DB`, so they work standalone or
inside a caller-owned transaction. The service layer owns the boundary:

```go
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(db DB) error) error {
    tx, err := pool.Begin(ctx)
    if err != nil {
        return err
    }
    defer tx.Rollback(ctx) // no-op after commit

    if err := fn(tx); err != nil {
        return err
    }
    return tx.Commit(ctx)
}
```

Why this shape:

- `defer Rollback` guarantees cleanup on error or panic.
- A commit failure is returned; a rollback failure is usually ignored because the transaction is
  already doomed.
- Do not hold a transaction open across a slow network call unless the design requires it. The outbox
  relay deliberately holds the claim lock across the broker publish; that is a bounded local I/O and
  the lock is the point. At higher scale, batch, set timeouts, and measure lock duration.

### `QueryRow`, `ErrNoRows`, `RowsAffected`

**Description.** `QueryRow` scans one row and returns `pgx.ErrNoRows` when the query matches nothing. For conditional writes, `RowsAffected` tells you whether the `WHERE` matched; zero rows is a signal to re-read and disambiguate. `RETURNING` can avoid a second read when the caller needs the updated row.

```go
row := db.QueryRow(ctx, `SELECT ... WHERE id = $1`, id)
var t Tenant
if err := row.Scan(&t.ID, &t.Slug); err != nil {
    if errors.Is(err, pgx.ErrNoRows) { ... }
    return err
}
```

For conditional writes, `RowsAffected` tells you whether the WHERE matched:

```go
tag, err := db.Exec(ctx, `
    UPDATE tenants
    SET status = $2
    WHERE id = $1 AND status = $3`, id, next, expected)
if err != nil { return err }
if tag.RowsAffected() == 0 { /* stale or wrong state */ }
```

`RETURNING` can avoid a second read when you need the updated row. `QueryRow` returns `ErrNoRows` if
the UPDATE matched nothing.

### Unique violations

**Description.** Postgres reports a unique violation with SQLSTATE `23505`; foreign-key violations are `23503`, serialization failures `40001`, and deadlocks `40P01`. Detect them with `errors.As` on `*pgconn.PgError` and map only the codes you have a policy for. String-matching the message is brittle and localized.

```go
var pgErr *pgconn.PgError
if errors.As(err, &pgErr) && pgErr.Code == "23505" {
    return domain.NewError(domain.ErrTenantAlreadyExists, "...")
}
```

Do not string-match the error message. `23505` is `unique_violation`; `23503` is foreign-key violation;
`40001` is serialization failure; `40P01` is deadlock detected. Map only the codes you have a policy
for; let the rest become 500 with a logged cause.

### Statement timeouts and cancellation

**Description.** Put a context timeout on each query or transaction and set `statement_timeout` at the role or session level as a backstop. The context cancels the client-side wait, while the server-side timeout stops the query even if the client disconnects. A query that ignores cancellation can keep locks and a connection after the request is gone.

Set a per-query or per-transaction timeout:

```go
ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
defer cancel()
```

Also set `statement_timeout` at the role/session level as a backstop. A query that ignores context
cancellation can hold locks and connections after the client is gone.

---

## 4. Postgres concurrency — the part interviewers push on

### MVCC and isolation

**Description.** Postgres uses multiversion concurrency control: readers see a snapshot, and writers create new row versions instead of overwriting in place. Read Committed takes a snapshot per statement, Repeatable Read per transaction, and Serializable adds SSI predicate checks that can abort with `40001`. Higher isolation prevents more anomalies but requires a retry policy.

Postgres uses MVCC: readers see a snapshot, writers create new row versions. The default isolation is
**Read Committed**: each statement sees a snapshot taken at the start of that statement.

- Two concurrent `UPDATE`s to the same row: the second blocks, then re-evaluates its `WHERE` against
  the updated row (EvalPlanQual). This is why a conditional `UPDATE ... WHERE version = $v` is safe.
- **Repeatable Read** takes one snapshot for the whole transaction; a concurrent update can cause a
  serialization failure (`40001`).
- **Serializable** adds SSI predicate checks; it prevents write skew but requires retry logic on
  `40001`.

Write skew example: two doctors each check "at least two doctors are on call", then both go off call.
Each transaction is individually valid under snapshot isolation, but the invariant breaks. Serializable
or a materialized constraint/row lock is needed.

For ControlPlane, Read Committed plus unique constraints and conditional updates is sufficient. Do not
reach for Serializable without a retry policy and a measured need.

### Unique index as the arbiter

**Description.** A unique index is evaluated atomically at insert time, so two concurrent inserts with the same key cannot both commit: one succeeds, the other gets `23505`. This works across processes and replicas, unlike an in-process mutex, and it removes the read-then-write gap that a pre-check `SELECT` leaves open.

```sql
INSERT INTO tenants (id, slug, name, status, version)
VALUES ($1, $2, $3, 'provisioning', 1);
```

Two concurrent inserts with the same slug: one commits, the other gets `23505`. There is no
read-then-write gap, so no TOCTOU. A pre-check `SELECT` would be a race: both requests can see "not
found" and then both insert; the index still saves you, but the pre-check adds a round trip and a false
sense of safety.

**Senior phrasing:** "The database constraint is the concurrency control. The application maps the
violation to a domain error; it does not try to serialize the race in Go."

### Conditional UPDATE instead of read-then-write

**Description.** A conditional `UPDATE ... WHERE version = $v AND status = $s` makes the check and the write one atomic statement, so two clients cannot both read the same version and overwrite each other. If it affects zero rows, re-read to distinguish not-found, wrong-state, and stale-version before choosing 404 or 409.

Bad:

```sql
SELECT version, status FROM tenants WHERE id = $1;
-- application checks version/status
UPDATE tenants SET name = $2 WHERE id = $1;
```

Two requests can read the same version and both update; the last write wins (lost update).

Good:

```sql
UPDATE tenants
SET name = $2,
    status = 'updating',
    version = version + 1,
    updated_at = now()
WHERE id = $1
  AND version = $3
  AND status = 'active'
RETURNING id, slug, name, status, version, created_at, updated_at;
```

One statement is atomic. If it returns zero rows, re-read to disambiguate:

1. No row → 404 `tenant_not_found`.
2. Row exists but status is not allowed → 409 `tenant_update_not_allowed`.
3. Status is allowed but version differs → 409 `tenant_version_conflict`.

Order matters. In a 50-way PATCH race, the losers often see `updating` (the winner already changed the
status), so they get `tenant_update_not_allowed`, not `tenant_version_conflict`. That is correct: the
zero-rows result alone cannot tell you why.

### Row locks and lock ordering

**Description.** `SELECT ... FOR UPDATE` locks the selected rows until the transaction commits or rolls back, which lets you read, decide, and write across multiple statements. Every code path must acquire locks in the same order, or two paths can deadlock in an ABBA pattern. Keep the transaction short, and retry the whole transaction after `40P01`.

`SELECT ... FOR UPDATE` locks the selected rows until the transaction ends. Use it when you need to
read, decide, and write atomically across multiple statements.

- Lock rows in a **consistent order** everywhere. ControlPlane locks tenant then task. The API path
  also touches tenant before task. Reversing the order in one path creates an ABBA deadlock.
- Keep transactions short. A lock held across a slow broker call blocks other writers.
- Deadlock detection aborts one transaction with `40P01`; the application should retry the whole
  transaction, not just the statement.

### `SKIP LOCKED` for queues

**Description.** `FOR UPDATE SKIP LOCKED` claims rows for a queue: each transaction locks a batch and concurrent workers skip rows already locked, so replicas claim disjoint work. The lock is transaction-scoped, so the claim, publish, and mark must share one transaction. It is not appropriate for queries that must eventually see every row.

```sql
SELECT id, tenant_id, type, status, created_at, updated_at, tenant_slug, tenant_name
FROM tasks
WHERE published_at IS NULL
ORDER BY id
LIMIT 100
FOR UPDATE SKIP LOCKED;
```

- `FOR UPDATE` claims rows; `SKIP LOCKED` lets concurrent relay replicas skip rows another transaction
  already holds. They claim disjoint batches.
- The lock is **transaction-scoped**. If you run the SELECT outside an explicit transaction, the lock
  is released when the statement ends, and two relays can claim the same rows/republish.
- `SKIP LOCKED` is for work queues, not for "read all rows eventually" queries; it can skip rows.
- The claim, publish, and `published_at` update must share one transaction so the lock stays held.

### Partial unique indexes

**Description.** A partial unique index enforces uniqueness only for rows matching a predicate, such as "at most one open task per tenant". Historical rows are excluded, so the constraint expresses a state invariant without blocking new work. The service's status guards remain the primary check; the index is the cross-process safety net.

```sql
CREATE UNIQUE INDEX tasks_one_open_per_tenant
ON tasks (tenant_id)
WHERE status IN ('accepted', 'in_progress');
```

This enforces "at most one open task per tenant" without preventing historical tasks. The service's
status guards are the primary mechanism; the index is a safety net. Constraints are cheap; use them to
make invalid states unrepresentable.

### Keyset pagination and indexes

**Description.** The index must match the filter and the sort order: `(id)` for an unfiltered list, `(status, id)` for a status-filtered list. `WHERE id > $1 ORDER BY id LIMIT $2` can walk the index instead of scanning discarded rows. Verify the plan with `EXPLAIN (ANALYZE, BUFFERS)` rather than guessing.

`WHERE id > $1 ORDER BY id LIMIT $2` uses the primary key index. For `WHERE status = $1 AND id > $2
ORDER BY id`, create `(status, id)`. Verify with `EXPLAIN (ANALYZE, BUFFERS)`; do not guess.

### UUIDv7 vs UUIDv4

**Description.** UUIDv7 embeds a timestamp, so values sort roughly by creation time and give better B-tree locality and keyset pagination. UUIDv4 is random: fine for uniqueness, worse for index locality and range scans. UUIDv5 is deterministic from a namespace and name, which is why it fits idempotent message IDs rather than row IDs.

- UUIDv7 is time-ordered: good for keyset pagination and index locality.
- UUIDv4 is random: fine for uniqueness, worse for B-tree locality and range pagination.
- Do not use UUIDv5 for row IDs; ControlPlane uses UUIDv5 for deterministic **message IDs** because the
  same logical update must produce the same key. Different jobs, different tools.

### N+1 and query shape

**Description.** An N+1 is one query to list N parents followed by N queries for their children, which turns one logical read into N+1 round trips. Fix it with a join, an `IN` batch, or a DataLoader-style layer that batches keys. Use `EXPLAIN` to check row estimates, index usage, and whether a sequential scan is appropriate for the table size.

An N+1 is one query to list N parents, then N queries for children. Fix with a join, `IN`, a batched
query, or a DataLoader-style batching layer. `EXPLAIN` the actual query; check row estimates, index
usage, and whether a sequential scan is appropriate for the table size.

---

## 5. Active practice (30 min)

### Drill 1 — draw the create path (8 min)

Draw, in order:

1. Client → chi router → RequestID/Recoverer.
2. Handler decodes/validates.
3. `service.CreateTenant` opens a transaction.
4. `INSERT tenants` + `INSERT tasks (published_at = NULL)`.
5. Commit.
6. Post-commit `notify()` wakes the relay.
7. Handler returns 201 + `Location`.
8. Relay claims with `FOR UPDATE SKIP LOCKED`, publishes with confirms, marks published.
9. Worker consumes, publishes `in_progress`/`done`.
10. Consumer inserts inbox row, locks tenant → task, rank guard, updates task/tenant, commits, acks.

Say out loud where a crash can happen and what the recovery is. If you cannot, re-read
[`DESIGN.md` §6 and §8](../../Avid/ControlPlane/docs/DESIGN.md).

### Drill 2 — write the conditional update (7 min)

Write the PATCH SQL and the disambiguation order. Then explain why this is better than
`SELECT` → check → `UPDATE`. Use the exact error codes from ControlPlane.

### Drill 3 — keyset pagination (5 min)

Write the query for `GET /v1/tenants?status=active&limit=20&cursor=<uuid>`. Add the index you would
create. Explain why `OFFSET 100000` is worse and what "page drift" means.

### Drill 4 — HTTP status drill (10 min)

For each scenario, choose the status and the error code:

1. Body is `{"slug":}` (invalid JSON).
2. Body is valid JSON but `name` is blank.
3. `POST` with a slug that already exists.
4. `PATCH` with `version` missing.
5. `PATCH` with an old version while the tenant is `active`.
6. `PATCH` while the tenant is `updating`.
7. `GET` a random UUID.
8. `DELETE` a tenant in `provisioning`.
9. `GET /healthz` while RabbitMQ is down.
10. A SQL connection reset during a query.

Answers are in the grill below; write your own first.

---

## 6. Grill

<details>
<summary><strong>Q1. Why 201 for create but 202 for PATCH/DELETE?</strong></summary>

201 means the resource exists synchronously at response time. 202 means the request was accepted but
the work is asynchronous. ControlPlane commits the tenant + deploy task before the 201; PATCH/DELETE
only move the tenant to `updating`/`destroying` and create a task, so the final state is not known yet.

</details>

<details>
<summary><strong>Q2. 400 vs 422 vs 409?</strong></summary>

400: malformed request (invalid JSON, wrong type, missing body). 422: well-formed but fails business
validation (blank name, bad slug). 409: valid request conflicts with current state (duplicate slug,
stale version, wrong status). Do not use 400 for a business-rule failure.

</details>

<details>
<summary><strong>Q3. Why use an idempotency key if you already have a version?</strong></summary>

They solve different problems. Version/ETag prevents lost updates on an existing resource. An
idempotency key prevents duplicate side effects when a client retries an unsafe request (e.g. POST)
after a timeout and does not know whether the first attempt committed.

</details>

<details>
<summary><strong>Q4. PATCH vs PUT?</strong></summary>

PUT replaces the resource (or creates at a known URI) and is idempotent. PATCH applies a partial
change; it is not inherently idempotent, but a version-guarded patch can be. ControlPlane uses PATCH
for a name change plus a version token.

</details>

<details>
<summary><strong>Q5. Why is a DELETE body a questionable API choice?</strong></summary>

Some proxies, clients, and caches do not forward DELETE bodies reliably. `If-Match` or a query
parameter is more interoperable. ControlPlane accepts a body version for simplicity; in a public API I
would prefer `If-Match`.

</details>

<details>
<summary><strong>Q6. Why not use `OFFSET` pagination?</strong></summary>

It scans and discards `OFFSET` rows, so it gets slower as the page number grows, and concurrent
inserts/deletes shift the window, causing duplicate or missing rows ("page drift"). Keyset pagination
uses an indexed `id > cursor` and is stable under writes.

</details>

<details>
<summary><strong>Q7. How do you size a Postgres connection pool?</strong></summary>

Start small, measure, and respect the database's connection limit. Use Little's Law as a sanity check:
concurrency ≈ throughput × latency. More connections increase contention and memory; they do not
automatically increase throughput. Account for all replicas and background workers, and use PgBouncer
if connection count is the bottleneck.

</details>

<details>
<summary><strong>Q8. Why does the service layer own the transaction boundary?</strong></summary>

A use case often spans multiple store calls that must commit or roll back together (tenant + task,
inbox + task + tenant). If each store call opened its own transaction, a crash between them would leave
partial state. The service composes the use case; the store stays transaction-agnostic via `DB`.

</details>

<details>
<summary><strong>Q9. Why `defer tx.Rollback` after `Begin`?</strong></summary>

It guarantees the transaction is cleaned up on every error/panic path. After a successful `Commit`,
the rollback is a no-op. Without it, an early return can leak a transaction and hold locks/connections.

</details>

<details>
<summary><strong>Q10. Why is a unique index better than a pre-check SELECT for slug uniqueness?</strong></summary>

A pre-check has a read-then-write race: two requests can both see "not found" and then both insert.
The unique index is evaluated atomically at insert time; one commits, the other gets `23505`. It also
works across processes and replicas, where an in-process mutex does not.

</details>

<details>
<summary><strong>Q11. A conditional UPDATE returns zero rows. What do you do?</strong></summary>

Re-read the row and disambiguate in order: missing → 404; status not allowed → 409
`tenant_update_not_allowed`; status allowed but version differs → 409 `tenant_version_conflict`. The
zero-row result alone cannot distinguish these three cases.

</details>

<details>
<summary><strong>Q12. `FOR UPDATE` vs `SKIP LOCKED`?</strong></summary>

`FOR UPDATE` locks the selected rows until the transaction ends; concurrent readers/writers block.
`SKIP LOCKED` makes a concurrent claim skip rows already locked, so multiple queue consumers can claim
disjoint batches. Use `SKIP LOCKED` for work queues, not for queries that must see every row.

</details>

<details>
<summary><strong>Q13. Why must the outbox claim + publish + mark share one transaction?</strong></summary>

Postgres row locks are transaction-scoped. If the claim runs outside a transaction, the lock is
released at statement end and another relay can claim the same rows. Keeping the transaction open
through publish + mark makes the claim exclusive and ensures `published_at` is only set after the
broker confirms.

</details>

<details>
<summary><strong>Q14. Why lock tenant before task?</strong></summary>

Consistent lock ordering prevents ABBA deadlocks. The API path touches tenant before task; the consumer
does the same. If one path locked task then tenant while another locked tenant then task, two
transactions could each hold one lock and wait for the other.

</details>

<details>
<summary><strong>Q15. What isolation level does ControlPlane need?</strong></summary>

Read Committed is enough because the invariants are enforced by unique indexes and conditional
updates. Serializable would add predicate locking and require retrying `40001` errors; it is not
needed for these races. The consumer's multi-row update is protected by row locks and the inbox/rank
guards.

</details>

<details>
<summary><strong>Q16. What is write skew, and does ControlPlane have it?</strong></summary>

Write skew is two transactions each reading a condition, then writing in a way that invalidates the
condition together. Classic example: two doctors each see two on call and both go off call. ControlPlane's
slug/version races are direct write conflicts, handled by constraints/conditional updates; the
one-open-task invariant is backed by a partial unique index.

</details>

<details>
<summary><strong>Q17. Why UUIDv7 for row IDs but UUIDv5 for message IDs?</strong></summary>

UUIDv7 is time-ordered, which helps B-tree locality and keyset pagination for rows. UUIDv5 is
deterministic: the same `task_id + "/" + status` always yields the same message ID, which is exactly
what the inbox idempotency key needs. Using UUIDv7 for messages would make every retry a new key and
defeat deduplication.

</details>

<details>
<summary><strong>Q18. What does `/healthz` check, and should it check the DB?</strong></summary>

ControlPlane's `/healthz` checks Postgres and RabbitMQ and returns 503 with a `dependency` field if
either is down. That is a readiness signal. A liveness probe should be shallow (process is alive); if
liveness checks the DB, a transient DB outage can restart every pod and make the outage worse.

</details>

<details>
<summary><strong>Q19. How do you prevent an unbounded request body?</strong></summary>

Wrap the body with `http.MaxBytesReader` before decoding; set a server-level `MaxHeaderBytes`; reject
unknown/oversized fields; and consider a reverse-proxy limit. A JSON decoder will happily allocate for
a huge body otherwise.

</details>

<details>
<summary><strong>Q20. What HTTP status for a database connection reset?</strong></summary>

500 `internal_error` (or 503 if you have a clear dependency-unavailable policy and a `Retry-After`).
Do not leak the driver error. Log the real cause with `request_id`; return a stable envelope. Retry
only if the operation is safe/idempotent.

</details>

### Rapid answer key

| # | Question | Model answer | Section |
|---|---|---|---|
| Q1 | Why 201 for create but 202 for PATCH/DELETE? | 201 means the resource exists at response time; 202 means the request was accepted but the work is asynchronous. | §2 |
| Q2 | 400 vs 422 vs 409? | 400 is malformed, 422 is well-formed but fails business validation, 409 conflicts with current state. | §1, §2 |
| Q3 | Why use an idempotency key if you already have a version? | Version prevents lost updates on an existing resource; an idempotency key prevents duplicate side effects from a retried unsafe request. | §2 |
| Q4 | PATCH vs PUT? | PUT replaces the resource and is idempotent; PATCH applies a partial change and is idempotent only when version-guarded. | §2 |
| Q5 | Why is a DELETE body questionable? | Proxies and clients do not forward DELETE bodies reliably; `If-Match` or a query parameter is more interoperable. | §2 |
| Q6 | Why not `OFFSET` pagination? | It scans and discards rows and suffers page drift; keyset uses an indexed `id > cursor` and is stable under writes. | §2 |
| Q7 | How do you size a Postgres pool? | Start small, use Little's Law as a sanity check, respect `max_connections`, account for every replica, and use PgBouncer if needed. | §3 |
| Q8 | Why does the service own the transaction boundary? | A use case spans multiple store calls that must commit or roll back together; otherwise a crash leaves partial state. | §3 |
| Q9 | Why `defer tx.Rollback` after `Begin`? | It cleans up on every error or panic path and is a no-op after a successful commit. | §3 |
| Q10 | Why a unique index instead of a pre-check SELECT? | A pre-check has a read-then-write race; the unique index is atomic at INSERT and works across replicas. | §4 |
| Q11 | A conditional UPDATE returns zero rows. Now what? | Re-read and disambiguate: 404 missing, 409 wrong status, 409 stale version. | §4 |
| Q12 | `FOR UPDATE` vs `SKIP LOCKED`? | `FOR UPDATE` blocks concurrent access to selected rows; `SKIP LOCKED` lets queue consumers claim disjoint batches. | §4 |
| Q13 | Why must outbox claim, publish, and mark share one transaction? | Row locks are transaction-scoped; one transaction keeps the claim exclusive and marks `published_at` only after a confirm. | §4 |
| Q14 | Why lock tenant before task? | A consistent lock order prevents ABBA deadlocks across the API and consumer paths. | §4 |
| Q15 | What isolation level does ControlPlane need? | Read Committed, because unique indexes and conditional updates enforce the invariants; Serializable would add `40001` retries. | §4 |
| Q16 | What is write skew? | Two transactions each read a condition, then write so the condition is jointly invalidated; ControlPlane avoids it with direct conflicts and a partial unique index. | §4 |
| Q17 | Why UUIDv7 for rows but UUIDv5 for messages? | UUIDv7 is time-ordered for index locality; UUIDv5 is deterministic so retries produce the same dedup key. | §4 |
| Q18 | What should `/healthz` check? | Readiness can check Postgres and RabbitMQ; liveness should be shallow so a dependency outage does not restart every pod. | §1 |
| Q19 | How do you bound a request body? | Wrap it with `http.MaxBytesReader` before decoding, set `MaxHeaderBytes`, and add a proxy limit. | §1 |
| Q20 | What status for a database connection reset? | 500 `internal_error`, or 503 with `Retry-After` under a clear dependency policy; log the cause without leaking the driver error. | §1 |

---

## 7. ControlPlane tie-in

**Description.** This table maps each Day 3 decision to the file that enforces it: server timeouts and shutdown, request IDs, the error-code map, 201/202 semantics, conditional updates, unique-violation mapping, the `DB` interface, keyset pagination, and the outbox/inbox statements. The recurring point is that the handler never does read-then-write for a race; the database is the arbiter and the service owns transaction boundaries.

| Decision | File | Why |
|---|---|---|
| Server timeouts + graceful shutdown | [`cmd/controlplane/main.go`](../../Avid/ControlPlane/cmd/controlplane/main.go) | 5 s read-header, 10 s shutdown; one cancellation tree. |
| Request ID + recoverer | [`internal/httpapi/router.go`](../../Avid/ControlPlane/internal/httpapi/router.go) | Correlate errors; a panic becomes 500. |
| Error code → status map | [`internal/httpapi/errors.go`](../../Avid/ControlPlane/internal/httpapi/errors.go) | One table; unknown errors become `internal_error`. |
| 201/202 semantics | [`internal/httpapi/tenant_handlers.go`](../../Avid/ControlPlane/internal/httpapi/tenant_handlers.go) | Create is synchronous; PATCH/DELETE start async work. |
| Conditional UPDATE + disambiguation | [`internal/service/tenant.go`](../../Avid/ControlPlane/internal/service/tenant.go) | One atomic statement, then explain zero rows. |
| Unique violation mapping | [`internal/store/pool.go`](../../Avid/ControlPlane/internal/store/pool.go) | `23505` → `tenant_already_exists`. |
| `DB` interface + `WithTx` | [`internal/store/pool.go`](../../Avid/ControlPlane/internal/store/pool.go) | Transaction boundary in service. |
| Keyset pagination | [`internal/store/tenant.go`](../../Avid/ControlPlane/internal/store/tenant.go) | `id > cursor`, no OFFSET. |
| Outbox claim SQL | [`internal/store/outbox.go`](../../Avid/ControlPlane/internal/store/outbox.go) | `FOR UPDATE SKIP LOCKED` inside the relay transaction. |
| Inbox insert | [`internal/store/inbox.go`](../../Avid/ControlPlane/internal/store/inbox.go) | `ON CONFLICT DO NOTHING` + row lock order. |

**Say this in the interview:** "The API never does read-then-write for a race. Create relies on the
unique index; update/delete use one conditional UPDATE with `version` and status; when zero rows come
back, the service re-reads only to explain why. The transaction boundary is in the service, and the
store takes a `DB` interface so the same query works on a pool or a transaction."

---

## 8. End-of-day checkpoint

- [ ] Draw `POST /v1/tenants` from HTTP to broker to consumer.
- [ ] Explain 201 vs 202, 400 vs 422 vs 409.
- [ ] Write the conditional UPDATE and the zero-row disambiguation order.
- [ ] Explain why the unique index wins the slug race.
- [ ] Explain `FOR UPDATE`, `SKIP LOCKED`, transaction-scoped locks, lock order.
- [ ] Explain Read Committed vs Serializable and when you would need retries.
- [ ] Write keyset pagination and the matching index.
- [ ] Size a pool and name the tradeoff.

**If you only remember five things:** DB constraint as arbiter, conditional UPDATE, transaction-owned
service, lock order, keyset pagination.

**Next:** [`day-4-messaging-controlplane.md`](day-4-messaging-controlplane.md).
