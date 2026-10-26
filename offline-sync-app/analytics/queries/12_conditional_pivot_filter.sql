/*
BUSINESS QUESTION
  "Show me damage by region as a table: how many photos are good / moderate / bad, and which
  region has the worst share of 'bad'?"
*/
SELECT COALESCE(split_part(l.name, '-', 1), 'ALL')                                 AS region,
       count(*) FILTER (WHERE p.condition = 'good')                                AS good,
       count(*) FILTER (WHERE p.condition = 'moderate')                            AS moderate,
       count(*) FILTER (WHERE p.condition = 'bad')                                 AS bad,
       count(*)                                                                    AS total,
       round(count(*) FILTER (WHERE p.condition = 'bad')::numeric / count(*), 4)   AS pct_bad,
       count(*) FILTER (WHERE p.condition = 'bad'
                          AND p.updated_at AT TIME ZONE 'UTC' >= (SELECT max(updated_at) AT TIME ZONE 'UTC' FROM photos) - interval '14 days') AS bad_last_14d
FROM photos p
JOIN site_assessments s  ON s.id = p.site_assessment_id
JOIN disaster_locations l ON l.id = s.location_id
GROUP BY ROLLUP (split_part(l.name, '-', 1))
ORDER BY GROUPING(split_part(l.name, '-', 1)), pct_bad DESC;
/*
RESULT SHAPE
  8 region rows (worst pct_bad first) + 1 'ALL' row last. good + moderate + bad = total;
  pct_bad in [0,1]; total sums to count(photos).

SAY THIS IN THE INTERVIEW
  - Conditional aggregation = a pivot without crosstab(): one pass, one aggregate per output
    column, each with its own FILTER (WHERE ...). tablefunc's crosstab needs an extension and
    a fixed, pre-declared column list; this needs neither.
  - FILTER vs CASE: sum(CASE WHEN c='bad' THEN 1 END) returns NULL when no row matches;
    count(*) FILTER returns 0. FILTER also reads as what it means and works for any aggregate.
  - The columns are hard-coded from the CHECK constraint (good/moderate/bad). A new condition
    value silently lands in no column but still counts in total: good+moderate+bad = total is
    the invariant that catches it (the test asserts it).
  - pct_bad is count/count cast to numeric; integer division would give 0.
  - Rows = latest state of each photo (photos.condition), not event history. Re-tagging in
    the event log (condition changes) is a different question; do not mix the two tables.
  - Plan: three-way join over small tables; hash joins, one pass. Nothing to index at this
    size.
*/
