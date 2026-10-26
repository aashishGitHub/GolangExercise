/*
BUSINESS QUESTION
  "Where is the photo evidence concentrated? Rank locations by photos and show each one's
  share of the total and the cumulative share (do 20% of locations hold 80% of the photos?)."
*/
WITH per_location AS (
  SELECT l.name AS location, count(p.id) AS photos
  FROM disaster_locations l
  JOIN site_assessments s ON s.location_id = l.id
  JOIN photos p           ON p.site_assessment_id = s.id
  GROUP BY l.id, l.name
)
SELECT location,
       photos,
       round(photos::numeric / sum(photos) OVER (), 4)                                    AS share,
       sum(photos) OVER (ORDER BY photos DESC, location
                         ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)                AS running_photos,
       round(sum(photos) OVER (ORDER BY photos DESC, location
                               ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)::numeric
             / sum(photos) OVER (), 4)                                                    AS cum_share,
       round(row_number() OVER (ORDER BY photos DESC, location)::numeric
             / count(*) OVER (), 4)                                                       AS pct_of_locations,
       sum(photos) OVER (ORDER BY photos DESC)                                            AS running_photos_default_frame
FROM per_location
ORDER BY photos DESC, location;
/*
RESULT SHAPE
  One row per location that has photos, biggest first. sum(share) ~ 1 (4-dp rounding);
  cum_share is non-decreasing and ends at 1.0; running_photos <= running_photos_default_frame.

SAY THIS IN THE INTERVIEW
  - share-of-total = value / sum(value) OVER (): an empty OVER () is the whole result set,
    so no self-join or scalar subquery is needed.
  - Running total = sum() OVER (ORDER BY ... ROWS UNBOUNDED PRECEDING). The last column shows
    the trap: with only ORDER BY photos DESC, the default frame is RANGE, so locations with
    equal photo counts are peers and ALL jump to the same running total (they include each
    other). The explicit ROWS frame plus a unique tiebreaker (location) fixes it.
  - Cumulative share is a Pareto curve: read the first row where cum_share >= 0.8 and look at
    pct_of_locations to answer the 80/20 question.
  - round() on a double precision needs a ::numeric cast in Postgres (round(x, n) exists only
    for numeric); sum(bigint) returns numeric, which is why the share columns work unchanged.
  - Rounded shares will not sum to exactly 1; never assert equality on rounded money/percent.
*/
