/*
BUSINESS QUESTION
  "Of the field workers who started in a given week, what share is still syncing 1, 2, 3...
  weeks later? Are newer cohorts sticking better than older ones?"
*/
WITH activity AS (
  SELECT DISTINCT actor_sub,
         date_trunc('week', occurred_at AT TIME ZONE 'UTC')::date AS week
  FROM domain_events
), last_complete AS (
  SELECT (date_trunc('week', max(occurred_at AT TIME ZONE 'UTC')) - interval '7 days')::date AS week
  FROM domain_events
), cohort AS (
  SELECT actor_sub, min(week) AS cohort_week
  FROM activity
  GROUP BY actor_sub
), cohort_size AS (
  SELECT cohort_week, count(*) AS size
  FROM cohort
  GROUP BY cohort_week
), grid AS (
  SELECT cs.cohort_week, cs.size, n.n
  FROM cohort_size cs
  CROSS JOIN last_complete lc
  CROSS JOIN LATERAL generate_series(0, (lc.week - cs.cohort_week) / 7) n(n)
)
SELECT g.cohort_week,
       g.size                                        AS cohort_size,
       g.n                                           AS weeks_since_start,
       count(a.actor_sub)                            AS active_actors,
       round(count(a.actor_sub)::numeric / g.size, 4) AS retention
FROM grid g
JOIN cohort c       ON c.cohort_week = g.cohort_week
LEFT JOIN activity a ON a.actor_sub = c.actor_sub AND a.week = g.cohort_week + g.n * 7
GROUP BY g.cohort_week, g.size, g.n
ORDER BY g.cohort_week, g.n;
/*
RESULT SHAPE
  One row per (cohort week, weeks since start) - a complete triangle, zero-retention cells
  included. weeks_since_start = 0 always has retention 1.0. 0 <= retention <= 1.

SAY THIS IN THE INTERVIEW
  - Cohort = week of an actor's first event (min over activity). Retention(n) = share of the
    cohort active again exactly n weeks later ("classic" retention, not "rolling/any time
    after").
  - The grid (cohort x n from generate_series) is what makes zeros appear as 0 instead of the
    row being absent. Without it a dead cohort looks like missing data, not 0% retention.
  - Right-censoring: the last, partial week is excluded (last_complete). Counting it would
    make every cohort's final cell look like churn that has not happened yet.
  - date_trunc('week', ...) truncates to MONDAY (ISO). It is applied after AT TIME ZONE 'UTC'
    so the week boundary does not depend on the session TimeZone.
  - (date - date) is an integer number of days, so / 7 is integer division on purpose.
  - Small-n warning: 25 actors means cohorts of 1-8 people; one person moves a cell by 12-100
    points. Don't over-read the matrix; say what the denominator is.
  - Definition choice matters: "active" = any domain event. For a product decision, "active"
    should be a meaningful action (a photo upload), not any heartbeat.
*/
