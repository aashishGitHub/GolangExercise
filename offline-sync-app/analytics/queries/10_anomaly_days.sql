/*
BUSINESS QUESTION
  "Which days had abnormal sync volume - a surge we should staff for, or a bug flooding the
  system? Flag any day above mean + 2 standard deviations."
*/
WITH bounds AS (
  SELECT min((occurred_at AT TIME ZONE 'UTC')::date) AS first_day,
         max((occurred_at AT TIME ZONE 'UTC')::date) AS last_day
  FROM domain_events
), daily AS (
  SELECT d.day, count(de.id) AS events
  FROM bounds b
  CROSS JOIN LATERAL generate_series(b.first_day::timestamp, b.last_day::timestamp, interval '1 day') AS g(ts)
  CROSS JOIN LATERAL (SELECT g.ts::date AS day) d
  LEFT JOIN domain_events de ON (de.occurred_at AT TIME ZONE 'UTC')::date = d.day
  GROUP BY d.day
), scored AS (
  SELECT day,
         events,
         extract(isodow FROM day)::int                                           AS isodow,
         avg(events)         OVER ()                                             AS mean_all,
         stddev_samp(events) OVER ()                                             AS sd_all,
         count(*)            OVER w28                                            AS n_28,
         avg(events)         OVER w28                                            AS mean_28,
         stddev_samp(events) OVER w28                                            AS sd_28,
         count(*)            OVER wdow                                           AS n_dow,
         avg(events)         OVER wdow                                           AS mean_dow,
         stddev_samp(events) OVER wdow                                           AS sd_dow
  FROM daily
  WINDOW w28  AS (ORDER BY day ROWS BETWEEN 28 PRECEDING AND 1 PRECEDING),
         wdow AS (PARTITION BY extract(isodow FROM day) ORDER BY day ROWS BETWEEN 4 PRECEDING AND 1 PRECEDING)
)
SELECT day,
       isodow,
       events,
       round(mean_all, 1)                                              AS mean_all,
       round(sd_all, 1)                                                AS sd_all,
       COALESCE(events > mean_all + 2 * sd_all, false)                 AS anomaly_all_days,
       round(((events - mean_28) / NULLIF(sd_28, 0))::numeric, 2)      AS z_trailing_28d,
       COALESCE(n_28 >= 14 AND events > mean_28 + 2 * sd_28, false)    AS anomaly_trailing_28d,
       COALESCE(n_dow = 4 AND events > mean_dow + 2 * sd_dow, false)   AS anomaly_same_weekday
FROM scored
ORDER BY day;
/*
RESULT SHAPE
  One row per calendar day between the first and last event (90), zero-event days included.
  Three boolean flags from three baselines. Planted: 2026-07-23 (surge, 3x field activity)
  and 2026-08-28 (client bug re-stamping updated_at, ~3k extra writes). Measured on the
  seeded data: the storm is flagged by all three baselines; the surge (1443 events) is flagged
  ONLY by the trailing-28d baseline - the all-days threshold is 563 + 2 x 460 = 1484, so the
  surge hides inside its own sigma, and the same-weekday baseline has only 3 prior Thursdays.

SAY THIS IN THE INTERVIEW
  - avg() / stddev_samp() with an empty OVER () are whole-table window stats attached to each
    row, so the comparison needs no join. stddev_samp divides by n-1 (sample), stddev_pop by n.
  - Baseline choice is the real answer. all-days includes the anomaly in its own mean/sd
    (masking: a big spike inflates sigma and can hide itself). Trailing 28 days with
    "BETWEEN 28 PRECEDING AND 1 PRECEDING" EXCLUDES today, so a day is judged against history.
  - Weekly seasonality: weekend volume is ~5-10% of weekday volume, so a flat baseline has a
    huge sigma. Comparing to the same weekday of the previous 4 weeks (PARTITION BY dow)
    removes it - at the cost of only 4 samples per baseline (noisy sigma; the first weeks
    have NULL baselines, hence COALESCE to false).
  - Empty days must exist as rows (generate_series + LEFT JOIN) or the mean is computed over
    busy days only and every quiet day is invisible.
  - Any NULL comparison is NULL, not false: COALESCE the flag or the NULL rows vanish from a
    WHERE flag filter and read as "unknown".
  - 2-sigma flags ~5% of normal days if the data were Gaussian; counts are not, so treat the
    flag as "look here", not "incident". Tune the threshold with a backtest.
*/
