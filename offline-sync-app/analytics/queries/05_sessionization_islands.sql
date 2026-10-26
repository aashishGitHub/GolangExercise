/*
BUSINESS QUESTION
  "How many work sessions does each field worker have, how long are they, and how much do
  they sync per session? A new session starts after 30 minutes of silence."
*/
WITH ordered AS (
  SELECT id, actor_sub, occurred_at,
         lag(occurred_at) OVER (PARTITION BY actor_sub ORDER BY occurred_at, id) AS prev_at
  FROM domain_events
), flagged AS (
  SELECT *,
         CASE WHEN prev_at IS NULL OR occurred_at - prev_at > interval '30 minutes' THEN 1 ELSE 0 END AS starts_session
  FROM ordered
), numbered AS (
  SELECT *,
         sum(starts_session) OVER (PARTITION BY actor_sub ORDER BY occurred_at, id
                                   ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS session_no
  FROM flagged
), sessions AS (
  SELECT actor_sub, session_no,
         count(*)                           AS events,
         max(occurred_at) - min(occurred_at) AS duration
  FROM numbered
  GROUP BY actor_sub, session_no
)
SELECT actor_sub,
       count(*)                                                                   AS sessions,
       sum(events)                                                                AS total_events,
       round(avg(events), 1)                                                      AS avg_events_per_session,
       round((percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM duration)))::numeric / 60, 1) AS median_session_min,
       round(max(extract(epoch FROM duration))::numeric / 60, 1)                  AS longest_session_min
FROM sessions
GROUP BY actor_sub
ORDER BY total_events DESC, actor_sub;
/*
RESULT SHAPE
  One row per actor (25). sum(total_events) = count(domain_events): every event belongs to
  exactly one session. sessions >= 1, durations >= 0.

SAY THIS IN THE INTERVIEW
  - Gaps-and-islands in three steps: (1) lag() the previous timestamp, (2) flag rows where
    the gap exceeds the threshold, (3) running sum of the flags = island/session number.
  - ORDER BY includes the unique id, so the order is total and no two rows are peers. ROWS is
    stated anyway; without the id tiebreaker the default RANGE frame would give rows with
    equal timestamps the same running value, and the lag() order would be non-deterministic.
  - The first event of an actor has prev_at NULL, so "gap > 30 min" is NULL, not true. In a CASE
    it falls to ELSE (session numbering would start at 0); in a WHERE it would drop the row.
    The explicit IS NULL branch makes the first event start session 1.
  - Threshold boundary: exactly 30:00 stays in the session (> not >=). State it.
  - Sessionising on occurred_at is SERVER time: an offline worker's whole day collapses into
    one sync burst. For field-work sessions, use the device timestamp from the payload.
  - Classic variant for consecutive days: date - row_number() over (partition by actor order
    by date) is constant within a streak; group by that constant.
  - Cost (checked with EXPLAIN): ONE Sort on (actor_sub, occurred_at, id) feeds both WindowAgg
    nodes because the PARTITION BY/ORDER BY match; an index on those columns could remove it.
*/
