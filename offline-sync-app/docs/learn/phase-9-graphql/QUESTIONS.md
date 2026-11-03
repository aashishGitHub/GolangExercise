# Phase 9 — GraphQL: Questions

Siblings: [BASICS.md](BASICS.md) · [FILES.md](FILES.md) · [learn index](../README.md) · full design doc: [graphql.md](../../graphql.md)

Run every "Try it" command from `offline-sync-app/`. Integration commands need the `fieldsync_graphql` database from the "Run" section of [graphql.md](../../graphql.md).

## L1 Beginner

**Q1. What is GraphQL, and what is the schema for?**
GraphQL lets a client ask for exactly the fields it needs in one request. The schema ([internal/graph/schema.graphqls](../../../internal/graph/schema.graphqls)) lists every type, field and entry point; a client cannot ask for anything outside it. Here the only entry points are `locations` and `location`.

**Q2. What is a resolver?**
A Go function that returns the value of one field. For `Location.siteAssessments` it is `locationResolver.SiteAssessments` in [internal/graph/schema.resolvers.go](../../../internal/graph/schema.resolvers.go).

**Q3. What is the N+1 problem?**
One query for a list of N parents, then one query per parent for its children. For 50 locations with 3 assessments each and 2 photos each, that is 1 + 50 + 150 = 201 queries. The project fixes it with batched loaders and gets 3. See [internal/graph/nplusone_test.go](../../../internal/graph/nplusone_test.go).

Try it:
```
go test -v -run TestNPlusOne ./internal/graph
```

**Q4. Why does the API use a cursor instead of page numbers?**
Page numbers (offset) make the database skip rows each time and can repeat or miss rows when data changes during paging. A cursor says "continue after this exact row", so the database seeks straight there. See `EncodeCursor` in [internal/graph/cursor.go](../../../internal/graph/cursor.go).

## L2 Junior

**Q1. Trace `{ locations(first: 50) { edges { node { id siteAssessments { id photos { id } } } } } }` through the code.**
`POST /graphql` reaches `NewHandler` ([internal/graph/handler.go](../../../internal/graph/handler.go)), which returns `auth(requestScope(store, cfg, srv))`. Auth checks the Cognito token (401 on failure). `requestScope` sets a 10 s timeout and creates a new `Loaders` set. gqlgen parses, validates, then `queryLimits` and the complexity limit run. `queryResolver.Locations` calls `Store.ListLocations` (SQL #1, up to `first + 1` rows). Each `Location.siteAssessments` resolver calls `Loaders.SiteAssessments.Load`; the 50 calls share one batch and run one `= ANY($1)` query (SQL #2). The same happens for `photos` (SQL #3). Total 3 statements: [graphql.md](../../graphql.md) reports this for real Postgres.

Try it (integration):
```
go test -tags=integration -run TestIntegration_NestedQueryIssuesConstantNumberOfSQLStatements ./internal/graph
```

**Q2. How does the DataLoader decide when to send a batch?**
In `DataLoader.enqueue` ([internal/graph/dataloader.go](../../../internal/graph/dataloader.go)) the first key of a batch starts a timer of `Wait` (2 ms). When the key count reaches `MaxBatch` (500) the batch is sent at once, otherwise the timer calls `flush`. A `sent` flag under the mutex ensures it runs once. `run` calls the batch function, stores results and error, then closes `done`, which unblocks every waiting `Load`.

Try it:
```
go test -v -run TestDataLoader ./internal/graph
```

**Q3. How does the second page of `locations` get fetched?**
The client passes `after: <endCursor>`. `queryResolver.Locations` ([internal/graph/schema.resolvers.go](../../../internal/graph/schema.resolvers.go)) decodes it with `DecodeCursor`, then `listLocationsSQL` ([internal/graph/pgstore.go](../../../internal/graph/pgstore.go)) adds `(updated_at, id) > ($n, $m)` and `ORDER BY updated_at, id LIMIT first+1`. The extra row only tells the resolver `hasNextPage`; it is trimmed before returning.

Try it:
```
go test -v -run TestListLocationsSQL ./internal/graph
```

**Q4. Where is authentication applied and what proves it runs first?**
`NewHandler` takes the auth middleware as a parameter and wraps everything with it, outermost. In production [cmd/graphql/main.go](../../../cmd/graphql/main.go) passes `verifier.Middleware`. `TestAuth_UnauthenticatedRequestIsRejectedBeforeAnyStoreCall` in [internal/graph/authn_test.go](../../../internal/graph/authn_test.go) asserts 401 and zero store calls.

Try it:
```
go test -v -run TestAuth ./internal/graph
```

## L3 Mid

**Q1. Why a hand-written DataLoader and a `Store` interface, instead of calling sqlc `Querier` directly?**
sqlc's `Querier` only has per-id methods, which is exactly the N+1 shape, so `PostgresStore` ([internal/graph/pgstore.go](../../../internal/graph/pgstore.go)) uses raw pgx with `= ANY($1)`. The `Store` interface ([internal/graph/store.go](../../../internal/graph/store.go)) lets tests use a fake that counts calls. The loader is about 100 lines so every line can be defended; the design doc notes that gqlgen's docs recommend `vikstrous/dataloadgen` for production if you do not want to own the code, and that the author has not benchmarked either.

**Q2. What does the N+1 test actually prove, and how is it kept honest?**
`TestNPlusOne_BatchedIssuesConstantNumberOfStoreCalls` asserts exactly 1 + 1 + 1 = 3 store calls; `TestNPlusOne_NaiveResolversIssueOnePlusNPlusM` runs the same resolvers, schema and batch functions with a naive `Loader` (defined in [testutil_test.go](../../../internal/graph/testutil_test.go)) and asserts 201. So the difference is only the loader. The doc reports a mutation check: setting `MaxBatch` to 1 turns the batched test red with `total store calls = 201, want 3`. A fake store proves call counts, not SQL; `TestIntegration_NestedQueryIssuesConstantNumberOfSQLStatements` counts real statements through a wrapper around the pgx pool.

Try it:
```
go test -v -run 'TestNPlusOne' ./internal/graph
```

**Q3. Why is `id` in the cursor, and what proves it is needed?**
Many rows can share one `updated_at`. With a time-only cursor, rows with the same timestamp that straddle a page boundary are skipped. `(updated_at, id)` makes each position unique. `TestIntegration_KeysetPaginationBreaksTimestampTiesOnID` seeds groups of 5 rows with the same timestamp and pages 7 at a time; the doc says dropping `id` from the comparison lost 15 of 50 rows in a mutation run. The unit test `TestLocations_KeysetPaginationWalksEveryRowOnce` checks the same walk against the fake store.

Try it (integration):
```
go test -tags=integration -run TestIntegration_KeysetPaginationBreaksTimestampTiesOnID ./internal/graph
```

**Q4. Why three limits (depth, field count, complexity) instead of just one?**
They catch different attacks, cheapest first. Depth: the schema is cyclic (`Location` -> `SiteAssessment` -> `Location`), so nesting is unbounded without it. Field count with memoised fragments: gqlgen's complexity walk re-expands every fragment spread, so a fragment bomb could make that walk slow; `measure` in [internal/graph/limits.go](../../../internal/graph/limits.go) rejects it in linear time first (`TestMeasure_MemoisesFragmentsSoABombIsLinear`, `TestFieldLimit_RejectsFragmentExpansionBeforeComplexityWalk`). Complexity: estimates rows (aliases multiply the cost, `TestComplexityLimit_AliasesMultiplyCost`; a huge `first` is clamped so it cannot overflow, `TestComplexityLimit_HugeFirstIsClampedNotOverflowed`). Violations return 422 (registered in `init`).

Try it:
```
go test -v -run 'TestDepthLimit|TestFieldLimit|TestComplexityLimit|TestMeasure' ./internal/graph
```

## L4 Senior

The eight questions already answered in [graphql.md](../../graphql.md) (section "Interview Q&A": N+1 proof, the time window, no global cache, keyset vs offset, abusive queries, partial failures, REST vs GraphQL, read-only) are not repeated here. Three more:

**Q1. The complexity numbers are estimates. What happens when reality differs, and what do you do?**
`complexityRoot` in [internal/graph/limits.go](../../../internal/graph/limits.go) assumes 10 assessments per location; its comment says no cap exists on assessments, only on photos (`UpsertPhoto` caps those at 10). If a location really has 100 assessments, the estimate is 10x too low for that part of the tree, so the limit under-protects. Other bounds still hold: depth 8, page size 100, the 10 s request timeout, and `MaxBatch` 500 keys per loader query. The doc calls both numbers "policy, tune them". Better options: cap assessments per location at write time, or read real row counts to calibrate. Neither is built. Not solved either: per-client rate limiting and persisted-query allowlists (the doc says to put rate limits at the gateway or WAF).

**Q2. A consumer wants to use `locations(updatedSince:)` as a sync feed. What cannot it do?**
Detect deletes. The doc states this schema has no tombstones, so deleted rows cannot be synced by GraphQL or by REST; contrast with Phase 8, where deletes are tombstones ([phase 8 basics](../phase-8-two-way-sync/BASICS.md)). Also: delivery is at-least-once (a row updated mid-walk reappears), so the consumer must de-duplicate by `id`; `updatedSince` is inclusive on purpose. I am not certain every writer path bumps `updated_at` on every change; verify that before relying on it as a change feed. Authorization is also flat: any authenticated user can read every location, and the doc says row-level rules would belong in the `Store` SQL so loaders cannot bypass them.

**Q3. What are the failure modes of the per-request loader at higher concurrency or bigger batches?**
(a) A failed batch is cached for the rest of the request and surfaces as one error per waiting field, so one slow or failing query becomes many `errors[]` entries (the doc's test shows 6 errors from 1 store call). (b) The batch runs with the context of the first caller; it works because every caller shares the request's context lineage and its timeout. Reusing a loader across requests would break that and the cache-scoping rule, which is why `requestScope` builds new `Loaders` per request. (c) Batch size is capped at `MaxBatch` 500 keys, so one request over a very large parent set becomes several `= ANY($1)` queries; the doc has no measurement of the cost of 500-key lists, so I am not certain how it behaves at scale. (d) The 2 ms timer window is tuned for this load; under CPU contention a batch can split into 2 queries instead of 1 (harmless but real, and the reason the tests flush on size). Migration risk too: `CREATE INDEX CONCURRENTLY` that fails midway leaves an `INVALID` index which must be dropped before retrying ([migrations/000004_locations_updated_at_id_idx.up.sql](../../../migrations/000004_locations_updated_at_id_idx.up.sql); the doc says so).
