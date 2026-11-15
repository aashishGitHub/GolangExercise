# Go service patterns — HTTP, pgx, outbox, consumer, tests

Reference for Days 3–4. These are the shapes ControlPlane actually uses, with the production hardening
called out where the demo stops short.

> Snippets are illustrative, not a single package. The source of truth is
> [`ControlPlane`](../../../Avid/ControlPlane) — cite the file and function when you use a pattern in
> the interview.

---

## 1. Layering and dependency direction

**Description.** The dependency graph points inward: `httpapi` depends on `service`, `service` depends on `domain` and `store`, and `domain` depends on nothing infrastructural. `store` functions accept a `DB` so they can run on a pool or inside a caller-owned transaction, while the service owns the transaction boundary. The worker has no database access and is treated as untrusted.

```text
cmd/controlplane        wiring, config, signal handling
cmd/worker              worker simulator wiring
        │
        ▼
internal/httpapi        HTTP: decode, validate, map errors, status codes
        │
        ▼
internal/service        use cases; owns transaction boundaries
        │
        ├── internal/domain      pure rules: states, ranks, slug, errors, timestamp
        ├── internal/store       SQL; takes a DB (pool or tx), never decides tx boundaries
        ├── internal/outbox      relay: claim → publish → mark
        ├── internal/messaging   AMQP: topology, publisher confirms, consumer, reconnect
        └── internal/worker      simulator: consume task, publish progress
```

Rules worth saying aloud:

- `domain` imports no infrastructure. It is testable without Postgres or RabbitMQ.
- `store` functions take a `DB`, so the same function runs on a pool or inside a caller-owned
  transaction. The **service** decides the transaction boundary.
- `httpapi` does not import AMQP. It depends on a small `BrokerHealth` interface for `/healthz`.
- `worker` has no DB. It is untrusted; the consumer is the authority on task/tenant state.

The dependency rule is not decoration: it is why the correctness model can be tested and why the worker
cannot corrupt state directly.

---

## 2. HTTP server — timeouts and shutdown

**Description.** A production `http.Server` sets distinct timeouts for header reads, full request reads, response writes, and idle keep-alive connections. Each one protects a different resource, and `WriteTimeout` must be disabled or handled per-route for streaming. A socket timeout is not a query timeout, so the request context must still reach pgx.

```go
func NewServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,  // slowloris
		ReadTimeout:       15 * time.Second, // headers + body
		WriteTimeout:      30 * time.Second, // set 0 for SSE/streaming
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}
```

What each timeout prevents:

| Field | Prevents | Trap |
|---|---|---|
| `ReadHeaderTimeout` | Slowloris holding connections | Too low breaks slow mobile clients |
| `ReadTimeout` | Slow request bodies | Includes body upload; streaming uploads need care |
| `WriteTimeout` | Slow readers pinning handlers | Kills SSE/long-poll; use `0` or a stream-specific policy |
| `IdleTimeout` | Idle keep-alive connections | Must be longer than the client's reuse window |
| `MaxHeaderBytes` | Header floods | Proxies may add large headers |

A socket timeout is not a query timeout. Pass `r.Context()` to pgx and set a statement timeout (or a
per-query `context.WithTimeout`) so a canceled request actually cancels the database work.

**Graceful shutdown:** `signal.NotifyContext` → `srv.Shutdown(ctx)` with a deadline → cancel background
workers → close the pool/broker. See
[`examples/go-concurrency.md`](go-concurrency.md#8-graceful-shutdown--ordered-deadline-bound).

---

## 3. Middleware order

**Description.** Middleware runs outside-in, so order determines what sees a panic, what runs before body decoding, and what can reject an unauthenticated request cheaply. Request ID and recoverer belong near the outside, body limits before JSON decoding, and auth/rate limits before database work. `RealIP` is safe only behind a trusted proxy.

```go
r := chi.NewRouter()

r.Use(middleware.RequestID)   // outermost: every log/error can carry it
r.Use(middleware.Recoverer)   // panic -> 500, process survives
r.Use(requestLogger)          // method, path, status, duration, request_id
r.Use(middleware.RealIP)      // only behind a trusted proxy
r.Use(bodyLimit(1 << 20))     // reject huge bodies before decoding
r.Use(auth)                   // reject before expensive work
r.Use(rateLimit)              // per-principal, before DB
r.Use(timeout(10 * time.Second))
```

Order is a security and performance decision:

- Request ID first so the recoverer and logger can use it.
- Recoverer outside handlers, inside the logger if you want the 500 logged with a request ID.
- Body limit before JSON decode; auth/rate limit before DB work.
- Timeout around business work, but let health checks use a shorter, separate deadline.
- `RealIP` only if a trusted proxy overwrites `X-Forwarded-For`; otherwise clients spoof it.

Chi's `middleware.Timeout` cancels the request context; it does not automatically produce a clean
timeout body for every handler. If the contract requires `504`, write that middleware explicitly and
make handlers observe `ctx.Done()`.

---

## 4. JSON decoding — strict, bounded, and honest about errors

**Description.** Decode into a typed struct, cap the body with `http.MaxBytesReader`, reject trailing JSON, and map decode failures to precise codes: malformed JSON is 400, an oversized body is 413, and well-formed but invalid business input is 422. `DisallowUnknownFields` is a compatibility tradeoff, not a universal default.

```go
const maxBody = 1 << 20 // 1 MiB

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields() // strict internal contract; see compatibility note

	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxErr):
			return domain.NewError(domain.ErrRequestTooLarge, "request body is too large")
		case errors.Is(err, io.EOF):
			return domain.NewError(domain.ErrMalformedRequest, "request body is required")
		default:
			return domain.NewError(domain.ErrMalformedRequest, "request body is not valid JSON")
		}
	}

	// Reject trailing JSON, e.g. {"slug":"a"}{"slug":"b"}.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return domain.NewError(domain.ErrMalformedRequest, "body must contain a single JSON object")
	}
	return nil
}
```

Map errors precisely:

| Cause | Status | Code |
|---|---|---|
| Invalid JSON / wrong type / empty body | 400 | `malformed_request` |
| Body over limit | 413 | `request_too_large` |
| Well-formed but blank name / bad slug / missing version | 422 | `validation_error` with `details` |
| Duplicate slug / stale version / wrong status | 409 | domain conflict code |
| Unknown route | 404 | `not_found` |
| Unmapped store/driver error | 500 | `internal_error`, log the cause |

`DisallowUnknownFields` is a compatibility decision. It catches typos in an internal contract, but it
breaks clients that add a field before the server deploys. For a public API, prefer ignoring unknown
fields and versioning the contract. Say which choice you made and why.

**ControlPlane gap to name:** its `decodeJSON` in `internal/httpapi/tenant_handlers.go` does not cap the
body or reject trailing JSON. It is a demo; production hardening would add both.

---

## 5. Domain errors and the HTTP envelope

**Description.** A domain error carries a stable code and a human message, independent of HTTP. The HTTP boundary maps that code to a status and emits a stable envelope with `code`, `message`, `details`, and `request_id`; unknown errors become 500 with the cause logged, never leaked. This keeps the domain package free of transport concerns.

```go
type ErrorCode string

const (
	ErrValidation            ErrorCode = "validation_error"
	ErrMalformedRequest      ErrorCode = "malformed_request"
	ErrTenantNotFound        ErrorCode = "tenant_not_found"
	ErrTaskNotFound          ErrorCode = "task_not_found"
	ErrTenantAlreadyExists   ErrorCode = "tenant_already_exists"
	ErrTenantUpdateNotAllowed ErrorCode = "tenant_update_not_allowed"
	ErrTenantVersionConflict ErrorCode = "tenant_version_conflict"
	ErrRequestTooLarge       ErrorCode = "request_too_large" // extension; not in the current build
	ErrInternal              ErrorCode = "internal_error"
)

type Error struct {
	Code    ErrorCode
	Message string
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

func NewError(code ErrorCode, message string) *Error {
	return &Error{Code: code, Message: message}
}
```

Why a typed domain error instead of `fmt.Errorf("not found")`:

- The HTTP layer maps **codes**, not strings.
- The consumer classifies **permanent vs transient** by code.
- Tests assert identity with `errors.As`, not message matching.
- The client gets a stable machine-readable code; support gets a `request_id`.

Mapping:

```go
var errorStatus = map[domain.ErrorCode]int{
	domain.ErrTenantNotFound:          http.StatusNotFound,
	domain.ErrTaskNotFound:            http.StatusNotFound,
	domain.ErrTenantAlreadyExists:     http.StatusConflict,
	domain.ErrTenantUpdateNotAllowed:  http.StatusConflict,
	domain.ErrTenantVersionConflict:   http.StatusConflict,
	domain.ErrValidation:              http.StatusUnprocessableEntity,
	domain.ErrMalformedRequest:        http.StatusBadRequest,
	domain.ErrRequestTooLarge:         http.StatusRequestEntityTooLarge,
	domain.ErrInternal:                http.StatusInternalServerError,
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var derr *domain.Error
	if !errors.As(err, &derr) {
		slog.ErrorContext(r.Context(), "unmapped error", "error", err)
		derr = domain.NewError(domain.ErrInternal, "internal error")
	}
	status, ok := errorStatus[derr.Code]
	if !ok {
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, errorEnvelope{Error: errorBody{
		Code:      derr.Code,
		Message:   derr.Message,
		RequestID: middleware.GetReqID(r.Context()),
	}})
}
```

ControlPlane's actual error set (in `internal/domain/errors.go`) stops at `internal_error`;
`request_too_large` is an extension shown to make the 413 path concrete.

The envelope:

```json
{
  "error": {
    "code": "tenant_version_conflict",
    "message": "tenant version conflict",
    "request_id": "req-123"
  }
}
```

Never return SQLSTATE, table names, or driver text to the client. Log the real cause; return a stable
code.

---

## 6. pgx pool, `DB`, and `WithTx`

**Description.** One shared `pgxpool.Pool` serves the process, and a small `DB` interface lets store functions run on either the pool or a `pgx.Tx`. `WithTx` begins, defers rollback, runs the callback, and commits; the service decides the boundary. The store never commits or opens its own transaction.

```go
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
```

`*pgxpool.Pool` and `pgx.Tx` both satisfy it. A store function takes `DB` and never begins/commits; the
service composes the use case:

```go
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(DB) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after commit

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
```

Why the service owns the boundary:

- Create tenant + create task must commit together.
- Apply inbox + task status + tenant outcome must commit together.
- A store method that opens its own transaction cannot compose with those.

Pool configuration:

```go
cfg, err := pgxpool.ParseConfig(databaseURL)
if err != nil {
	return nil, err
}
cfg.MaxConns = 10
cfg.MinConns = 2
cfg.MaxConnLifetime = time.Hour
cfg.MaxConnIdleTime = 30 * time.Minute
cfg.HealthCheckPeriod = time.Minute
cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5000" // 5s server-side cap

pool, err := pgxpool.NewWithConfig(ctx, cfg)
if err != nil {
	return nil, err
}
if err := pool.Ping(ctx); err != nil {
	pool.Close()
	return nil, err
}
```

Sizing: `max_connections` is shared by every API replica, worker, migration, and admin session. Little's
Law (`concurrency ≈ throughput × latency`) is a starting point, not a license to set 100. More
connections increase contention and memory. Use PgBouncer if connection count is the bottleneck.

---

## 7. Unique violation — the create-slug arbiter

**Description.** Insert the row and let the unique index arbitrate concurrent creates; one transaction commits and the other gets SQLSTATE `23505`. Detect the violation with `errors.As` on `*pgconn.PgError` and map it to a domain conflict. Do not pre-check with a `SELECT`, which adds a read-then-write race and a false sense of safety.

```go
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
```

```go
err := store.WithTx(ctx, pool, func(db store.DB) error {
	if err := store.CreateTenant(ctx, db, tenant); err != nil {
		return err
	}
	return store.CreateTask(ctx, db, task)
})
if err != nil {
	if store.IsUniqueViolation(err) {
		return store.Tenant{}, store.Task{}, domain.NewError(
			domain.ErrTenantAlreadyExists, "a tenant with this slug already exists")
	}
	return store.Tenant{}, store.Task{}, err
}
```

Why not:

```go
// BAD: TOCTOU. Two requests both see no row, both INSERT; one gets a 500.
existing, _ := store.GetTenantBySlug(ctx, pool, slug)
if existing != nil { return conflict }
```

The unique index is evaluated atomically at INSERT. It works across processes and replicas; a Go mutex
does not.

If one table has multiple unique constraints, use `pgErr.ConstraintName` to map the right domain error.
Do not match on the human-readable message.

---

## 8. Conditional UPDATE + disambiguation

**Description.** The PATCH is one atomic `UPDATE` guarded by `id`, `version`, and allowed status, with `version = version + 1` so the next stale write fails. Zero rows has three possible causes, so the service re-reads after the failed update and maps them in order: 404 missing, 409 wrong status, 409 stale version. This avoids a read-then-write race and needs no row lock.

The PATCH mechanism is one statement:

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

`pgx.ErrNoRows` means zero rows matched. That single result has three possible causes, so the service
re-reads **after** the failed update:

```go
if !matched {
	return disambiguateNoMatch(ctx, db, id, version, domain.CanUpdateTenant, domain.ErrTenantUpdateNotAllowed)
}
```

```go
func disambiguateNoMatch(...) error {
	current, err := store.GetTenantByID(ctx, db, id)
	if err == store.ErrNoRows {
		return domain.NewError(domain.ErrTenantNotFound, "tenant not found")
	}
	if err != nil {
		return err
	}
	if !canApply(current.Status) {
		return domain.NewError(notAllowedCode, "tenant status does not allow this operation")
	}
	return domain.NewError(domain.ErrTenantVersionConflict, "tenant version conflict")
}
```

Decision order:

1. Row missing → 404 `tenant_not_found`.
2. Status not allowed → 409 `tenant_update_not_allowed`.
3. Status allowed, so the version must differ → 409 `tenant_version_conflict`.

Why one statement instead of `SELECT ... FOR UPDATE` then `UPDATE`:

- One round trip, one atomic check; no read-then-write window.
- Concurrent PATCHes: one matches, the rest see the new version/status and match zero.
- In a 50-way race, losers usually see `updating` and get `tenant_update_not_allowed` — that is correct,
  not a bug in the status mapping. A stale version against an idle `active` tenant gets
  `tenant_version_conflict`.

Why `version = version + 1` matters: a client holding the old token must fail its next write. It also
gives the async terminal outcome a visible completion signal.

**Delete** uses the same shape with `status IN ('active','failed')` and `status = 'destroying'`. A
failed tenant can still be torn down.

---

## 9. Keyset pagination

**Description.** Keyset pagination uses the last seen `id` as an opaque cursor: `WHERE id > $cursor ORDER BY id LIMIT $n`. With UUIDv7 the ID is time-ordered, so the primary-key index serves the query and there is no `OFFSET` scan or page drift. Add `(status, id)` when filtering by status, and validate the cursor according to the API's strictness policy.

```sql
SELECT id, slug, name, status, version, created_at, updated_at
FROM tenants
WHERE id > $1
  AND ($2 = '' OR status = $2)
ORDER BY id
LIMIT $3;
```

- `id` is UUIDv7, so `id > cursor` is time-ordered.
- No `OFFSET`: no scanning/discarding rows, no page drift when rows are inserted/deleted.
- The cursor is opaque to the client (the last ID on the page).
- Return `next_cursor` only when the page is full; a short page means the end.

```go
func nextCursor(lastID uuid.UUID, pageLen, limit int) string {
	if pageLen < limit {
		return ""
	}
	return lastID.String()
}
```

Indexes:

```sql
-- All tenants, ordered by id: the primary key index.
-- Filter by status, ordered by id:
CREATE INDEX IF NOT EXISTS tenants_status_id_idx ON tenants (status, id);
```

Verify with:

```sql
EXPLAIN (ANALYZE, BUFFERS)
SELECT ... FROM tenants
WHERE status = 'active' AND id > '...'
ORDER BY id
LIMIT 20;
```

Look for an index scan, not a sequential scan + sort. The planner may still choose a sequential scan on
a tiny table; that is fine. At scale, check.

**Cursor validation is a product decision.** ControlPlane's `parseCursor` treats an invalid cursor as
"start from the beginning" rather than a 400. That is forgiving but can hide a client bug. A stricter
API returns `400 invalid_cursor`. State the choice.

`next_cursor` on the task list is declared in the wire type but the current `handleListTenantTasks` does
not set it because a tenant has only a handful of tasks. Do not claim it paginates.

---

## 10. Transactional outbox — claim, publish, mark

**Description.** The task row is the outbox: `published_at IS NULL` marks work that has not reached the broker. The relay claims rows with `FOR UPDATE SKIP LOCKED`, publishes with confirms and `mandatory`, marks `published_at`, and commits the claim, publish, and mark in one transaction. A crash after confirm before commit republishes on restart, so delivery is at-least-once and the consumer must deduplicate.

The task row is the outbox:

```sql
SELECT t.id, t.tenant_id, t.type, t.status, t.created_at, t.updated_at,
       tn.slug, tn.name
FROM tasks t
JOIN tenants tn ON tn.id = t.tenant_id
WHERE t.published_at IS NULL
ORDER BY t.id
LIMIT $1
FOR UPDATE OF t SKIP LOCKED;
```

```go
err := store.WithTx(ctx, pool, func(db store.DB) error {
	tasks, err := store.ClaimUnpublishedTasks(ctx, db, batchSize)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if err := publisher.PublishTask(ctx, toMsg(task)); err != nil {
			return err
		}
		if err := store.MarkTaskPublished(ctx, db, task.ID); err != nil {
			return err
		}
	}
	return nil
})
```

Why the transaction stays open across broker I/O:

- `FOR UPDATE` locks are released at transaction end. Close the transaction after the claim and another
  relay replica can claim and publish the same rows.
- Mark only after the broker confirms; a crash between confirm and mark causes a duplicate publish on
  restart, not a lost event.
- The cost is lock duration and throughput while the broker is slow. Bound the batch, set publish
  timeouts, monitor oldest-unpublished age and lock waits. At high scale, consider batching confirms or
  a different relay design.

Publisher shape:

```go
ch.Confirm(false)
confirms := ch.NotifyPublish(make(chan amqp.Confirmation, 1))
returns := ch.NotifyReturn(make(chan amqp.Return, 1))

err := ch.PublishWithContext(ctx, exchange, routingKey, true /* mandatory */, false, amqp.Publishing{
	ContentType:  "application/json",
	DeliveryMode: amqp.Persistent,
	Body:         body,
})
// wait for basic.return (unroutable) or basic.ack (accepted)
```

`mandatory=true` matters because a confirm can acknowledge a message that matched no queue. The return
is the routing failure signal.

Crash matrix:

| Crash point | Result | Recovery |
|---|---|---|
| Before API transaction commits | No tenant/task | Client retries; nothing to reconcile |
| After commit, before relay claim | Row is `published_at IS NULL` | Poll/notify claims it |
| After claim, before publish | Transaction rolls back; row stays unpublished | Next relay run republishes |
| After publish, before confirm | Row stays unpublished (confirm not observed) | Duplicate publish on retry |
| After confirm, before mark | Row stays unpublished | Duplicate publish; inbox dedups |
| After mark, before commit | Mark rolls back | Duplicate publish |
| After commit | Published once | Normal |

Never mark before publish. That is the lost-event direction.

---

## 11. Idempotent consumer — inbox first, then rank guard

**Description.** The consumer inserts the deterministic `message_id` into an inbox table with `ON CONFLICT DO NOTHING`, then loads and locks tenant then task, applies the rank guard, and updates task and tenant in one transaction. The inbox deduplicates the same logical message; the rank guard rejects different messages that arrive out of order. Both are needed, and the ack happens only after commit.

```go
func (s *Service) ApplyTaskUpdate(ctx context.Context, u TaskUpdate) (ApplyResult, error) {
	result := ApplyStale

	err := store.WithTx(ctx, s.pool, func(db store.DB) error {
		inserted, err := store.InsertProcessedMessage(ctx, db, u.MessageID, u.TaskID)
		if err != nil {
			return err
		}
		if !inserted {
			result = ApplyDuplicate // already applied; ack as no-op
			return nil
		}

		task, err := store.GetTaskByID(ctx, db, u.TaskID)
		if err == store.ErrNoRows {
			return domain.NewError(domain.ErrTaskNotFound, "task not found")
		}
		if err != nil {
			return err
		}

		// Lock order: tenant -> task, matching the API path.
		if _, err := store.GetTenantForUpdate(ctx, db, task.TenantID); err == store.ErrNoRows {
			return domain.NewError(domain.ErrTenantNotFound, "tenant not found")
		} else if err != nil {
			return err
		}
		locked, err := store.GetTaskForUpdate(ctx, db, u.TaskID)
		if err == store.ErrNoRows {
			return domain.NewError(domain.ErrTaskNotFound, "task not found")
		}
		if err != nil {
			return err
		}

		if domain.IsTaskTerminal(locked.Status) ||
			!domain.TaskUpdateAllowed(locked.Status, u.Status) {
			result = ApplyStale
			return nil
		}

		if err := store.UpdateTaskStatus(ctx, db, locked.ID, u.Status, reasonFor(u)); err != nil {
			return err
		}

		if domain.IsTaskTerminal(u.Status) {
			if expected, next, ok := domain.TenantOutcome(locked.Type, u.Status); ok {
				if _, err := store.UpdateTenantStatusGuarded(
					ctx, db, locked.TenantID, expected, next); err != nil {
					return err
				}
			}
		}

		result = ApplyApplied
		return nil
	})
	if err != nil {
		return "", err
	}
	return result, nil
}

// Only a failed task carries a reason. The worker may send error text on other
// statuses; the consumer drops it so the column is meaningful.
func reasonFor(u TaskUpdate) *string {
	if u.Status != domain.TaskFailed {
		return nil
	}
	return u.Reason
}
```

The two mechanisms solve different problems:

| Mechanism | Problem | Key |
|---|---|---|
| Inbox (`processed_messages`) | Same logical message delivered twice | `message_id` primary key + `ON CONFLICT DO NOTHING` |
| Rank guard | Different messages arriving out of order | `rank(next) > rank(current)` |

Rank:

```text
accepted(0) → in_progress(1) → {done, failed}(2)
```

Consequences:

- Duplicate terminal: `2 > 2` false → stale, no re-apply.
- Late `in_progress` after `done`: `1 > 2` false → stale.
- `accepted → done` if `in_progress` was lost: `2 > 0` true → apply. The first terminal wins.
- Unknown status: rejected during parsing before the map lookup; otherwise rank 0 would look like
  `accepted` and be silently acked.

Why insert the inbox row first: it serializes concurrent duplicates on the primary key. The second
delivery blocks until the first transaction ends, then sees zero rows. If inserted after the state
change, both deliveries could do state work before discovering the duplicate.

Why lock tenant then task: the API path also touches tenant before task. A consistent order prevents
ABBA deadlocks. The consumer's `GetTenantForUpdate` also stabilizes the expected-status guard while the
terminal outcome is applied.

The message's `tenant_id` is **not trusted**. The consumer loads the tenant from `task.tenant_id` and
ignores the claimed ID. That is a blast-radius defense.

---

## 12. Consumer ack policy and failure classification

**Description.** Ack only after the database transaction commits; a lost ack produces a redelivery that the inbox makes harmless. Classify permanent failures (malformed JSON, unknown task, bad schema) as poison and dead-letter immediately, and transient failures (connection reset, timeout, pool exhaustion) as bounded retries. The broker's delivery limit is the outer bound.

```go
if err := json.Unmarshal(delivery.Body, &wire); err != nil {
	nack(delivery, false) // poison
	return
}
update, err := wire.Parse()
if err != nil {
	nack(delivery, false) // invalid schema/ID/status: poison
	return
}

err = withTransientRetry(ctx, func() error { return handle(ctx, update) })
if err != nil {
	if isPermanent(err) {
		nack(delivery, false) // DLQ
		return
	}
	nack(delivery, true) // transient: requeue, delivery limit caps it
	return
}
if err := delivery.Ack(false); err != nil {
	log.Error("ack failed", "error", err)
	return
}
```

Classification:

| Class | Examples | Action |
|---|---|---|
| Poison/permanent | malformed JSON, unknown schema version, unknown status, task/tenant missing, recovered panic | `Nack(requeue=false)` → DLX/DLQ |
| Transient | connection reset, timeout, pool exhaustion, deadlock (`40P01`) | bounded in-process retry, then requeue |
| Success/no-op | applied, duplicate, stale | `Ack` |

Queue settings:

- `cp.task-updates`: quorum, `x-delivery-limit=5`, DLX to `cp.task-updates.dlq`.
- `worker.tasks`: quorum, **no DLQ/delivery limit** in this build — a known hardening gap. A malformed
  task is dropped with `Nack(false)`.
- Prefetch: consumer 10, worker 1.
- Consumer processes sequentially. That bounds unacked deliveries and keeps failure classification
  simple. Parallel consumers need per-tenant ordering and bounded concurrency.

Ack after the handler's transaction commits. If the ack is lost, the broker redelivers; the inbox makes
the replay a no-op. Acking before commit can lose the update.

---

## 13. Table-driven tests

**Description.** Table-driven tests put inputs and expected outputs in a slice of structs and run each case as a named subtest. The examples cover domain transitions, pagination helpers, a real-Postgres race test, and the handler shape. Integration tests should use real Postgres or RabbitMQ when the behavior being proved lives in the database or broker, not in a mock.

### Domain transition table

```go
func TestTaskUpdateAllowed(t *testing.T) {
	tests := []struct {
		name string
		from domain.TaskStatus
		to   domain.TaskStatus
		want bool
	}{
		{"accepted to in_progress", domain.TaskAccepted, domain.TaskInProgress, true},
		{"accepted to done", domain.TaskAccepted, domain.TaskDone, true},
		{"accepted to failed", domain.TaskAccepted, domain.TaskFailed, true},
		{"in_progress to done", domain.TaskInProgress, domain.TaskDone, true},
		{"in_progress to failed", domain.TaskInProgress, domain.TaskFailed, true},
		{"done repeats", domain.TaskDone, domain.TaskDone, false},
		{"done to failed", domain.TaskDone, domain.TaskFailed, false},
		{"failed to done", domain.TaskFailed, domain.TaskDone, false},
		{"late in_progress", domain.TaskDone, domain.TaskInProgress, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := domain.TaskUpdateAllowed(tt.from, tt.to); got != tt.want {
				t.Fatalf("TaskUpdateAllowed(%q, %q) = %v, want %v",
					tt.from, tt.to, got, tt.want)
			}
		})
	}
}
```

This table encodes the invariant, not the implementation. If someone replaces the rank map with a
transition table, the test still passes.

### Pagination helpers

```go
func TestNextCursor(t *testing.T) {
	id := uuid.MustParse("018f0000-0000-7000-8000-000000000001")
	tests := []struct {
		name    string
		pageLen int
		limit   int
		want    string
	}{
		{"short page ends", 3, 20, ""},
		{"empty page ends", 0, 20, ""},
		{"full page continues", 20, 20, id.String()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextCursor(id, tt.pageLen, tt.limit); got != tt.want {
				t.Fatalf("nextCursor = %q, want %q", got, tt.want)
			}
		})
	}
}
```

### Real-Postgres race test

Mocks cannot prove a unique-index race or a row-lock race. Use the real database:

```go
func TestCreateTenantSlugRace(t *testing.T) {
	pool := testPool(t) // dedicated controlplane_test DB, not the compose DB
	truncateAll(t, pool)
	svc := service.New(pool)

	const n = 50
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes, conflicts := 0, 0

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := svc.CreateTenant(context.Background(), "race-slug", "Race Tenant")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				successes++
			case isCode(err, domain.ErrTenantAlreadyExists):
				conflicts++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	require.Equal(t, 1, successes)
	require.Equal(t, n-1, conflicts)
}
```

Rules for this test:

- Separate test DB and RabbitMQ vhost from the dev compose stack.
- Serialize packages with a Postgres advisory lock if they share the test DB.
- Truncate between tests; do not rely on unique random slugs alone for isolation.
- Use `t.Errorf` from goroutines only if you guard the test state; `require` is not goroutine-safe for
  the parent `t`. Collect errors and assert after `Wait`.
- Run with `-race` and `-count=1` in CI; repeat the race tests with `-count=20` locally.

### Handler test shape

The current `httpapi.New` takes a concrete `*service.Service`, so handlers are not unit-testable without
a database. The improvement is a small consumer-side interface:

```go
type tenantService interface {
	CreateTenant(ctx context.Context, slug, name string) (store.Tenant, store.Task, error)
	GetTenant(ctx context.Context, id uuid.UUID) (store.Tenant, error)
}
```

Then:

```go
func TestCreateTenantValidation(t *testing.T) {
	h := newHandlerWithService(fakeTenantService{})
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants", strings.NewReader(`{"slug":"BAD"}`))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}
```

Name the gap honestly: ControlPlane has domain, service, outbox, messaging, and lifecycle tests; it does
not have dedicated HTTP handler tests. The lifecycle test exercises the service path, not the router.

---

## 14. SQL drills

### Explain the claim

```sql
EXPLAIN (ANALYZE, BUFFERS)
SELECT t.id
FROM tasks t
JOIN tenants tn ON tn.id = t.tenant_id
WHERE t.published_at IS NULL
ORDER BY t.id
LIMIT 100
FOR UPDATE OF t SKIP LOCKED;
```

Look for the partial index `tasks_unpublished_idx ON tasks (id) WHERE published_at IS NULL`. If the
planner chooses a sequential scan on a large table, investigate.

### Check the keyset index

```sql
EXPLAIN (ANALYZE, BUFFERS)
SELECT id, slug, name, status, version, created_at, updated_at
FROM tenants
WHERE status = 'active' AND id > '018f...'
ORDER BY id
LIMIT 20;
```

Expected: `Index Scan` or `Index Only Scan` on `(status, id)`, no `Sort`. On a tiny table, a seq scan is
acceptable.

### Prove the lock order

Open two sessions:

```sql
-- Session A
BEGIN;
SELECT * FROM tenants WHERE id = $1 FOR UPDATE;
SELECT * FROM tasks WHERE id = $2 FOR UPDATE;

-- Session B (same order, different rows)
BEGIN;
SELECT * FROM tenants WHERE id = $3 FOR UPDATE;
SELECT * FROM tasks WHERE id = $4 FOR UPDATE;
```

No deadlock. Reverse the order in one session and you can create an ABBA deadlock. ControlPlane uses
tenant → task everywhere.

### Find the open task

```sql
SELECT tenant_id, count(*)
FROM tasks
WHERE status IN ('accepted','in_progress')
GROUP BY tenant_id
HAVING count(*) > 1;
```

This should return zero rows because of `tasks_one_open_per_tenant`. Run it after a race test.

### Check version monotonicity

```sql
SELECT id, version, status, updated_at
FROM tenants
ORDER BY updated_at DESC
LIMIT 20;
```

Version should only move forward, by one per mutation. A jump larger than one in a single request is a
bug; multiple lifecycle events can move it multiple times over time.

---

## 15. Endpoint pre-flight — ten questions

Before shipping any endpoint, answer these:

1. What is the resource and the verb? Is it safe/idempotent?
2. Which status codes, and what does each mean to the client?
3. What is the success body? Does it include the state the client needs next?
4. What validation is syntactic (400) vs business (422)?
5. What conflicts exist (409), and how are they disambiguated?
6. What is the transaction boundary? Which rows lock, in what order?
7. What happens on retry? Idempotency key? Optimistic version?
8. What is the pagination contract and index?
9. What is logged, what is returned, and what is never leaked?
10. What is the timeout, the body limit, and the auth/rate-limit policy?

If you can answer these in 60 seconds, you can defend the endpoint.

---

## 16. End-of-day checkpoint

- [ ] Write the `DB`/`WithTx` pattern and explain why the service owns the boundary.
- [ ] Write the conditional UPDATE and the three-way disambiguation.
- [ ] Explain why a unique index beats a pre-check SELECT.
- [ ] Write the keyset query and name the index.
- [ ] Explain claim + publish + mark in one transaction, including the crash windows.
- [ ] Explain inbox-first and the rank guard with three examples.
- [ ] Classify poison vs transient and name the ack action for each.
- [ ] Write one domain table test and one real-Postgres race test.

**If you only remember three things:** the database is the arbiter, the service owns the transaction
boundary, and every async failure is either poison or transient — never "retry forever".
