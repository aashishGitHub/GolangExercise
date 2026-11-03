# Phase 9 — GraphQL: Files

Siblings: [BASICS.md](BASICS.md) · [QUESTIONS.md](QUESTIONS.md) · [learn index](../README.md) · full design doc: [graphql.md](../../graphql.md)

Test layers: **unit** = `go test ./internal/graph` (fake `Store`, no database; the shared fake is in [testutil_test.go](../../../internal/graph/testutil_test.go)). **integration** = `go test -tags=integration -run Integration ./internal/graph` against a separate database named `fieldsync_graphql` (the test refuses any other name). Setup commands are in the "Run" section of [graphql.md](../../graphql.md).

Not covered below: [internal/graph/generated.go](../../../internal/graph/generated.go) and [models_gen.go](../../../internal/graph/models_gen.go) are gqlgen output. Do not edit or review them; regenerate with `go generate ./internal/graph`.

## [internal/graph/schema.graphqls](../../../internal/graph/schema.graphqls)

- **What it does**: the contract. Types `Location`, `SiteAssessment`, `Photo`, `LocationStats`, a `LocationConnection` / `LocationEdge` / `PageInfo` wrapper, and two entry points: `Query.locations(first, after, updatedSince)` and `Query.location(id)`. No mutations or subscriptions.
- **Control flow**:
  1. `go generate ./internal/graph` (the `//go:generate` line in [resolver.go](../../../internal/graph/resolver.go)) runs gqlgen with [gqlgen.yml](../../../internal/graph/gqlgen.yml).
  2. gqlgen writes `generated.go` (the executable schema) and `models_gen.go` (types such as `LocationConnection`).
  3. At runtime `NewExecutableSchema` in `generated.go` is passed to the HTTP handler (see `NewHandler` below).
- **Data flow**: schema text -> generated Go types and resolver interfaces. Fallible child fields (`siteAssessments`, `stats`, `SiteAssessment.location`, `photos`) are nullable on purpose so a failed batch gives `null` plus an `errors[]` entry, not a nulled parent.
- **How it is tested**: no test reads the file directly. Every query test in [query_test.go](../../../internal/graph/query_test.go) executes against the generated schema, e.g. `TestLocations_DefaultsToTwentyRows`, `TestLocation_ByID`. Nullability is proved by `TestPartialResults_FailingChildFieldKeepsSiblingsAndReportsPath` in [safety_test.go](../../../internal/graph/safety_test.go). Unit.
- **Read next**: [internal/graph/handler.go](../../../internal/graph/handler.go).

## [internal/graph/handler.go](../../../internal/graph/handler.go)

- **What it does**: builds the HTTP handler: transport, limits, error masking, per-request loaders, and auth.
- **Control flow**:
  1. [cmd/graphql/main.go](../../../cmd/graphql/main.go) calls `graph.NewHandler(NewPostgresStore(pool), verifier.Middleware, serverCfg)` and mounts it at `POST /graphql` (there is also `GET /health`). `Introspection` is true only when `cfg.Env == "local"`.
  2. `NewHandler` creates a gqlgen `handler.New(NewExecutableSchema(...))`, adds `transport.POST{}` only, sets `SetParserTokenLimit`, `SetErrorPresenter(presentError)`, the `queryLimits` extension, and `extension.FixedComplexityLimit`.
  3. It returns `auth(requestScope(store, cfg, srv))`: auth is outermost.
  4. Per request: the auth middleware checks the token (401 stops here), then `requestScope` sets a context timeout (`RequestTimeout`, 10 s) and puts a fresh `Loaders` set in the context with `WithLoaders`, then gqlgen executes.
  5. Resolver errors go through `presentError`: errors with a path and no code become `internal error` and the original is logged.
- **Data flow**: HTTP body (query JSON) -> parse -> validate -> limits -> execute -> JSON `{data, errors}`. No persistence.
- **How it is tested**: unit. [authn_test.go](../../../internal/graph/authn_test.go): `TestAuth_UnauthenticatedRequestIsRejectedBeforeAnyStoreCall`, `TestAuth_GarbageAndExpiredTokensAreRejected`, `TestAuth_ValidTokenReachesResolvers` (real verifier, test JWKS server). [query_test.go](../../../internal/graph/query_test.go): `TestOnlyPostIsServed`, `TestIntrospection_DisabledByDefaultEnabledOnRequest`. [safety_test.go](../../../internal/graph/safety_test.go): `TestRootFailureNullsDataAndReportsOneMaskedError`, `TestRequestTimeoutBoundsASlowStore`.
- **Read next**: [internal/graph/schema.resolvers.go](../../../internal/graph/schema.resolvers.go).

## [internal/graph/schema.resolvers.go](../../../internal/graph/schema.resolvers.go)

- **What it does**: the resolver functions. The list resolver hits the store directly; child fields go through loaders.
- **Control flow**:
  1. `queryResolver.Locations`: `pageSize(first)` (default 20, must be 1..100, else `BAD_USER_INPUT`), `DecodeCursor(after)`, then `Store.ListLocations` with `Limit: size + 1`.
  2. The extra row decides `hasNextPage`; rows are trimmed to `size`; each edge gets `EncodeCursor(updated_at, id)`; `EndCursor` is the last edge's cursor.
  3. `locationResolver.SiteAssessments` calls `loadersFrom(ctx).SiteAssessments.Load(ctx, obj.ID)`; `siteAssessmentResolver.Photos` and `locationResolver.Stats` do the same with their loaders.
  4. `queryResolver.Location` and `siteAssessmentResolver.Location` go through `loadLocation` ([resolver.go](../../../internal/graph/resolver.go)) and the `Locations` loader.
- **Data flow**: GraphQL args -> `ListParams` -> `db.DisasterLocation` rows -> `Location` models ([models.go](../../../internal/graph/models.go), `newLocation`) -> JSON. Child rows come back as `db.*` slices grouped by parent id (`groupBy` in [loaders.go](../../../internal/graph/loaders.go)).
- **How it is tested**: unit, [query_test.go](../../../internal/graph/query_test.go): `TestLocations_KeysetPaginationWalksEveryRowOnce`, `TestLocations_EndCursorIsLastEdgeCursor`, `TestLocations_EmptyResultHasNoEndCursor`, `TestLocations_UpdatedSinceFiltersOlderRows`, `TestLocations_BadInputIsAUserErrorNotAnInternalOne`, `TestLocation_UnknownIDIsNullWithoutError`, `TestLocation_MissingStatsRowIsNull`. Integration: `TestIntegration_LocationByIDAndUnknownID`.
- **Read next**: [internal/graph/dataloader.go](../../../internal/graph/dataloader.go).

## [internal/graph/dataloader.go](../../../internal/graph/dataloader.go)

- **What it does**: a hand-written generic batching loader (`DataLoader[K, V]`), the fix for N+1. [loaders.go](../../../internal/graph/loaders.go) instantiates four of them (locations, site assessments, photos, stats).
- **Control flow**:
  1. 50 `Location.siteAssessments` resolvers (gqlgen runs list elements in goroutines) each call `Load(ctx, key)`.
  2. `Load` takes the mutex; a key already seen this request returns the existing batch (cache); otherwise `enqueue` appends it.
  3. The first key of a batch starts `time.AfterFunc(Wait, flush)` (2 ms by default).
  4. When `len(keys) >= MaxBatch` (500) the batch is sent immediately; otherwise the timer calls `flush`. `sent` is set under the mutex, so only one of them runs it.
  5. `run` calls the `BatchFunc` once with all keys (for example `siteAssessmentsBatch` calls `Store.SiteAssessmentsByLocationIDs`), stores `results` and `err`, recovers a panic into `err`, then closes `done`.
  6. Every waiting `Load` unblocks and returns `results[key]` (a missing key is the zero value, no error), or returns early on `ctx.Done()`.
- **Data flow**: keys in -> one `= ANY($1)` query -> rows grouped into `map[key]value` -> each waiter gets its own slice. Cache lives only as long as the loader (one request).
- **How it is tested**: unit, [dataloader_test.go](../../../internal/graph/dataloader_test.go): `TestDataLoader_ConcurrentLoadsShareOneBatch`, `TestDataLoader_DuplicateKeysAreFetchedOnce`, `TestDataLoader_CacheServesLaterLoadsWithoutFetching`, `TestDataLoader_MaxBatchSplitsAndDispatchesWithoutWaiting`, `TestDataLoader_MissingKeyYieldsZeroValueAndNoError`, `TestDataLoader_FetchErrorReachesEveryWaiter`, `TestDataLoader_FetchPanicBecomesError`, `TestDataLoader_LoadHonoursContextCancellation`. End to end, [nplusone_test.go](../../../internal/graph/nplusone_test.go): `TestNPlusOne_BatchedIssuesConstantNumberOfStoreCalls` (3 calls), `TestNPlusOne_NaiveResolversIssueOnePlusNPlusM` (201), `TestNPlusOne_AddingStatsAddsExactlyOneBatchedCall`, `TestNPlusOne_LoaderCacheIsPerRequest`, `TestNPlusOne_ManyToOneBackReferenceIsBatchedToo`. Integration: `TestIntegration_NestedQueryIssuesConstantNumberOfSQLStatements`, `TestIntegration_StatsAndBackReferenceAreBatchedToo` (count real SQL statements through a wrapper around the pool).
- **Read next**: [internal/graph/pgstore.go](../../../internal/graph/pgstore.go).

## [internal/graph/pgstore.go](../../../internal/graph/pgstore.go)

- **What it does**: the Postgres implementation of `Store` ([store.go](../../../internal/graph/store.go)) using raw pgx, because sqlc's `Querier` only has per-id methods (the N+1 shape).
- **Control flow**:
  1. Resolver calls `Store.ListLocations`; loader batch functions call `LocationsByIDs`, `SiteAssessmentsByLocationIDs`, `PhotosBySiteAssessmentIDs`, `LocationStatsByLocationIDs`.
  2. `ListLocations` builds SQL with `listLocationsSQL`: optional `updated_at >= $n`, optional `(updated_at, id) > ($n, $m)`, always `ORDER BY updated_at, id LIMIT $k`. Only placeholders are put in the SQL text; values are bind arguments.
  3. `collect` runs `conn.Query` and `pgx.CollectRows` with `pgx.RowToStructByPos`, which matches columns by position, so the `*Cols` constants must follow the sqlc struct order.
- **Data flow**: `ListParams` / id slices -> SQL -> `[]db.DisasterLocation`, `[]db.SiteAssessment`, `[]db.Photo`, `[]db.LocationStat`.
- **How it is tested**: `TestListLocationsSQL` ([pgstore_test.go](../../../internal/graph/pgstore_test.go), unit, table test of the exact SQL string and args for four parameter combinations). The SQL runs for real only in integration: `TestIntegration_KeysetPaginationBreaksTimestampTiesOnID`, `TestIntegration_RowUpdatedMidWalkReappearsAndIsNeverSkipped`, `TestIntegration_UpdatedSinceUsesInclusiveLowerBound` in [integration_test.go](../../../internal/graph/integration_test.go).
- **Read next**: [internal/graph/cursor.go](../../../internal/graph/cursor.go).

## [internal/graph/cursor.go](../../../internal/graph/cursor.go)

- **What it does**: encodes and decodes the opaque keyset cursor.
- **Control flow**:
  1. `queryResolver.Locations` calls `DecodeCursor(*after)` for incoming cursors (any failure returns `ErrInvalidCursor`, surfaced as `BAD_USER_INPUT`).
  2. It calls `EncodeCursor` for every edge it returns.
- **Data flow**: `Cursor{UpdatedAt, ID}` <-> `base64url("<unix micros>|<uuid>")` (`base64.RawURLEncoding`). Microseconds match `timestamptz` precision, so a round trip is lossless.
- **How it is tested**: unit, [cursor_test.go](../../../internal/graph/cursor_test.go): `TestCursor_RoundTripIsLossless`, `TestCursor_IsOpaqueBase64`, `TestDecodeCursor_RejectsGarbage`.
- **Read next**: [internal/graph/limits.go](../../../internal/graph/limits.go).

## [internal/graph/limits.go](../../../internal/graph/limits.go)

- **What it does**: query-abuse defences: depth, expanded field count, and a complexity cost model.
- **Control flow**:
  1. `NewHandler` sets the parser token limit (`MaxTokens` 5000), then registers `queryLimits` and `FixedComplexityLimit(MaxComplexity)`.
  2. After parse and validate, gqlgen calls `queryLimits.MutateOperationContext`, which calls `measure` (memoised per fragment name) to get depth and field count. Over `MaxDepth` (8): `QUERY_DEPTH_EXCEEDED`; over `MaxFields` (1000): `QUERY_TOO_LARGE`.
  3. Then the complexity limit runs with `complexityRoot()`: `locations` costs `clampedPageSize(first) * child`; `siteAssessments` assumes 10 per location; `photos` assumes 10 per assessment (`UpsertPhoto` caps photos at 10). Over 10000: `COMPLEXITY_LIMIT_EXCEEDED`.
  4. The `init` function registers the three codes so they are returned as HTTP 422 instead of 200.
- **Data flow**: parsed query AST -> `shape{depth, fields}` and a cost number -> pass, or an error with a code. Nothing persisted. Fields starting with `__` are skipped in the depth walk.
- **How it is tested**: unit, [safety_test.go](../../../internal/graph/safety_test.go): `TestDepthLimit_RejectsAbusiveCyclicQuery`, `TestDepthLimit_AllowsDeepestLegitimateQuery`, `TestDepthLimit_BoundaryIsInclusive`, `TestDepthLimit_FragmentsCountTowardsDepth`, `TestMeasure_MemoisesFragmentsSoABombIsLinear`, `TestFieldLimit_RejectsFragmentExpansionBeforeComplexityWalk`, `TestComplexityLimit_RejectsWideNestedPage`, `TestComplexityLimit_AllowsHalfSizedPageOfTheSameShape`, `TestComplexityLimit_AliasesMultiplyCost`, `TestComplexityLimit_HugeFirstIsClampedNotOverflowed`, `TestTokenLimit_RejectsOversizedDocumentsAtParse`.
- **Read next**: [migrations/000004_locations_updated_at_id_idx.up.sql](../../../migrations/000004_locations_updated_at_id_idx.up.sql).

## [migrations/000004_locations_updated_at_id_idx.up.sql](../../../migrations/000004_locations_updated_at_id_idx.up.sql)

- **What it does**: one statement, `CREATE INDEX CONCURRENTLY disaster_locations_updated_at_id_idx ON disaster_locations (updated_at, id)`. The column order matches `ORDER BY updated_at, id` and the row comparison `(updated_at, id) > (...)` in `listLocationsSQL`. `CONCURRENTLY` avoids blocking writes during the build, and cannot run inside a transaction, so the file holds only that statement (the comment in the file says so). The down file is `DROP INDEX CONCURRENTLY IF EXISTS`.
- **Control flow**: `make migrate` runs it after migrations 1 to 3 (migrate applies files in version order). It is read at query time by the Postgres planner when `ListLocations` runs; no Go code references it.
- **Data flow**: a new B-tree over `disaster_locations(updated_at, id)`; no table data changes.
- **How it is tested**: no automated test. The doc ([graphql.md](../../graphql.md), "Keyset cursors, not offsets") reports a manual `EXPLAIN (ANALYZE, BUFFERS)` on 100k seeded rows: with the index an `Index Scan` (about 4 shared buffers, 0.037 ms); without it a `Parallel Seq Scan` (2485 shared buffers, 19.2 ms). Those are the doc's own measurements; re-run them yourself before quoting.
- **Read next**: [cmd/graphql/main.go](../../../cmd/graphql/main.go) to see everything wired together.

> Note: the header comment of [integration_test.go](../../../internal/graph/integration_test.go) says the database needs migrations 000001 and 000002, while the "Run" section of [graphql.md](../../graphql.md) also applies 000004. I have not run the integration tests without 000004; grep finds no `EXPLAIN` in the graph tests, so they check rows and statement counts, not the query plan. Only 000004 provides the index.
