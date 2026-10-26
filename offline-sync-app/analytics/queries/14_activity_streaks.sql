/*
BUSINESS QUESTION
  "Who has the longest unbroken run of consecutive working days (a dedication signal, and a
  burnout risk when the run crosses weekends)?"
*/
WITH active_days AS (
  SELECT DISTINCT actor_sub, (occurred_at AT TIME ZONE 'UTC')::date AS day
  FROM domain_events
), numbered AS (
  SELECT actor_sub, day,
         day - (row_number() OVER (PARTITION BY actor_sub ORDER BY day))::int AS island
  FROM active_days
), streaks AS (
  SELECT actor_sub, island,
         min(day)  AS streak_start,
         max(day)  AS streak_end,
         count(*)  AS streak_days
  FROM numbered
  GROUP BY actor_sub, island
), ranked AS (
  SELECT *,
         row_number() OVER (PARTITION BY actor_sub ORDER BY streak_days DESC, streak_start) AS rn
  FROM streaks
)
SELECT actor_sub, streak_days AS longest_streak_days, streak_start, streak_end
FROM ranked
WHERE rn = 1
ORDER BY longest_streak_days DESC, actor_sub;
/*
RESULT SHAPE
  One row per actor (25) with their longest run of consecutive calendar days having >= 1
  event. streak_end - streak_start + 1 = longest_streak_days.

SAY THIS IN THE INTERVIEW
  - The classic island trick: for consecutive days, day - row_number() is a constant. Both
    advance by 1 per row, so the difference only changes when a day is skipped.
  - DISTINCT first: row_number over raw events would advance several times per day and break
    the arithmetic. One row per (actor, day) is the precondition.
  - Calendar streak, so a weekend with no events ends it. Counting only weekdays needs a
    working-day calendar table; say that rather than silently approximating.
  - Tie for the longest streak: row_number with (streak_days DESC, streak_start) picks the
    earliest deterministically; rank() would return both.
*/
