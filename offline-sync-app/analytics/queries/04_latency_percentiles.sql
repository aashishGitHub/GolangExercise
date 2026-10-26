/*
BUSINESS QUESTION
  "How long does it take for a field write to become visible, get published, and get
  consumed - at the median and in the tail - and has the tail got worse in the last 2 weeks?"
*/
WITH as_of AS (
  SELECT max(occurred_at) AT TIME ZONE 'UTC' AS ts FROM domain_events
), samples AS (
  SELECT 'sync_lag' AS metric, event_type AS segment, occurred_at AS at,
         extract(epoch FROM occurred_at - (payload ->> 'updatedAt')::timestamptz) AS secs
  FROM domain_events
  UNION ALL
  SELECT 'publish_lag', event_type, occurred_at,
         extract(epoch FROM published_at - occurred_at)
  FROM domain_events
  WHERE published_at IS NOT NULL
  UNION ALL
  SELECT 'consume_lag', pe.consumer_name, pe.processed_at,
         extract(epoch FROM pe.processed_at - de.published_at)
  FROM processed_events pe
  JOIN domain_events de ON de.id = pe.event_id
)
SELECT metric,
       segment,
       count(*)                                                                    AS n,
       round((percentile_cont(0.50) WITHIN GROUP (ORDER BY secs))::numeric, 3)     AS p50_s,
       round((percentile_cont(0.90) WITHIN GROUP (ORDER BY secs))::numeric, 3)     AS p90_s,
       round((percentile_cont(0.99) WITHIN GROUP (ORDER BY secs))::numeric, 3)     AS p99_s,
       round((percentile_cont(0.99) WITHIN GROUP (ORDER BY secs)
                FILTER (WHERE at AT TIME ZONE 'UTC' >= (SELECT ts FROM as_of) - interval '14 days'))::numeric, 3) AS p99_s_last_14d
FROM samples
GROUP BY metric, segment
ORDER BY metric, segment;
/*
RESULT SHAPE
  One row per (metric, segment): sync_lag by event type, publish_lag by event type,
  consume_lag by consumer. Always p50 <= p90 <= p99; all >= 0.

SAY THIS IN THE INTERVIEW
  - percentile_cont is an ordered-set aggregate: WITHIN GROUP (ORDER BY x), interpolates
    between the two nearest values. percentile_disc returns an actual observed value.
  - FILTER (WHERE ...) goes AFTER the WITHIN GROUP clause and restricts which rows feed the
    aggregate; it is the clean way to get "all-time" and "last 14 days" in one pass.
  - Percentiles do not compose: you cannot average the p99s of two groups to get the pooled
    p99. Compute from raw rows, or keep a histogram/t-digest if you pre-aggregate.
  - The tail is the story here: sync_lag p50 is seconds, p99 is hours. Offline-first clients
    queue writes, so a mean (or p50) hides the stragglers. Report p99 to the customer.
  - sync_lag uses payload.updatedAt (device clock) vs occurred_at (server clock). Clock skew
    can produce small negatives in real data; guard with GREATEST(0, ...) or monitor it.
  - "Last 14 days" is computed on UTC wall-clock timestamps. timestamptz - interval '14 days'
    is calendar arithmetic in the SESSION time zone, so across a DST change it is not 336
    hours and the cut-off moves by an hour (a test that runs the query in two zones caught it).
  - Exact percentiles sort every group: O(n log n) memory/time. Fine at 50k rows; at 500M
    use approximate quantiles (t-digest / HdrHistogram) in the warehouse.
*/
