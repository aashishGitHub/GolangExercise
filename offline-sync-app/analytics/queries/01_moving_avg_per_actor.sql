/*
BUSINESS QUESTION
  "Is each field worker's sync activity trending up or down? Smooth the daily noise
  with a 7-day moving average per actor."
*/
WITH daily AS (
  SELECT actor_sub,
         (occurred_at AT TIME ZONE 'UTC')::date AS day,
         count(*)                               AS events
  FROM domain_events
  GROUP BY actor_sub, (occurred_at AT TIME ZONE 'UTC')::date
)
SELECT actor_sub,
       day,
       events,
       round(avg(events) OVER w_rows, 2)                     AS avg_rows_7,
       round(avg(events) OVER w_range, 2)                    AS avg_range_7_active_days,
       round(sum(events) OVER w_range / 7.0, 2)              AS avg_calendar_7,
       count(*) OVER w_range                                 AS active_days_in_window,
       day - first_value(day) OVER w_rows                    AS rows_frame_span_days
FROM daily
WINDOW w_rows  AS (PARTITION BY actor_sub ORDER BY day ROWS  BETWEEN 6 PRECEDING AND CURRENT ROW),
       w_range AS (PARTITION BY actor_sub ORDER BY day RANGE BETWEEN INTERVAL '6 days' PRECEDING AND CURRENT ROW)
ORDER BY actor_sub, day;
/*
RESULT SHAPE
  One row per (actor, day the actor had >=1 event). Days with no events have NO row.
  avg_rows_7               average of the last 7 ROWS (active days), however far back they reach
  avg_range_7_active_days  average over active days that fall inside the last 7 CALENDAR days
  avg_calendar_7           sum over the last 7 calendar days / 7 (idle days count as zero)
  rows_frame_span_days     how many calendar days the ROWS frame actually covers (> 6 = wrong)

SAY THIS IN THE INTERVIEW
  - ROWS counts physical rows; RANGE counts values of the ORDER BY key. The daily table is
    sparse (no row for idle days), so "6 PRECEDING" rows can reach back weeks.
  - RANGE with an interval offset (PG 11+) is the calendar window, but it still averages only
    the days that exist. A true "events per day over 7 days" is sum/7, or densify first with
    generate_series (see 13_gaps_silent_hours.sql).
  - Warm-up: the first 6 days of an actor's life divide by 7 anyway. Say so, or drop them.
  - Default frame when ORDER BY is present is RANGE UNBOUNDED PRECEDING..CURRENT ROW; peers
    (equal ORDER BY values) are included. Always write the frame explicitly.
  - Day bucket uses AT TIME ZONE 'UTC', not date_trunc on timestamptz, so the result does not
    change with the session TimeZone.
*/
