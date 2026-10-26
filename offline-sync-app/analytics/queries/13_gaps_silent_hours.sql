/*
BUSINESS QUESTION
  "Was there any stretch during working hours when nothing synced at all - an outage the
  customer felt - and how long was it?"
*/
WITH bounds AS (
  SELECT date_trunc('hour', min(occurred_at AT TIME ZONE 'UTC')) AS first_hour,
         date_trunc('hour', max(occurred_at AT TIME ZONE 'UTC')) AS last_hour
  FROM domain_events
), hours AS (
  SELECT g.h
  FROM bounds b
  CROSS JOIN LATERAL generate_series(b.first_hour, b.last_hour, interval '1 hour') AS g(h)
), hourly AS (
  SELECT h.h AS hour_start, count(de.id) AS events
  FROM hours h
  LEFT JOIN domain_events de
         ON de.occurred_at >= h.h AT TIME ZONE 'UTC'
        AND de.occurred_at <  (h.h + interval '1 hour') AT TIME ZONE 'UTC'
  GROUP BY h.h
), silent AS (
  SELECT hour_start,
         hour_start - row_number() OVER (ORDER BY hour_start) * interval '1 hour' AS island
  FROM hourly
  WHERE events = 0
    AND extract(isodow FROM hour_start) <= 5
    AND extract(hour FROM hour_start) BETWEEN 7 AND 18
)
SELECT min(hour_start)                          AS silent_from_utc,
       max(hour_start) + interval '1 hour'      AS silent_until_utc,
       count(*)                                 AS silent_hours
FROM silent
GROUP BY island
HAVING count(*) >= 2
ORDER BY silent_hours DESC, silent_from_utc;
/*
RESULT SHAPE
  One row per run of >= 2 consecutive empty weekday working hours (07:00-18:59 UTC), longest
  first. silent_until is exclusive. The seeded 6-hour outage is 2026-08-13 10:00-16:00.

SAY THIS IN THE INTERVIEW
  - Group by date_trunc and you only get buckets that HAD events: the outage is invisible,
    because "no rows" is not "zero". generate_series makes every bucket exist and LEFT JOIN
    turns the missing ones into count 0 (count(de.id), not count(*), or empty buckets count 1).
  - AT TIME ZONE 'UTC' converts timestamptz -> timestamp, so the series is wall-clock UTC and
    has no DST jumps. generate_series over timestamptz with day steps follows the session
    TimeZone and can produce 23/25-hour days.
  - Join on a half-open RANGE (>= start AND < start+1h), not on date_trunc(col) = bucket: the
    range form can use a btree on occurred_at, the function form cannot.
  - Islands again: number the silent hours, subtract n hours; consecutive hours collapse to
    the same constant. Restricting to working hours BEFORE numbering means a silence that
    crosses the 18:59 -> 07:00 boundary is reported as two runs; say that limitation.
  - Weekends/nights are excluded on purpose: empty is normal there. A silence rule needs a
    business-hours definition, otherwise it just reports Sundays.
  - The burst after the gap (the queued offline writes flushing at 16:00) is not a bug; it is
    why sync_lag p99 is in hours (see 04).
*/
