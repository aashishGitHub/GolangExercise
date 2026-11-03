# Phase 10 — Analytics SQL: Questions

Siblings: [BASICS.md](BASICS.md) · [FILES.md](FILES.md) · [learn index](../README.md) · analytics folder: [analytics/README.md](../../../analytics/README.md)

Run every "Try it" command from `offline-sync-app/`. They need the database from [analytics/README.md](../../../analytics/README.md) ("Create the database"). The Go test re-seeds that database on every run.

## L1 Beginner

**Q1. What is a window function, and how is it different from `GROUP BY`?**
`GROUP BY` collapses rows into one row per group. A window function (`lag`, `rank`, `avg(...) OVER (...)`) computes a value per row from a set of related rows and keeps every row. `PARTITION BY` picks the related rows, `ORDER BY` orders them. Example: `avg(events) OVER w_rows` in [analytics/queries/01_moving_avg_per_actor.sql](../../../analytics/queries/01_moving_avg_per_actor.sql).

Try it:
```
docker compose exec -T postgres psql -U fieldsync -d fieldsync_analytics < analytics/queries/01_moving_avg_per_actor.sql
```

**Q2. Why report p99 instead of the average?**
An average hides the slow tail. In this data the sync lag has a median of seconds and a p99 of hours, because offline clients queue writes ([analytics/queries/04_latency_percentiles.sql](../../../analytics/queries/04_latency_percentiles.sql), the comments in the file say this). Those figures describe the synthetic seed, not a real system.

Try it:
```
docker compose exec -T postgres psql -U fieldsync -d fieldsync_analytics < analytics/queries/04_latency_percentiles.sql
```

**Q3. What does `EXPLAIN` show, and what is a Seq Scan?**
`EXPLAIN` prints the plan the database will use; `EXPLAIN (ANALYZE, BUFFERS)` runs the query and adds real time and pages touched. A Seq Scan reads the whole table; an Index Scan jumps to matching rows. See [analytics/EXPLAIN.md](../../../analytics/EXPLAIN.md): query 1 went from a Seq Scan (3,063 buffers) to a Bitmap Index Scan (10 buffers). The doc says those timings are order of magnitude on one laptop, not a benchmark.

**Q4. Why does this phase use its own database and a seed script?**
The queries need a big, realistic dataset with known quirks (duplicates, an outage, stuck outbox rows), and the seed script truncates tables. A separate `fieldsync_analytics` database keeps that away from app data, and both [analytics/seed/seed.sql](../../../analytics/seed/seed.sql) and [analytics/analytics_test.go](../../../analytics/analytics_test.go) refuse to run unless the database name contains `analytics`.

## L2 Junior

**Q1. Trace how query 05 turns raw events into sessions.**
In [analytics/queries/05_sessionization_islands.sql](../../../analytics/queries/05_sessionization_islands.sql): `ordered` adds the previous event time per actor with `lag()`. `flagged` sets `starts_session = 1` when there is no previous event or the gap is over 30 minutes. `numbered` takes a running `sum(starts_session)` per actor, which is the session number. `sessions` groups by `(actor_sub, session_no)`. The final select summarises per actor. The first event needs the explicit `prev_at IS NULL` branch because `NULL > interval` is NULL, not true.

Try it:
```
go test -tags=integration -run TestQueryInvariants/05_sessionization_islands ./analytics/...
```

**Q2. What happens when you run `go test -tags=integration ./analytics/...`?**
`TestMain` in [analytics/analytics_test.go](../../../analytics/analytics_test.go) calls `run`: it connects (`ANALYTICS_DATABASE_URL` or the default DSN), refuses any database not named like `analytics`, executes `seed/seed.sql`, then runs the tests. `TestQueryInvariants` runs each `queries/*.sql` file and calls the matching `check*` function from the `invariants` map. Without the `integration` tag the file is not compiled.

Try it:
```
go test -tags=integration ./analytics/...
```

**Q3. How does query 07 show a dead cohort as 0% rather than a missing row?**
It builds a grid first: `CROSS JOIN LATERAL generate_series(0, (lc.week - cs.cohort_week) / 7)` gives every `(cohort, n)` cell, then `LEFT JOIN activity` and `count(a.actor_sub)` yields 0 for empty cells ([analytics/queries/07_weekly_retention_cohort.sql](../../../analytics/queries/07_weekly_retention_cohort.sql)). It also excludes the last partial week through `last_complete`, so the final week does not look like churn.

**Q4. Query 02 returns three ranks. What does each do with ties?**
`row_number` never ties (a tiebreak on `last_assessment_at DESC, actor_sub` makes it deterministic), `rank` gives ties the same number and skips the next (1,2,2,4), `dense_rank` gives ties the same number and does not skip (1,2,2,3). The query filters on `drnk <= 3`, which can return more than 3 people per location ([analytics/queries/02_top3_per_group_ties.sql](../../../analytics/queries/02_top3_per_group_ties.sql)).

Try it:
```
go test -tags=integration -run TestQueryInvariants/02_top3_per_group_ties ./analytics/...
```

## L3 Mid

**Q1. Why do the queries use window frames explicitly, and what does the test for query 01 prove?**
With an `ORDER BY`, the default frame is `RANGE UNBOUNDED PRECEDING ... CURRENT ROW`, which includes peer rows, so the file writes the frame out. The daily table is sparse (no row for an idle day), so `ROWS 6 PRECEDING` can reach back weeks while `RANGE INTERVAL '6 days'` is a true calendar window ([analytics/queries/01_moving_avg_per_actor.sql](../../../analytics/queries/01_moving_avg_per_actor.sql)). `checkMovingAvg` proves three things: the event total matches the table, the calendar average matches an independent SQL count on sampled rows, and the ROWS and RANGE results really differ on this data. The last check guards the teaching point: if the seed changed and they stopped differing, the test would fail.

Try it:
```
go test -tags=integration -run TestQueryInvariants/01_moving_avg_per_actor ./analytics/...
```

**Q2. Why test SQL by recomputing results in Go instead of asserting exact output rows?**
Exact rows would break on any seed tweak and prove little. Independent recomputation checks the logic: `checkSessions` recounts sessions in Go (`sessionsFor`), `checkRetention` rebuilds every cohort cell from raw pairs, `checkLatencyPercentiles` recomputes percentiles with `percentileCont` in Go. Each also checks cheap invariants (totals equal table counts, `p50 <= p90 <= p99`, retention in [0,1]). A bug would have to be present in both the SQL and the independent code to slip through. `TestEveryQueryFileHasInvariants` ensures no query file is added without a check. What it does not prove: that the business definition is right (for example, "active" = any event).

**Q3. Why is there a test that runs every query under two session time zones?**
Day and week buckets depend on the connection's `TimeZone` unless you convert first. `TestQueryResultsDoNotDependOnSessionTimeZone` renders each query under `UTC` and `Pacific/Auckland` and requires identical output. The comment in [analytics/queries/04_latency_percentiles.sql](../../../analytics/queries/04_latency_percentiles.sql) says this test caught a real bug: `timestamptz - interval '14 days'` is calendar arithmetic in the session zone, so across a DST change the cut-off moved by an hour. The fix is to compute the cut-off on UTC wall-clock timestamps. The test only covers those two zones.

**Q4. Why `(aggregate_id, occurred_at)` and not two single-column indexes, and what does `TestIndexesChangeThePlans` prove?**
The query filters `aggregate_id = ...` and sorts by `occurred_at`; a composite index with the equality column first serves both ([analytics/indexes.sql](../../../analytics/indexes.sql)). Query 3 is different: `processed_events` has primary key `(event_id, consumer_name)`, and a B-tree can only seek on its leading column, so a `consumer_name` filter needed its own index ([analytics/EXPLAIN.md](../../../analytics/EXPLAIN.md); the doc says Postgres 16 has no skip scan and tells you to verify newer releases). The test drops the indexes and asserts a Seq Scan and no index node in each plan, then creates them and asserts an index-based plan. It proves the plan shape changes. It does not prove a speed-up or the write cost; EXPLAIN.md says write cost was not measured, and the timings are medians of 6 runs on one machine.

Try it:
```
go test -tags=integration -run TestIndexesChangeThePlans ./analytics/...
```

## L4 Senior

The senior-level material already exists, so it is linked here and not copied:

- [analytics/QUESTIONS.md](../../../analytics/QUESTIONS.md): 12 SQL interview questions, each with a pitfall and a complexity/index note (ties in `dense_rank`, `lag()` on sparse days, NULL traps in `NOT IN`, `count(DISTINCT ...) OVER` being unsupported, `DISTINCT ON` ordering, time zone effects), plus the one-decision dashboard (which hop of the pipeline to fix first) with its caveat that the counts are planted in synthetic data.
- [analytics/EXPLAIN.md](../../../analytics/EXPLAIN.md): the measured before/after for three index lookups, what the planner did differently (bitmap scan beating a plain index scan, why the primary key did not help query 3), and the "What this does not show" list: write cost on the hot outbox table, cold cache and concurrency, and table size.
