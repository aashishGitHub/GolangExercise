/*
BUSINESS QUESTION
  "After a field worker opens a site assessment, how long until the first photo is taken,
  and once they start, how fast do they shoot? Which sites never got a photo?"
*/
WITH photo_rows AS (
  SELECT s.id AS site_id,
         s.created_at AS site_created_at,
         p.created_at AS photo_at,
         min(p.created_at) OVER (PARTITION BY s.id)                      AS first_photo_at,
         lag(p.created_at) OVER (PARTITION BY s.id ORDER BY p.created_at, p.id) AS prev_photo_at
  FROM site_assessments s
  LEFT JOIN photos p ON p.site_assessment_id = s.id
), per_site AS (
  SELECT site_id,
         min(first_photo_at) - min(site_created_at) AS time_to_first_photo,
         count(photo_at)                            AS photos,
         percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM photo_at - prev_photo_at))
           FILTER (WHERE prev_photo_at IS NOT NULL) AS median_gap_s
  FROM photo_rows
  GROUP BY site_id
)
SELECT CASE WHEN time_to_first_photo IS NULL            THEN '6 no photo'
            WHEN time_to_first_photo <  interval '5 minutes'  THEN '1 < 5 min'
            WHEN time_to_first_photo <  interval '15 minutes' THEN '2 5-15 min'
            WHEN time_to_first_photo <  interval '1 hour'     THEN '3 15-60 min'
            WHEN time_to_first_photo <  interval '6 hours'    THEN '4 1-6 h'
            ELSE                                                   '5 >= 6 h' END AS bucket,
       count(*)                                                            AS sites,
       round(count(*)::numeric / sum(count(*)) OVER (), 4)                 AS share,
       round(avg(photos), 2)                                               AS avg_photos,
       round(percentile_cont(0.5) WITHIN GROUP (ORDER BY median_gap_s)::numeric, 1) AS median_gap_between_photos_s,
       min(extract(epoch FROM time_to_first_photo))::float8                AS min_ttfp_s
FROM per_site
GROUP BY 1
ORDER BY 1;
/*
RESULT SHAPE
  <= 6 rows (one per bucket, empty buckets absent). sites sum = count(site_assessments);
  shares sum to 1. median_gap_between_photos_s is NULL for sites with <2 photos.

SAY THIS IN THE INTERVIEW
  - Two window uses over the same partition: min(...) OVER (PARTITION BY site) puts the first
    photo time on every row without collapsing rows; lag() gives the previous photo so the
    per-photo gap is a plain subtraction. Both need the PARTITION; only lag needs ORDER BY.
  - LEFT JOIN keeps sites with zero photos. An INNER JOIN would silently drop exactly the
    sites the business cares about, and the shares would sum to 1 over the wrong population.
  - lag() ORDER BY has a tiebreaker (p.id) so identical timestamps give a stable answer.
  - Median not mean: delays are long-tailed (some photos upload the next day), the mean is
    dominated by them.
  - Pitfall: photo.created_at is the DEVICE clock (offline-first). Clock skew can make a
    negative gap; here min_ttfp_s is checked >= 0, in production you would too.
  - Index: photos(site_assessment_id) already exists (migration 000001); the query is a full
    scan + sort either way because it covers every site.
*/
