# EXPLAIN study: before and after indexes

Measured locally: one Postgres 16.15 container (Docker on an Apple Silicon laptop), database `fieldsync_analytics`, data fully in `shared_buffers` (every plan shows `shared hit`, no `read`). `domain_events` is 50,680 rows / 24 MB, `processed_events` 124,639 rows / 8.6 MB. Times are the server-side `Execution Time` of `EXPLAIN (ANALYZE, BUFFERS)`, median of 6 consecutive runs. Treat them as order-of-magnitude on one machine, not a benchmark: a cold cache, a network hop, concurrency or a 100x bigger table change the absolute numbers and widen the gap.

Queries are in `explain/`, indexes in `indexes.sql`, the reset in `drop_indexes.sql`. The 14 queries in `queries/` do not need these indexes; these three are lookups a customer-facing screen would run.

| # | Query | Before | After | Buffers before / after | Speed-up |
|---|-------|--------|-------|------------------------|----------|
| 1 | audit trail of one aggregate (`aggregate_id = ...`, 13 rows) | 6.11 ms, Seq Scan | 0.105 ms, Bitmap Index Scan | 3,063 / 10 | about 58x |
| 2 | one actor's recent activity (`actor_sub = ... AND occurred_at >= ...`, 450 rows) | 8.54 ms, Seq Scan | 0.46 ms, Bitmap Index Scan | 3,063 / 93 | about 19x |
| 3 | one consumer's recent processing (`consumer_name = ... AND processed_at >= ...`, 1,051 rows) | 4.32 ms, Seq Scan | 0.22 ms, Index Only Scan | 1,081 / 9 | about 19x |

Index sizes: `domain_events_aggregate_idx` 2,032 kB, `domain_events_actor_time_idx` 2,000 kB, `processed_events_consumer_time_idx` 5,336 kB.

## What the planner did differently

1. **No usable index before.** The only indexes on `domain_events` are the primary key (`id`) and the partial `domain_events_unpublished_idx`. Neither leads with `aggregate_id` or `actor_sub`, so the planner had one option: Seq Scan, reading all 3,060 8 kB pages, discarding 50,667 of 50,680 rows (query 1), then sorting the survivors.
2. **Query 1 and 2 with the composite index.** `(aggregate_id, occurred_at)` and `(actor_sub, occurred_at)` put the equality column first, so the planner reads a handful of index pages (3 and 5 buffers) and then only the heap blocks that hold matching rows (4 and 85). It chose a Bitmap Index Scan plus a separate Sort by `occurred_at`.
3. **It did not use the index order to skip the Sort, and that was the faster choice.** With `SET enable_bitmapscan = off` (single run each) the planner uses a plain Index Scan, the Sort disappears because `occurred_at` is the second index column, and it is slower: 0.25 ms vs 0.075 ms for query 1 and 1.22 ms vs 0.42 ms for query 2 (374 buffers vs 93 for query 2). One actor's rows are scattered across the table, so a plain index scan revisits heap pages repeatedly, while a bitmap scan visits each page once.
4. **Query 3: the primary key does not help.** `processed_events` has PRIMARY KEY `(event_id, consumer_name)`. A btree can only seek on its leading column; `consumer_name` is second, and Postgres 16 has no skip scan (I believe that arrived in a later release; verify in the current docs), so the filter on `consumer_name` forced a Seq Scan. `(consumer_name, processed_at)` has both predicates in the index; because the query only needs `count(*)` and `max(processed_at)`, it becomes an Index Only Scan. `Heap Fetches: 0` in this run depends on the visibility map being current, which I did not control; on a table with recent writes it would fetch from the heap.
5. **Row estimates.** Query 1 estimated 15 rows before and 3 after (actual 13): the planner's picture of how many events a single aggregate has is rough, and that is why it favours the cheap bitmap plan. `ANALYZE` after creating the indexes is part of `indexes.sql`.

## What this does not show

- Write cost: `domain_events` is the transactional outbox, written on every sync. Two more indexes make every insert maintain three btrees instead of one. I did not measure that; weigh it before indexing a hot outbox table, or serve these reads from a replica/warehouse instead.
- Cold cache and concurrency. All reads here are from memory; on disk the Seq Scan would be much slower, so the real gap is likely larger.
- A single table size. At 50k rows a Seq Scan costs milliseconds, which is why none of the 14 analytical queries needs an index: they read every row anyway.

`go test -tags=integration ./analytics/...` includes `TestIndexesChangeThePlans`, which drops the indexes, asserts every plan contains a Seq Scan and no index node, creates them, and asserts the plans are index-based with no Seq Scan.
