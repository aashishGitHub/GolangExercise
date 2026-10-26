/*
BUSINESS QUESTION
  "For each disaster location, who are the top 3 assessors by number of site assessments?"
  (Customer wants to recognise local leads - and ties are the whole problem.)
*/
WITH per_actor AS (
  SELECT location_id,
         created_by       AS actor_sub,
         count(*)         AS assessments,
         max(created_at)  AS last_assessment_at
  FROM site_assessments
  GROUP BY location_id, created_by
), ranked AS (
  SELECT location_id, actor_sub, assessments,
         row_number() OVER (PARTITION BY location_id
                            ORDER BY assessments DESC, last_assessment_at DESC, actor_sub) AS rn,
         rank()       OVER (PARTITION BY location_id ORDER BY assessments DESC)             AS rnk,
         dense_rank() OVER (PARTITION BY location_id ORDER BY assessments DESC)             AS drnk
  FROM per_actor
)
SELECT l.name AS location, r.actor_sub, r.assessments, r.rn, r.rnk, r.drnk
FROM ranked r
JOIN disaster_locations l ON l.id = r.location_id
WHERE r.drnk <= 3
ORDER BY l.name, r.rn;
/*
RESULT SHAPE
  Every (location, actor) row whose dense_rank <= 3. Filtering on rn <= 3 keeps exactly <= 3
  rows per location; rnk <= 3 keeps >= that (ties at the boundary all stay); drnk <= 3 keeps
  every actor in the top 3 DISTINCT counts, so it can return many more than 3 people.
  Always drnk <= rnk <= rn on a row.

SAY THIS IN THE INTERVIEW
  - row_number: unique 1..n, ties broken arbitrarily unless you add a tiebreaker. Add one
    (here: most recent assessment, then actor id) or the answer changes between runs/plans.
  - rank: ties share a rank and the next rank skips (1,2,2,4). "Top 3" can return 4+ rows.
  - dense_rank: ties share a rank, no gaps (1,2,2,3). "Top 3 distinct scores".
  - Which one is right is a business decision: "exactly 3 badges" -> row_number with a
    stated tiebreak; "everyone tied for a podium place" -> rank.
  - You cannot filter on a window function in WHERE; compute it in a CTE/subquery first.
  - Index note: the aggregate scans site_assessments once; (location_id, created_by) would
    allow a sorted GroupAggregate but at 2k rows the planner will hash anyway.
*/
