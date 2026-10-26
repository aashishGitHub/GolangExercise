# FieldSync GraphQL read API

Read-only GraphQL over `disaster_locations -> site_assessments -> photos` plus `location_stats`.
Code: `internal/graph/`, entrypoint `cmd/graphql/main.go`, schema `internal/graph/schema.graphqls`.

## Decisions

| Decision | Choice | Why / trade-off |
|---|---|---|
| Library | gqlgen v0.17.95 (`go list -m -versions` shows it as the latest; its go.mod needs Go 1.26, same as this repo) | Schema-first codegen: the `.graphqls` file is the contract and the compiler checks resolvers against it. Cost: a 4k-line `generated.go` nobody reviews, and a codegen step (`go generate ./internal/graph`). graphql-go/graphql was not tried: code-first, no codegen, but schema and resolvers drift without compiler help. |
| Codegen runs via | `go tool gqlgen` (Go 1.24+ `tool` directive in go.mod) | No global install. Pins the generator version next to the runtime version. |
| Mutations / subscriptions | None | Writes already have one audited path: `POST /api/sync` (LWW upserts + transactional outbox in one tx). A second write path would bypass the outbox and the `domain_events` audit log. Subscriptions are already served by the WebSocket notifier; GraphQL adds nothing there. |
| Transport | POST only | No GET: avoids cacheable/CSRF-able queries and a second code path. Server-to-server callers do not need CORS. |
| DataLoader | ~100 lines written by hand (`dataloader.go`) | You can defend every line. gqlgen's own docs recommend `vikstrous/dataloadgen`; use that in production if you do not want to own the code. gqlgen v0.17.95 also has built-in batch resolvers (`@goField(batch: true)`, in its `docs/content/reference/resolvers.md`); not used because a loader also deduplicates and caches across different parents in one request. I have not benchmarked either. |
| Batch SQL | Raw pgx in `internal/graph/pgstore.go` | sqlc `Querier` only has per-id methods (the N+1 shape). `internal/db` and `sqlc/queries.sql` are untouched. |

## Control flow and data flow of one nested request

Request: `{ locations(first: 50) { edges { node { id siteAssessments { id photos { id } } } } } }`

```
HTTP POST /graphql  (Authorization: Bearer <Cognito ID token>)
  |
  v
auth.Verifier.Middleware ----- no/bad token --> 401, stops here (store never touched)
  |  puts auth.Claims in ctx
  v
requestScope:  ctx timeout (10s)  +  NewLoaders(store)  <-- one set per request
  |
  v
gqlgen executor
  1. parse            token limit 5000                -> 422 GRAPHQL_PARSE_FAILED
  2. validate         schema rules (cycles, types)    -> 422
  3. queryLimits      depth<=8, expanded fields<=1000 -> 422 QUERY_DEPTH_EXCEEDED / QUERY_TOO_LARGE
  4. complexity       est. rows <= 10000              -> 422 COMPLEXITY_LIMIT_EXCEEDED
  5. execute
       Query.locations ----------------------------> Store.ListLocations        SQL #1 (50+1 rows)
         | 50 Location objects, resolved concurrently (gqlgen goroutine per list element)
         v
       Location.siteAssessments x50 --Load(locID)--> SiteAssessments loader
              all 50 calls land in the same 2ms window / MaxBatch
              -> Store.SiteAssessmentsByLocationIDs(ids[50])   SQL #2  WHERE location_id = ANY($1)
         | 150 SiteAssessment objects
         v
       SiteAssessment.photos x150 --Load(siteID)--> Photos loader
              -> Store.PhotosBySiteAssessmentIDs(ids[150])     SQL #3  WHERE site_assessment_id = ANY($1)
  6. presentError     resolver errors -> "internal error" + path; real error goes to the server log
  v
200 {"data": ..., "errors": [...]}       (limit violations: 422)
```

Data flow per row: `pgx row -> db.* (sqlc structs, RowToStructByPos) -> graph model (models.go) -> JSON`.
Loaders return `db.*` rows grouped by parent id (`groupBy`/`indexBy`); resolvers map to GraphQL models.

## N+1, with the real numbers

Naive resolver: each `Location.siteAssessments` calls the store with that one location id; each
`SiteAssessment.photos` calls it with one assessment id. Query over 50 locations x 3 assessments x 2 photos:

| Variant | ListLocations | Assessments | Photos | Total |
|---|---|---|---|---|
| Naive (`TestNPlusOne_NaiveResolversIssueOnePlusNPlusM`) | 1 | 50 | 150 | **201** = 1 + 50 + 150 |
| DataLoader (`TestNPlusOne_BatchedIssuesConstantNumberOfStoreCalls`) | 1 | 1 | 1 | **3** |
| DataLoader + `stats` selected | 1 | 1 | 1 | 4 (+1 stats batch) |

Real Postgres (`TestIntegration_NestedQueryIssuesConstantNumberOfSQLStatements`, counting wrapper
around the pgx pool): 3 SQL statements for the same query.

Both tests assert the exact numbers. Mutation-checked: setting `MaxBatch` to 1 or making the loader flush on
every key turns the batched test red with `total store calls = 201, want 3`. The naive variant uses the same
resolvers, schema and batch functions; only the `Loader` implementation differs (it lives in the test file).

The REST equivalent has the same shape on the client: `GET /api/locations/{id}` returns the assessments, then
one `GET /api/sites/{id}` per assessment for photos: 1 + N round trips over the network instead of the DB.

Test determinism: a 2ms timer window splits batches under CPU contention (observed: 24 parallel race-enabled
processes on 2 CPUs made the first version of these tests flake). Tests therefore flush each loader when the
expected key count arrives (`MaxBatch`), with a 5s timer only as a safety net. Production uses the timer.
A split batch in production is harmless: 2 queries instead of 1.

## How the DataLoader works

- One `Loaders` set per HTTP request (`requestScope`). Each `DataLoader` has a `pending` batch.
- `Load(key)`: if the key was already requested this request, return the same batch's result (per-request cache).
  Otherwise append it to `pending`. The first key of a batch starts a 2ms timer.
- The batch is sent when the timer fires or `len(keys) >= MaxBatch` (500), whichever comes first. `sent` is
  checked under the mutex so a size flush and the timer cannot both run it.
- gqlgen resolves list elements and object fields in goroutines (`MarshalSliceConcurrently`, `out.Concurrently`
  in `generated.go`), so 50 `Location.siteAssessments` resolvers block in `Load` at about the same time and share a batch.
- The batch function gets all keys, runs one `= ANY($1)` query, returns `map[key]value`. A missing key yields
  the zero value and no error (`location(id)` -> `null`, no stats row -> `stats: null`).
- A fetch error or panic is stored on the batch and returned to every waiter (panic recovered: the timer goroutine
  is outside gqlgen's recovery). The error is cached for the rest of that request.
- The fetch runs with the context of the first caller; all callers share the request context lineage, so
  the request timeout cancels it. `Load` also returns early on its own `ctx.Done()`.

Why per-request and never a global cache: the cache has no invalidation and no user scoping. A process-wide
cache would serve stale rows and, once row-level authorisation exists, leak one user's rows to another.
Per-request gives deduplication within a request and is garbage after it.

## Keyset cursors, not offsets

`first/after/updatedSince`, ordered by `(updated_at, id)`. Cursor = base64url(`<updated_at unix micros>|<uuid>`).
SQL: `WHERE updated_at >= $since AND (updated_at, id) > ($ts, $id) ORDER BY updated_at, id LIMIT first+1`.

- Offset pagination is O(offset) in the database and, under concurrent writes, skips or repeats rows when
  rows are inserted before the offset. Keyset seeks to the position and is stable.
- `id` is the tiebreaker: many rows can share one `updated_at`. Integration test
  `...BreaksTimestampTiesOnID` seeds groups of 5 rows with one timestamp and pages by 7; dropping `id` from the
  comparison lost 15 of 50 rows in a mutation run.
- A row updated mid-walk moves to the end (its `updated_at` grows), so it is delivered again and never skipped:
  at-least-once. The consumer dedups by `id`. Proven by `...RowUpdatedMidWalkReappearsAndIsNeverSkipped`.
- `updatedSince` is inclusive (`>=`): a poller that restarts from the max `updated_at` it saw re-reads boundary
  rows instead of missing ones that share that timestamp. Safe because applying a row twice is idempotent.
- Microsecond precision matches `timestamptz`, so the cursor round trip is lossless.
- Forward-only connection: `hasNextPage` and `endCursor`, no `hasPreviousPage`/`totalCount` (a `COUNT(*)` per
  page is the expensive part of a feed that never needs it).
- Index: migration `000004_locations_updated_at_id_idx` adds `disaster_locations (updated_at, id)`, in the
  same column order as the `ORDER BY` and the row comparison, so one B-tree seek serves both. `CONCURRENTLY`
  so it does not block writes while it builds; that cannot run in a transaction, so it is the only statement
  in the file (`make migrate` applied it and `migrate down 1` reverted it without errors). If it fails midway it leaves an `INVALID`
  index that must be dropped before a retry.
- Measured (`EXPLAIN (ANALYZE, BUFFERS)` of the second-page query on `fieldsync_graphql`, 100k seeded rows inside a
  rolled-back transaction):
  - with the index: `Index Scan using disaster_locations_updated_at_id_idx on disaster_locations (actual time=0.013..0.017 rows=51 loops=1)`,
    both predicates in `Index Cond` (`updated_at >= ...` and `ROW(updated_at, id) > ROW(...)`), 4 shared buffers, 0.037 ms.
  - index dropped in the same transaction: `Parallel Seq Scan` + `top-N heapsort`, `Rows Removed by Filter: 27024`
    per loop (2 loops), 2485 shared buffers, 19.2 ms.
  - with the 50 rows the integration tests leave behind, the planner picks `Seq Scan` + `Sort`: correct for a
    one-page table, so an EXPLAIN on a tiny dev table does not prove the index is used.

## Safety limits (checked cheapest first)

| Limit | Default | Real numbers from the tests |
|---|---|---|
| Parser tokens | 5000 | 1000 repeats of a 7-token selection (7000 tokens) rejected at parse |
| Depth | 8 | deepest legit query (`locations.edges.node.siteAssessments.location.stats.goodCount`) is 7 (limit 8 leaves one level of headroom); the 20-hop `siteAssessments{location{...}}` cycle has depth 42 and is rejected |
| Expanded fields | 1000 | fragment bomb: memoised walk, 40 doubling levels measured in linear time |
| Complexity | 10000 | `first:50` full nesting = 5600 (accepted); `first:100` = 11200 (rejected); 1 aliased page = 1200, 10 aliases = 12000 (rejected) |
| Page size | 100 | resolver rejects `first` outside 1..100 with `BAD_USER_INPUT` |
| Request timeout | 10s | slow store cut off near the configured timeout |

- Why a depth limit at all: `SiteAssessment.location` makes the schema cyclic (Location -> SiteAssessment -> Location),
  so a client can nest forever. A DAG schema cannot be deep; the back-reference is what creates the attack.
- Complexity = estimated rows materialised: scalars cost 0, each object costs 1, `locations` multiplies by
  `first` (clamped to 100 so a huge `first` cannot overflow the multiplication into a cheap negative number),
  `siteAssessments` assumes 10 per location (estimate: no cap exists), `photos` 10 per assessment (real: `UpsertPhoto` caps at 10).
  Both numbers are policy, tune them.
- Why the extra field-count check: gqlgen's complexity walk re-expands every fragment spread without memoising.
  The depth/field walk in `limits.go` is memoised and runs first, so a fragment bomb is rejected in linear time
  before the complexity walk could be slow. I did not test the gqlparser validator itself against bombs; that
  runs before any of these and is outside this code.
- Limit violations return HTTP 422 (registered with `errcode.RegisterErrorType`); gqlgen's default would answer 200.
- Introspection is off unless `APP_ENV=local`. The depth walk skips `__` fields.
- Not covered: per-client rate limiting and persisted-query allowlists (the real fix for hostile clients; put the
  rate limit at the gateway/WAF).

## Partial results and errors

Fallible child fields are nullable in the schema (`siteAssessments: [SiteAssessment!]`, `photos`, `stats`,
`SiteAssessment.location`). If the photos batch fails, the response is `200` with all locations and assessments,
`photos: null`, and one `errors[]` entry per affected path, e.g.
`locations.edges[0].node.siteAssessments[1].photos`. Non-null fields would instead null the nearest nullable
parent, up to `data: null`. Nullability is the reliability contract.

- One failed batch = one query but N errors (one per waiting field). Test: 6 errors, 1 store call.
- `Query.locations` is non-null: if the root list query fails there is nothing to return, `data: null` + 1 error.
- Resolver errors have a path; they are replaced by `"internal error"` (`INTERNAL_SERVER_ERROR`) and the original
  is logged server-side, so SQL text and host names do not reach clients. Errors without a path (parse,
  validation, limits) and errors with a code (`BAD_USER_INPUT`) pass through.
- Return errors, do not panic in a resolver: gqlgen nulls the entire list when an element panics.

## Auth

`NewHandler(store, auth, cfg)` takes the middleware as a parameter and applies it outermost; production passes
`auth.Verifier.Middleware` (Cognito ID token, same as the REST API). Unauthenticated, garbage, wrong-scheme and
expired tokens get 401 and the store receives zero calls (`authn_test.go`, real verifier against a test JWKS
server). Every authenticated user can read every location, same as the REST read routes; there is no
row-level authorisation to enforce yet. If it arrives, it belongs in the `Store` (SQL `WHERE`) so loaders
cannot bypass it.

## Run

```
go generate ./internal/graph
go test ./internal/graph
go test -tags=integration -run Integration ./internal/graph
GRAPHQL_ADDR=:8081 go run ./cmd/graphql
```

Integration DB (separate from `fieldsync` and `fieldsync_analytics`; the test refuses any other database name):

```
docker exec fieldsync-postgres-1 psql -U fieldsync -d postgres -c "CREATE DATABASE fieldsync_graphql"
docker exec -i fieldsync-postgres-1 psql -U fieldsync -d fieldsync_graphql < migrations/000001_init_schema.up.sql
docker exec -i fieldsync-postgres-1 psql -U fieldsync -d fieldsync_graphql < migrations/000002_add_domain_events.up.sql
docker exec -i fieldsync-postgres-1 psql -U fieldsync -d fieldsync_graphql < migrations/000004_locations_updated_at_id_idx.up.sql
```

CI guard against a stale generated file: `go generate ./internal/graph && git diff --exit-code internal/graph`.

## REST vs GraphQL for integrations and sync

For a sync/integration consumer the style matters less than two properties: an incremental cursor and
idempotent re-delivery.

- What matters: stable keyset pagination, an `updated_since` checkpoint, at-least-once delivery with a stable
  `id` to dedup on, bounded page size, and a way to detect deletes (this schema has no tombstones: deleted rows
  cannot be synced by either style, and that is a gap).
- REST wins for: bulk/ETL pulls and webhooks (flat, cacheable, trivially rate-limited per route, simple
  to document and to reason about cost), file/binary transfer, and writes with strict idempotency semantics.
- GraphQL wins for: UI and integration clients that need different slices of the nested tree without a new
  endpoint per screen, fewer round trips over high-latency links, and a typed, introspectable contract.
- GraphQL costs: you own query-cost defence (everything in the limits section), HTTP caching is mostly gone
  (POST), errors are 200 with `errors[]` so generic monitoring misses them, and N+1 moves from the client into
  your resolvers.
- Webhooks are the push half: the outbox/`domain_events` pipeline already is that; GraphQL is the pull/backfill half.
  A consumer would backfill with `locations(updatedSince:)` then switch to events.

## Interview Q&A

1. **What is N+1 and how did you prove you fixed it?**
   A list resolver returns N parents and each parent's child resolver issues its own query: 1 + N (here 1 + 50 +
   150 = 201 queries). A per-request DataLoader collects the child keys of one level and issues one `= ANY($1)`
   query per level: 3. A test counts store calls on a fake and asserts both 201 and 3; a second test counts real
   SQL statements on Postgres (3). Breaking the batching turns it red.

2. **Why a time window, and what does it cost?**
   Resolvers are independent goroutines that cannot see each other, so the loader waits a few ms (2ms) or until
   `MaxBatch` keys, then fires. Cost: up to the window of added latency per nesting level and
   non-deterministic batch boundaries under load (a split batch costs one extra query, never wrong data).
   Tests avoid the timer by flushing on size.

3. **Why not a global cache?**
   No invalidation and no user scoping: stale reads and cross-user leakage once authorisation exists. Per-request
   cache gives dedup within one request and dies with it.

4. **Why keyset cursors instead of offset?**
   Offset is O(n) to skip and shifts under concurrent inserts/updates (skips or repeats). Keyset on
   `(updated_at, id)` seeks, is stable, and doubles as a sync checkpoint. `id` breaks timestamp ties. Mid-walk
   updates re-deliver a row (at-least-once) but never skip one. Backed by the `(updated_at, id)` index (migration
   000004): at 100k rows a page reads 4 buffers through it instead of seq-scanning 2485.

5. **How do you stop an abusive query?**
   Layered, cheapest first: parser token limit, depth limit (the schema is cyclic, so depth is unbounded without it),
   memoised expanded-field limit (stops fragment bombs before the complexity walk), cost limit
   (estimated rows: `first` x fan-out, aliases multiply it), page-size cap, request timeout, 422 for violations.
   What is missing: per-client rate limiting and persisted queries; do those at the gateway.

6. **A child field fails. What does the client see?**
   200, the parent data, `null` for the failed field, and an `errors[]` entry with that field's path. That works
   only because fallible fields are nullable; a non-null failure would null its parent. The message is masked
   to `internal error`; the real error is logged. One failed batch yields one error per waiting field.

7. **REST or GraphQL for a partner integration?**
   Depends on the consumer. For sync/backfill I care about cursor pagination, `updated_since`, idempotent
   dedup by id and rate limiting, which REST gives with less machinery and better caching. GraphQL fits clients
   with varied read shapes. Here they coexist: writes stay on `/api/sync`, GraphQL is the read side, and
   events/webhooks cover push.

8. **Why read-only, and why not mutations?**
   The write path is LWW upserts plus a transactional outbox in a single transaction, and it is the only
   thing that feeds `domain_events` (audit, dashboard aggregation, notifications). A GraphQL mutation would be a
   second write path that has to replicate that guarantee or silently bypass it. Read-only also shrinks the attack
   surface and lets the API run against a read replica later.
