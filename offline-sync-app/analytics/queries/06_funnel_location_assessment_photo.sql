/*
BUSINESS QUESTION
  "Of the locations we opened, how many got a site assessment, and of those how many have
  photo evidence? Where in the funnel do we lose them, and is one region worse?"
*/
WITH loc AS (
  SELECT l.id,
         split_part(l.name, '-', 1)       AS region,
         count(s.id)                      AS sites,
         count(ph.site_assessment_id)     AS sites_with_photo
  FROM disaster_locations l
  LEFT JOIN site_assessments s ON s.location_id = l.id
  LEFT JOIN (SELECT DISTINCT site_assessment_id FROM photos) ph ON ph.site_assessment_id = s.id
  GROUP BY l.id, l.name
)
SELECT COALESCE(region, 'ALL')                                                    AS region,
       count(*)                                                                   AS locations,
       count(*) FILTER (WHERE sites > 0)                                          AS with_assessment,
       count(*) FILTER (WHERE sites_with_photo > 0)                               AS with_photo,
       round(count(*) FILTER (WHERE sites > 0)::numeric / count(*), 4)            AS conv_location_to_assessment,
       round(count(*) FILTER (WHERE sites_with_photo > 0)::numeric
             / nullif(count(*) FILTER (WHERE sites > 0), 0), 4)                   AS conv_assessment_to_photo,
       sum(sites)                                                                 AS sites,
       sum(sites_with_photo)                                                      AS sites_with_photo,
       round(sum(sites_with_photo)::numeric / nullif(sum(sites), 0), 4)           AS site_photo_coverage
FROM loc
GROUP BY ROLLUP (region)
ORDER BY GROUPING(region), region;
/*
RESULT SHAPE
  8 region rows (R1..R8) + 1 'ALL' row. locations >= with_assessment >= with_photo on every
  row; conversions and coverage in [0,1]; the ALL row equals the sum of the region rows.
  The last three columns re-state stage 3 at ASSESSMENT grain.

SAY THIS IN THE INTERVIEW
  - Count DISTINCT ENTITIES per stage, not joined rows. location x assessments x photos fans
    out (one location x 12 sites x 8 photos = 96 rows); counting that makes a "funnel" that
    grows. The photo side is pre-collapsed with SELECT DISTINCT site_assessment_id for this.
  - Grain changes the story: at LOCATION grain stage 2 -> 3 is ~99% (any one photo counts),
    but at ASSESSMENT grain ~12% of assessments have no photo. Say which grain you report.
  - Integer division: count(*)/count(*) is integer division in Postgres and returns 0 or 1.
    Cast one side to numeric before dividing (done here).
  - nullif(denominator, 0) turns a divide-by-zero (region with no assessments) into NULL
    instead of aborting the whole query.
  - ROLLUP adds the grand-total row; GROUPING(region) tells a real NULL region from the
    rollup row, and sorts the total last.
  - "Ever reached the stage" funnel, not a time-boxed one. "Photo within 7 days of opening"
    puts the timestamps into the join predicate.
  - Seeded leaks: 12% of locations are "scouted, never assessed" (plus ones opened too late to
    be assessed), and some surveyors never take photos.
  - Indexes: site_assessments(location_id) and photos(site_assessment_id) exist (migration
    000001); the DISTINCT over photos is a full scan, cheap at 15k rows.
*/
