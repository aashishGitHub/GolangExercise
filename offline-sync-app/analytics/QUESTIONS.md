# FieldSync analytics: interview SQL questions

Database: `fieldsync_analytics` (seed in `seed/seed.sql`). Every SQL block below was run against the seeded data; numbers quoted are measured. Day and week buckets use `AT TIME ZONE 'UTC'` so results do not depend on the session TimeZone.

## 1. Which was each field worker's second-busiest day, and how busy was it?

```sql
WITH daily AS (
  SELECT actor_sub, (occurred_at AT TIME ZONE 'UTC')::date AS day, count(*) AS events
  FROM domain_events
  GROUP BY 1, 2
), ranked AS (
  SELECT *, dense_rank() OVER (PARTITION BY actor_sub ORDER BY events DESC) AS r
  FROM daily
)
SELECT actor_sub, day, events
FROM ranked
WHERE r = 2
ORDER BY actor_sub, day;
```

**Pitfall.** Ties. `dense_rank() = 2` returns every day tied at the second-highest count, so an actor can have several rows. An actor with only one distinct daily count has no row at all (not a NULL row). `rank() = 2` would return nothing after a two-way tie for first (ranks go 1,1,3).

**Complexity / index.** One hash aggregate over domain_events (O(n)), then a sort per actor over the ~1k (974) actor-days. Every row is needed, so no index avoids the scan.

## 2. For each worker, how did their volume change from one working day to the next?

```sql
WITH daily AS (
  SELECT actor_sub, (occurred_at AT TIME ZONE 'UTC')::date AS day, count(*) AS events
  FROM domain_events
  GROUP BY 1, 2
)
SELECT actor_sub, day, events,
       events - lag(events) OVER w AS delta,
       round(100.0 * (events - lag(events) OVER w) / NULLIF(lag(events) OVER w, 0), 1) AS pct_change
FROM daily
WINDOW w AS (PARTITION BY actor_sub ORDER BY day)
ORDER BY actor_sub, day;
```

**Pitfall.** `lag()` is the previous ACTIVE day, not the previous calendar day (idle days have no row). First row per actor is NULL. Use `100.0`, not `100`: `5/2` is `2` in Postgres, `5/2.0` is `2.5` (checked). `NULLIF(..., 0)` stops divide-by-zero from aborting the query.

**Complexity / index.** Same aggregate, then one WindowAgg per actor partition; one sort on (actor_sub, day). Index does not help, every row is read.

## 3. How concentrated is our activity: what share of all events comes from our three busiest workers?

```sql
WITH per_actor AS (
  SELECT actor_sub, count(*) AS events
  FROM domain_events
  GROUP BY actor_sub
), ranked AS (
  SELECT events, row_number() OVER (ORDER BY events DESC, actor_sub) AS rn
  FROM per_actor
)
SELECT round(sum(events) FILTER (WHERE rn <= 3) / sum(events), 4) AS top3_share
FROM ranked;
```

**Pitfall.** Integer division: `count(*) FILTER (...) / count(*)` is 0 or 1. Here the numerator is `sum(bigint)`, which is numeric, so it is safe, but `sum(int)` is bigint and would truncate; cast explicitly. Ties at rank 3: `row_number` cuts arbitrarily unless you add a tiebreaker (actor_sub here). Measured on the seed: 0.6103.

**Complexity / index.** O(n) aggregate + a sort of 25 rows. An index on actor_sub would allow an index-only count at best; not worth it.

## 4. How many assessments does a typical location have, and how many have none? Is the average misleading?

```sql
SELECT round(avg(n), 2)                                 AS mean_sites,
       percentile_cont(0.5) WITHIN GROUP (ORDER BY n)   AS median_sites,
       count(*) FILTER (WHERE n = 0)                    AS locations_with_none
FROM (
  SELECT l.id, count(s.id) AS n
  FROM disaster_locations l
  LEFT JOIN site_assessments s ON s.location_id = l.id
  GROUP BY l.id
) per_location;
```

**Pitfall.** `LEFT JOIN` + `count(s.id)`: an INNER JOIN drops the zero-assessment locations, and `count(*)` would count the NULL-extended row as 1. Mean vs median differ a lot because of skew: seed gives mean 10.00, median 4, 42 locations with none.

**Complexity / index.** Aggregate over site_assessments joined to 200 locations; site_assessments(location_id) exists (migration 000001). `percentile_cont` sorts 200 values: trivial. At scale, use approximate percentiles.

## 5. Which locations have never had an assessment?

```sql
SELECT l.name
FROM disaster_locations l
WHERE NOT EXISTS (SELECT 1 FROM site_assessments s WHERE s.location_id = l.id)
ORDER BY l.name;
```

**Pitfall.** `NOT IN (subquery)` returns zero rows if the subquery yields even one NULL (verified: 0 rows vs 42). `NOT EXISTS` is NULL-safe and plans as a Hash Anti Join. `LEFT JOIN ... WHERE s.id IS NULL` is equivalent.

**Complexity / index.** Anti-join. EXPLAIN shows a Nested Loop Anti Join: for each of the 200 locations an Index Only Scan on site_assessments_location_id_idx (migration 000001) stops at the first match.

## 6. Each week, how many workers are new and how many are returning?

```sql
WITH weekly AS (
  SELECT DISTINCT actor_sub, date_trunc('week', occurred_at AT TIME ZONE 'UTC')::date AS week
  FROM domain_events
), tagged AS (
  SELECT week, actor_sub, week = min(week) OVER (PARTITION BY actor_sub) AS is_new
  FROM weekly
)
SELECT week,
       count(*) FILTER (WHERE is_new)     AS new_actors,
       count(*) FILTER (WHERE NOT is_new) AS returning_actors
FROM tagged
GROUP BY week
ORDER BY week;
```

**Pitfall.** 'Returning' here means active in any earlier week, including after a gap; it is not 'retained from last week'. `min(week) OVER (PARTITION BY actor)` marks the first week; the first and last weeks of the data are partial (data runs Wed to Mon). `date_trunc('week')` snaps to Monday and must be applied after `AT TIME ZONE 'UTC'`.

**Complexity / index.** DISTINCT (actor, week) is a sort or hash over n rows, then a window over a few hundred rows. An index (actor_sub, occurred_at) can feed the DISTINCT in order.

## 7. Give me events per day for the whole period, quiet days included, with a trailing 7-day total.

```sql
WITH bounds AS (
  SELECT min((occurred_at AT TIME ZONE 'UTC')::date) AS lo,
         max((occurred_at AT TIME ZONE 'UTC')::date) AS hi
  FROM domain_events
), daily AS (
  SELECT g.d::date AS day, count(de.id) AS events
  FROM bounds b
  CROSS JOIN LATERAL generate_series(b.lo::timestamp, b.hi::timestamp, interval '1 day') AS g(d)
  LEFT JOIN domain_events de ON (de.occurred_at AT TIME ZONE 'UTC')::date = g.d::date
  GROUP BY g.d::date
)
SELECT day, events,
       sum(events) OVER (ORDER BY day ROWS BETWEEN 6 PRECEDING AND CURRENT ROW) AS trailing_7d
FROM daily
ORDER BY day;
```

**Pitfall.** `count(de.id)`, not `count(*)`: `count(*)` returns 1 for an empty day (the NULL-extended row). `ROWS 6 PRECEDING` is only a calendar week because the series is dense; on sparse data use RANGE. `generate_series(date, date, interval)` resolves to the timestamptz overload and follows the session TimeZone (checked with pg_typeof); cast the bounds to `timestamp`.

**Complexity / index.** One full Seq Scan of domain_events; EXPLAIN shows a Merge Left Join between the day series and the events on the date expression (checked). Rewriting the join as `occurred_at >= d AND occurred_at < d + 1 day` (UTC timestamptz bounds) lets a btree on occurred_at drive it.

## 8. How many assessments are empty, partly photographed, or maxed out at the 10-photo limit?

```sql
WITH per_site AS (
  SELECT s.id, count(p.id) AS photos
  FROM site_assessments s
  LEFT JOIN photos p ON p.site_assessment_id = s.id
  GROUP BY s.id
)
SELECT count(*)                                                   AS sites,
       count(*) FILTER (WHERE photos = 0)                         AS no_photos,
       count(*) FILTER (WHERE photos BETWEEN 1 AND 9)             AS some_photos,
       count(*) FILTER (WHERE photos = 10)                        AS at_cap,
       round(count(*) FILTER (WHERE photos = 10)::numeric / count(*), 4) AS share_at_cap
FROM per_site;
```

**Pitfall.** Zero-photo sites exist only with a LEFT JOIN. `BETWEEN 1 AND 9` is inclusive at both ends. The cap of 10 is an application rule (UpsertPhoto in sqlc/queries.sql), not a database constraint: the seed test asserts max = 10, in production you should too.

**Complexity / index.** Aggregate over photos grouped by site; photos(site_assessment_id) already exists (migration 000001). One pass over 14.5k photos.

## 9. What is the longest break each worker took between two active days, and when did they come back?

```sql
WITH active_days AS (
  SELECT DISTINCT actor_sub, (occurred_at AT TIME ZONE 'UTC')::date AS day
  FROM domain_events
), gaps AS (
  SELECT actor_sub, day,
         day - lag(day) OVER (PARTITION BY actor_sub ORDER BY day) AS gap_days
  FROM active_days
)
SELECT DISTINCT ON (actor_sub) actor_sub, gap_days AS longest_gap_days, day AS returned_on
FROM gaps
WHERE gap_days IS NOT NULL
ORDER BY actor_sub, gap_days DESC, day;
```

**Pitfall.** `day - lag(day)` is an integer (date minus date); 1 means consecutive days, so idle days = gap - 1. Leading time before the first event and trailing time after the last (churn) are not gaps. `DISTINCT ON (actor_sub)` keeps the first row per ORDER BY, so ORDER BY must start with actor_sub; add `day` as the tiebreaker.

**Complexity / index.** Sort by (actor_sub, day) over ~1k (974) actor-days. Trivial; the DISTINCT over events is the O(n) part.

## 10. What share of events reach the message bus within 5 seconds, by hour of day? Do unpublished ones count against us?

```sql
SELECT extract(hour FROM occurred_at AT TIME ZONE 'UTC')::int AS hour_utc,
       count(*)                                               AS events,
       round(count(*) FILTER (WHERE published_at - occurred_at <= interval '5 seconds')::numeric
             / count(*), 4)                                   AS pct_published_within_5s,
       count(*) FILTER (WHERE published_at IS NULL)           AS unpublished
FROM domain_events
GROUP BY 1
ORDER BY 1;
```

**Pitfall.** `published_at - occurred_at` is NULL for unpublished rows, and FILTER treats NULL as false, so unpublished rows stay in the denominator as misses. State that choice; for 'of those published', filter the denominator too. Hour of day needs `AT TIME ZONE`: the same calendar day 2026-07-23 holds 1,443 events by UTC date, 1,389 by Asia/Kolkata and 889 by Pacific/Auckland (measured).

**Complexity / index.** Single scan, 24 groups. A partial index like domain_events_unpublished_idx serves only the NULL side.

## 11. Does the event log agree with the photos table about each photo's current condition?

```sql
WITH latest AS (
  SELECT DISTINCT ON (aggregate_id) aggregate_id, payload ->> 'condition' AS condition
  FROM domain_events
  WHERE event_type = 'photo.upserted'
  ORDER BY aggregate_id, (payload ->> 'updatedAt')::timestamptz DESC, occurred_at DESC, id DESC
)
SELECT count(*)                                                           AS photos_in_event_log,
       count(*) FILTER (WHERE l.condition IS DISTINCT FROM p.condition)   AS disagree_with_photos_table
FROM latest l
JOIN photos p ON p.id = l.aggregate_id;
```

**Pitfall.** `DISTINCT ON` needs ORDER BY to start with the DISTINCT ON expression. Order by the record's own version (`updatedAt`, which is the last-write-wins key in the upsert), not arrival time: on this seed both orderings give 0 disagreements (checked) because versions are days apart, but they diverge when an offline device syncs an older edit after a newer one. `IS DISTINCT FROM` is the NULL-safe `<>`.

**Complexity / index.** Sort of ~40k photo events by (aggregate_id, version). The text-to-timestamptz cast is not IMMUTABLE, so it cannot be an index expression (checked: ERROR functions in index expression must be marked IMMUTABLE); store updated_at as a column to index it.

## 12. How many distinct workers have we ever had, as a running total by day?

```sql
WITH first_seen AS (
  SELECT actor_sub, min((occurred_at AT TIME ZONE 'UTC')::date) AS day
  FROM domain_events
  GROUP BY actor_sub
), new_per_day AS (
  SELECT day, count(*) AS new_actors
  FROM first_seen
  GROUP BY day
)
SELECT day, new_actors,
       sum(new_actors) OVER (ORDER BY day ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS cumulative_actors
FROM new_per_day
ORDER BY day;
```

**Pitfall.** `count(DISTINCT actor_sub) OVER (ORDER BY day)` is rejected: `ERROR: DISTINCT is not implemented for window functions` (checked). Compute each actor's first day, count those per day, then take a running sum. Days with no new worker have no row; join to a generate_series if you need a continuous line.

**Complexity / index.** Aggregate to 25 first-seen rows, then a trivial window. The expensive part is the O(n) scan for min(day) per actor.

## A dashboard that informs ONE customer decision

**Decision:** which hop of the pipeline do we put an engineer on this week: the offline client sync path, the outbox relay, or a consumer?

Three metrics, because each one points at exactly one hop and together they let the customer choose. All three are in `queries/04`, `queries/09` in fuller form.

**1. Is the client sync path getting worse? (weekly p99 sync lag, hours, line)**

```sql
SELECT date_trunc('week', occurred_at AT TIME ZONE 'UTC')::date AS week,
       round((percentile_cont(0.99) WITHIN GROUP (
                ORDER BY extract(epoch FROM occurred_at - (payload ->> 'updatedAt')::timestamptz)) / 3600)::numeric, 2) AS p99_sync_lag_hours
FROM domain_events
GROUP BY 1
ORDER BY 1;
```

Measured: p99 sits between about 5.8 and 11.0 hours with no upward trend. Offline-first clients queue writes, so hours of lag are structural, not a regression. Verdict: do not spend the week here.

**2. Is the relay dropping events? (outbox rows stuck over 1 hour, single number + oldest age)**

```sql
SELECT count(*)                                                                    AS stuck_outbox_rows,
       round((extract(epoch FROM max(a.ts - de.occurred_at)) / 3600)::numeric, 1)  AS oldest_hours
FROM domain_events de
CROSS JOIN (SELECT max(occurred_at) AS ts FROM domain_events) a
WHERE de.published_at IS NULL
  AND a.ts - de.occurred_at > interval '1 hour';
```

Measured: 227 rows, the oldest about 2,140 hours (89 days) old. These were never retried: events that never reach any consumer are a correctness bug, not latency.

**3. Is a consumer behind? (unprocessed over 1 hour, per consumer, bar)**

```sql
SELECT pe_c.consumer_name,
       count(*) FILTER (WHERE pe.event_id IS NULL AND a.ts - de.published_at > interval '1 hour') AS unprocessed_over_1h
FROM (VALUES ('photo-processor'), ('dashboard-aggregator'), ('notifier')) pe_c(consumer_name)
CROSS JOIN (SELECT max(occurred_at) AS ts FROM domain_events) a
JOIN domain_events de ON de.event_type = 'photo.upserted' AND de.published_at IS NOT NULL
LEFT JOIN processed_events pe ON pe.event_id = de.id AND pe.consumer_name = pe_c.consumer_name
GROUP BY pe_c.consumer_name
ORDER BY unprocessed_over_1h DESC;
```

Measured: notifier 713, photo-processor 131, dashboard-aggregator 109. The notifier number is dominated by a stall that begins on the last morning of the data; that is a live incident, bigger in count but fixable by restart, while metric 2 is silent data loss.

**Reading the three together:** relay stuck rows first (silent loss), then the notifier stall (live), and leave the sync path alone (flat). The ranking is the decision; no single chart gives it.

**Why not more charts.** Daily event volume, sessions per actor, retention and funnel are real analyses (see `queries/`) but none changes which hop we fix this week, and every extra chart makes the customer re-derive the ranking. Throughput without an error or age dimension looks healthy while events are being lost, which is exactly the trap here: the stuck rows are 0.4% of volume. Alert on age (oldest stuck) and trend (p99), not on volume.

Caveat: the seed plants these conditions (about 0.4% stuck outbox rows, a notifier that stops processing 15 hours before the end, 1% random consumer misses), so the specific counts are properties of the synthetic data, not findings about a real system.
