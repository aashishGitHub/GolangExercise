/*
BUSINESS QUESTION
  "Is the event pipeline keeping up? How many outbox rows are still unpublished and how old
  is the oldest, and for each consumer how many published events has it not processed yet?"
*/
WITH as_of AS (
  SELECT max(occurred_at) AS ts FROM domain_events
), subscriptions(consumer_name, event_type) AS (
  VALUES ('photo-processor',      'photo.upserted'),
         ('dashboard-aggregator', 'photo.upserted'),
         ('notifier',             'photo.upserted')
), outbox AS (
  SELECT 'outbox'::text AS kind,
         'outbox-relay'::text AS name,
         count(*)                                                         AS eligible,
         count(*) FILTER (WHERE de.published_at IS NULL)                  AS backlog,
         count(*) FILTER (WHERE de.published_at IS NULL
                            AND a.ts - de.occurred_at > interval '1 hour') AS backlog_over_1h,
         percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM a.ts - de.occurred_at))
           FILTER (WHERE de.published_at IS NULL)                         AS p50_age_s,
         max(extract(epoch FROM a.ts - de.occurred_at))
           FILTER (WHERE de.published_at IS NULL)                         AS oldest_age_s
  FROM domain_events de
  CROSS JOIN as_of a
), consumers AS (
  SELECT 'consumer'::text AS kind,
         s.consumer_name  AS name,
         count(*)                                                         AS eligible,
         count(*) FILTER (WHERE pe.event_id IS NULL)                      AS backlog,
         count(*) FILTER (WHERE pe.event_id IS NULL
                            AND a.ts - de.published_at > interval '1 hour') AS backlog_over_1h,
         percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM a.ts - de.published_at))
           FILTER (WHERE pe.event_id IS NULL)                             AS p50_age_s,
         max(extract(epoch FROM a.ts - de.published_at))
           FILTER (WHERE pe.event_id IS NULL)                             AS oldest_age_s
  FROM subscriptions s
  JOIN domain_events de ON de.event_type = s.event_type AND de.published_at IS NOT NULL
  LEFT JOIN processed_events pe ON pe.event_id = de.id AND pe.consumer_name = s.consumer_name
  CROSS JOIN as_of a
  GROUP BY s.consumer_name
)
SELECT kind, name, eligible, backlog, backlog_over_1h,
       round((p50_age_s / 60)::numeric, 1)    AS p50_age_min,
       round((oldest_age_s / 3600)::numeric, 2) AS oldest_age_h
FROM (SELECT * FROM outbox UNION ALL SELECT * FROM consumers) u
ORDER BY kind DESC, name;
/*
RESULT SHAPE
  4 rows: the outbox relay + one per consumer. backlog <= eligible; backlog_over_1h <=
  backlog; age columns NULL when backlog = 0. For the outbox, backlog equals
  count(*) WHERE published_at IS NULL (the table has a partial index for exactly that).

SAY THIS IN THE INTERVIEW
  - Two different lags: outbox lag = row written but not yet handed to the bus (relay is
    behind or stuck); consumer lag = published but no processed_events row for that consumer.
  - Consumer anti-join: LEFT JOIN ... ON (event_id, consumer_name) with pe.event_id IS NULL.
    Join on BOTH key columns: joining on event_id alone matches another consumer's row and
    hides the backlog. NOT EXISTS gives the same plan; NOT IN is a trap if the subquery can
    return NULL.
  - FILTER on the aggregates keeps zero-backlog consumers in the output (a WHERE pe IS NULL
    would drop a healthy consumer entirely, and "no row" looks like "no data").
  - Age is measured against the data's own max(occurred_at) so the result is reproducible; in
    production use now(). Say that you made that substitution on purpose.
  - Ages are a health signal, counts are a size signal: alert on oldest_age (a stuck poison
    event) as well as on backlog, because 3 events stuck for 40 days is small but wrong.
  - Subscriptions mirror the code (all three consumers handle only photo.upserted); if a
    consumer's EventTypes change, this VALUES list must change with it.
  - Duplicate events (see 11_dedupe_latest.sql) have distinct ids, so consumer idempotency by
    event id does not collapse them: each duplicate is processed again.
*/
