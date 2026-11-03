# Phase 10 — Analytics SQL: Files

Siblings: [BASICS.md](BASICS.md) · [QUESTIONS.md](QUESTIONS.md) · [learn index](../README.md) · analytics folder: [analytics/README.md](../../../analytics/README.md)

There is no application Go code in this phase. The code is SQL files plus one Go test file ([analytics/doc.go](../../../analytics/doc.go) is only a `package analytics` line). All tests are **integration** (`go test -tags=integration ./analytics/...`) against a real Postgres database named `fieldsync_analytics`; there are no unit tests. Setup commands are in [analytics/README.md](../../../analytics/README.md).

All numbers in this phase come from synthetic seed data on one laptop. The EXPLAIN doc itself says to treat them as order of magnitude, not a benchmark.

## [analytics/seed/seed.sql](../../../analytics/seed/seed.sql)

- **What it does**: builds a deterministic fake dataset (sizes asserted by `TestSeedShape`: about 190 to 210 locations, 1,900 to 2,100 assessments, 13,000 to 16,500 photos, 45,000 to 55,000 events, 25 actors, 3 consumers, a span of 85 to 90 days) in one transaction, with planted quirks: weekday pattern, a 6-hour outage on 2026-08-13 with no events, duplicate events, unpublished outbox rows, a consumer stall.
- **Control flow**:
  1. Run by hand with `psql` through docker (see README), or by `TestMain` via `seed` in [analytics_test.go](../../../analytics/analytics_test.go).
  2. `BEGIN`, `SET LOCAL TIME ZONE 'UTC'`.
  3. A `DO $$` block raises an exception unless `current_database()` matches `analytics`.
  4. `TRUNCATE` of the app tables, then helper functions in `pg_temp` (`u`, `z`, `ex` turn `md5(key)` into uniform, normal and exponential draws).
  5. `INSERT` into `disaster_locations`, `site_assessments`, `photos`, `domain_events`, `processed_events`; `COMMIT`; `ANALYZE`.
- **Data flow**: hash of a key string -> pseudo-random number -> timestamps, lags and ids -> rows. No `random()`, so the same input gives byte-identical output.
- **How it is tested**: integration, [analytics_test.go](../../../analytics/analytics_test.go): `TestSeedIsDeterministic` (md5 fingerprint of five tables equal after a re-seed), `TestSeedShape` (row counts in ranges, 25 actors, 3 consumers, span 85 to 90 days, top-3 actors hold 0.4 to 0.8 of events, weekend share under 0.10, zero events in the outage window, at most 10 photos per site, duplicates and unpublished rows exist).
- **Read next**: [analytics/queries/01_moving_avg_per_actor.sql](../../../analytics/queries/01_moving_avg_per_actor.sql).

## [analytics/queries/01_moving_avg_per_actor.sql](../../../analytics/queries/01_moving_avg_per_actor.sql)

- **What it does**: answers "is each worker trending up or down?" with a 7-day moving average, computed three ways to show how `ROWS` and `RANGE` frames differ on sparse data.
- **Control flow** (inside one SQL statement):
  1. CTE `daily`: `GROUP BY actor_sub, day` over `domain_events`, with the day taken `AT TIME ZONE 'UTC'`.
  2. Two named windows: `w_rows` (`ROWS BETWEEN 6 PRECEDING AND CURRENT ROW`) and `w_range` (`RANGE BETWEEN INTERVAL '6 days' PRECEDING AND CURRENT ROW`).
  3. Select `avg_rows_7`, `avg_range_7_active_days`, `avg_calendar_7` (sum over window divided by 7.0), `active_days_in_window`, `rows_frame_span_days`.
- **Data flow**: `domain_events(actor_sub, occurred_at)` -> daily counts per actor (days with no events have no row) -> per-row window averages. Read-only.
- **How it is tested**: integration, `checkMovingAvg` run by `TestQueryInvariants/01_moving_avg_per_actor` in [analytics_test.go](../../../analytics/analytics_test.go). It asserts the event total equals `count(*)` of `domain_events`, spot-checks `avg_calendar_7` against an independent SQL count for about 30 sampled rows, and fails if the ROWS and RANGE frames never differ (so the query would stop teaching the point).
- **Read next**: [analytics/queries/02_top3_per_group_ties.sql](../../../analytics/queries/02_top3_per_group_ties.sql).

## [analytics/queries/02_top3_per_group_ties.sql](../../../analytics/queries/02_top3_per_group_ties.sql)

- **What it does**: top assessors per disaster location, showing `row_number`, `rank` and `dense_rank` side by side and why ties matter.
- **Control flow**:
  1. CTE `per_actor`: `count(*)` of assessments per `(location_id, created_by)`.
  2. CTE `ranked`: three window functions over `PARTITION BY location_id`. `row_number` has a full tiebreak (`assessments DESC, last_assessment_at DESC, actor_sub`).
  3. Join to `disaster_locations` for the name; `WHERE r.drnk <= 3`.
- **Data flow**: `site_assessments` -> per-actor counts -> ranks -> rows with `rn`, `rnk`, `drnk`. A window function cannot be used in `WHERE`, which is why ranking happens in a CTE first.
- **How it is tested**: integration, `checkTop3`: asserts `drnk <= rnk <= rn` on every row, `row_number` is contiguous, counts are non-increasing, the top count per location matches an independent query, and that `rank <= 3` returns more than 3 rows for at least one location (otherwise ties disappeared from the seed).
- **Read next**: [analytics/queries/04_latency_percentiles.sql](../../../analytics/queries/04_latency_percentiles.sql).

## [analytics/queries/04_latency_percentiles.sql](../../../analytics/queries/04_latency_percentiles.sql)

- **What it does**: p50, p90, p99 of three lags (sync, publish, consume), plus p99 for the last 14 days, in one pass.
- **Control flow**:
  1. CTE `as_of`: latest `occurred_at`, so "last 14 days" is relative to the data, not `now()`.
  2. CTE `samples`: three `UNION ALL` branches: sync lag (`occurred_at` minus `payload ->> 'updatedAt'`), publish lag (`published_at - occurred_at`), consume lag (`processed_events` joined to `domain_events`).
  3. Final select groups by `(metric, segment)` and uses `percentile_cont(...) WITHIN GROUP (ORDER BY secs)`; the 14-day column adds `FILTER (WHERE ...)` after `WITHIN GROUP`.
- **Data flow**: timestamps -> seconds -> ordered-set percentiles per group. Percentiles are computed from raw rows because p99s cannot be averaged across groups.
- **How it is tested**: integration, `checkLatencyPercentiles`: asserts `0 <= p50 <= p90 <= p99`, per-metric row totals equal the table counts, and recomputes the photo sync percentiles in Go (`percentileCont`) and compares within a small tolerance. `TestQueryResultsDoNotDependOnSessionTimeZone` also runs it in `UTC` and `Pacific/Auckland`; the file's comment says that test caught a DST bug in the "last 14 days" cut-off.
- **Read next**: [analytics/queries/05_sessionization_islands.sql](../../../analytics/queries/05_sessionization_islands.sql).

## [analytics/queries/05_sessionization_islands.sql](../../../analytics/queries/05_sessionization_islands.sql)

- **What it does**: splits each actor's events into work sessions (a gap over 30 minutes starts a new one) and summarises them.
- **Control flow**:
  1. `ordered`: `lag(occurred_at) OVER (PARTITION BY actor_sub ORDER BY occurred_at, id)`.
  2. `flagged`: `CASE WHEN prev_at IS NULL OR occurred_at - prev_at > interval '30 minutes' THEN 1 ELSE 0 END`.
  3. `numbered`: running `sum(starts_session)` as `session_no`.
  4. `sessions`: group by `(actor_sub, session_no)`, then the final select gives sessions, events, median and longest duration per actor.
- **Data flow**: event timestamps -> gap flags -> running sum (session id) -> per-session, then per-actor aggregates. The `id` in `ORDER BY` makes ordering total.
- **How it is tested**: integration, `checkSessions`: total events across sessions equals the table count; for `actor-01` and `actor-21` it recounts sessions and the longest session in Go (`sessionsFor`) and compares.
- **Read next**: [analytics/queries/07_weekly_retention_cohort.sql](../../../analytics/queries/07_weekly_retention_cohort.sql).

## [analytics/queries/07_weekly_retention_cohort.sql](../../../analytics/queries/07_weekly_retention_cohort.sql)

- **What it does**: weekly cohort retention: of the actors who started in week W, what share is active again n weeks later.
- **Control flow**:
  1. `activity`: distinct `(actor_sub, week)` with `date_trunc('week', ...)` after `AT TIME ZONE 'UTC'`.
  2. `last_complete`: the last full week (the final partial week is excluded to avoid fake churn).
  3. `cohort` and `cohort_size`: each actor's first week and the size per week.
  4. `grid`: `CROSS JOIN LATERAL generate_series(0, (lc.week - cs.cohort_week) / 7)` builds every `(cohort, n)` cell.
  5. Final `LEFT JOIN activity`, `count(a.actor_sub) / size` gives `retention`; empty cells show as 0.
- **Data flow**: `domain_events` -> weekly activity -> cohort grid -> retention triangle.
- **How it is tested**: integration, `checkRetention`: retention in [0,1], week-0 retention is 1, and an independent Go recomputation of every `(cohort, n)` cell must match exactly (same number of cells and same counts).
- **Read next**: [analytics/indexes.sql](../../../analytics/indexes.sql).

## [analytics/indexes.sql](../../../analytics/indexes.sql) (with [drop_indexes.sql](../../../analytics/drop_indexes.sql) and [explain/](../../../analytics/explain/))

- **What it does**: the EXPLAIN study. Three composite indexes: `domain_events (aggregate_id, occurred_at)`, `domain_events (actor_sub, occurred_at)`, `processed_events (consumer_name, processed_at)`, followed by `ANALYZE`. `drop_indexes.sql` removes them; `explain/01..03_*.sql` are the three lookups measured.
- **Control flow**:
  1. Run `drop_indexes.sql`, then `EXPLAIN (ANALYZE, BUFFERS)` on an `explain/` query (README has the exact commands): Seq Scan.
  2. Run `indexes.sql`, run the same EXPLAIN again: Bitmap Index Scan (queries 1 and 2) or Index Only Scan (query 3).
- **Data flow**: no data changes, only indexes and planner statistics. Measured results and the write-cost caveat are in [analytics/EXPLAIN.md](../../../analytics/EXPLAIN.md): for query 1, 6.11 ms to 0.105 ms and 3,063 to 10 buffers on Postgres 16.15, data fully cached, median of 6 runs. The doc says this is order of magnitude on one machine. Write cost was not measured.
- **How it is tested**: integration, `TestIndexesChangeThePlans` in [analytics_test.go](../../../analytics/analytics_test.go): drops the indexes and asserts each of the 3 plans has a Seq Scan and no index node, then creates them and asserts index-based plans with no Seq Scan. It checks plan shape, not speed. The timings in EXPLAIN.md are not asserted by any test.
- **Read next**: [analytics/analytics_test.go](../../../analytics/analytics_test.go).

## [analytics/analytics_test.go](../../../analytics/analytics_test.go)

- **What it does**: the only Go file in the phase (build tag `integration`, about 1,000 lines). Seeds the database once and checks invariants for every query file.
- **Control flow**:
  1. `TestMain` calls `run`: reads `ANALYTICS_DATABASE_URL` (default `postgres://fieldsync:fieldsync@localhost:5432/fieldsync_analytics?sslmode=disable`), opens a pgx pool, refuses any database whose name lacks `analytics`, calls `seed`, then `m.Run()`.
  2. `TestEveryQueryFileHasInvariants` checks every `queries/*.sql` file has an entry in the `invariants` map, and vice versa.
  3. `TestQueryInvariants` runs each query with `mustQuery` and calls its `check*` function.
  4. `TestQueryResultsDoNotDependOnSessionTimeZone` renders each query's output under `UTC` and `Pacific/Auckland` and compares.
  5. `TestIndexesChangeThePlans`, `TestSeedIsDeterministic`, `TestSeedShape` as described above.
- **Data flow**: SQL files read from disk -> executed through pgx -> `rowset` -> compared to independent SQL or Go recomputation. Test code never mutates the query files.
- **How it is tested**: it is the test. It has no test of its own. If it cannot connect to Postgres it exits at `TestMain`. Plain `go test ./...` skips it because of the build tag.
- **Read next**: [analytics/QUESTIONS.md](../../../analytics/QUESTIONS.md) for the 12 SQL interview questions.

> Note: the README says `go vet ./analytics/...` is part of running the tests. With the `integration` build tag absent the test file is excluded, so this vets only `doc.go`. Use `go vet -tags=integration ./analytics/...` to vet the test file (standard Go build-tag behaviour; I have not run it here).
