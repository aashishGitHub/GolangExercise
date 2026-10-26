BEGIN;
SET LOCAL TIME ZONE 'UTC';

DO $$
BEGIN
  IF current_database() !~ 'analytics' THEN
    RAISE EXCEPTION 'refusing to seed database "%": its name must contain "analytics"', current_database();
  END IF;
END $$;

TRUNCATE processed_events, domain_events, location_stats, ws_connections, photos, site_assessments, disaster_locations;

CREATE OR REPLACE FUNCTION pg_temp.u(k text) RETURNS float8 LANGUAGE sql IMMUTABLE AS
$$ SELECT (('x' || substr(md5(k), 1, 8))::bit(32)::bigint)::float8 / 4294967296.0 $$;

CREATE OR REPLACE FUNCTION pg_temp.z(k text) RETURNS float8 LANGUAGE sql IMMUTABLE AS
$$ SELECT sqrt(-2 * ln(1 - pg_temp.u(k || ':a'))) * cos(2 * pi() * pg_temp.u(k || ':b')) $$;

CREATE OR REPLACE FUNCTION pg_temp.ex(k text, mean float8) RETURNS float8 LANGUAGE sql IMMUTABLE AS
$$ SELECT -mean * ln(1 - pg_temp.u(k)) $$;

CREATE OR REPLACE FUNCTION pg_temp.t0() RETURNS timestamptz LANGUAGE sql IMMUTABLE AS
$$ SELECT timestamptz '2026-07-01 00:00:00+00' $$;

CREATE OR REPLACE FUNCTION pg_temp.t_end() RETURNS timestamptz LANGUAGE sql STABLE AS
$$ SELECT pg_temp.t0() + interval '90 days' $$;

CREATE OR REPLACE FUNCTION pg_temp.outage_start() RETURNS timestamptz LANGUAGE sql STABLE AS
$$ SELECT pg_temp.t0() + interval '43 days 10 hours' $$;

CREATE OR REPLACE FUNCTION pg_temp.outage_end() RETURNS timestamptz LANGUAGE sql STABLE AS
$$ SELECT pg_temp.t0() + interval '43 days 16 hours' $$;

CREATE OR REPLACE FUNCTION pg_temp.after_outage(ts timestamptz, k text) RETURNS timestamptz LANGUAGE sql STABLE AS
$$ SELECT CASE WHEN ts >= pg_temp.outage_start() AND ts < pg_temp.outage_end()
               THEN pg_temp.outage_end() + interval '20 minutes' * pg_temp.u(k || ':outage')
               ELSE ts END $$;

CREATE OR REPLACE FUNCTION pg_temp.tod(k text) RETURNS interval LANGUAGE sql IMMUTABLE AS
$$ SELECT interval '7 hours' + interval '12 hours' * (pg_temp.u(k || ':t1') + pg_temp.u(k || ':t2')) / 2 $$;

CREATE OR REPLACE FUNCTION pg_temp.sync_lag(k text, offline boolean) RETURNS interval LANGUAGE sql IMMUTABLE AS
$$ SELECT interval '1 second' * CASE
            WHEN offline AND pg_temp.u(k || ':off') < 0.6 THEN least(pg_temp.ex(k || ':lag', 10800), 64800)
            ELSE 2 * exp(0.9 * pg_temp.z(k || ':lag'))
          END $$;

CREATE OR REPLACE FUNCTION pg_temp.add_workdays(d timestamptz, n int) RETURNS timestamptz LANGUAGE sql STABLE AS
$$ SELECT date_trunc('day', d, 'UTC') - (extract(isodow FROM d)::int - 1) * interval '1 day'
          + ((least(extract(isodow FROM d)::int, 5) - 1 + n) / 5) * interval '7 days'
          + ((least(extract(isodow FROM d)::int, 5) - 1 + n) % 5) * interval '1 day' $$;

CREATE OR REPLACE FUNCTION pg_temp.client_ts(created_at timestamptz, v int, k text) RETURNS timestamptz LANGUAGE sql STABLE AS
$$ SELECT CASE WHEN v = 0 THEN created_at
          ELSE pg_temp.add_workdays(created_at, ceil(v * (1 + pg_temp.ex(k || ':rate', 1.2)))::int)
               + interval '7 hours' + v * interval '1 hour' + interval '30 minutes' * pg_temp.u(k || ':tod:' || v)
          END $$;

CREATE OR REPLACE FUNCTION pg_temp.server_ts(client_ts timestamptz, offline boolean, k text) RETURNS timestamptz LANGUAGE sql STABLE AS
$$ SELECT pg_temp.after_outage(client_ts + pg_temp.sync_lag(k, offline), k) $$;

CREATE TEMP TABLE t_actor ON COMMIT DROP AS
SELECT a.rank, a.sub, a.weight, a.join_day,
       CASE WHEN a.rank <= 8 OR pg_temp.u('churn:' || a.rank) < 0.55 THEN 9999
            ELSE a.join_day + 14 + floor(pg_temp.u('life:' || a.rank) * 40)::int END AS churn_day,
       CASE WHEN a.rank <= 5 THEN 0.95 ELSE 0.30 + 0.40 * pg_temp.u('act:' || a.rank) END AS active_p,
       pg_temp.u('off:' || a.rank) < 0.30 AS offline_heavy,
       a.rank <= 5 OR pg_temp.u('cam:' || a.rank) >= 0.40 AS takes_photos
FROM (
  SELECT i AS rank,
         'actor-' || lpad(i::text, 2, '0') AS sub,
         power(i, -0.7) AS weight,
         CASE WHEN i <= 3 THEN 0 ELSE floor(pg_temp.u('join:' || i) * 50)::int END AS join_day
  FROM generate_series(1, 25) i
) a;

CREATE TEMP TABLE t_actor_day ON COMMIT DROP AS
WITH cand AS (
  SELECT a.rank, a.sub, d.day, a.weight, a.active_p,
         extract(isodow FROM pg_temp.t0() + d.day * interval '1 day')::int AS dow
  FROM t_actor a
  JOIN generate_series(0, 89) d(day) ON d.day >= a.join_day AND d.day < a.churn_day
), active AS (
  SELECT c.rank, c.sub, c.day,
         c.weight * (1.3 - 0.6 * c.day / 90.0) * CASE WHEN c.day = 22 THEN 3 ELSE 1 END
           * exp(0.5 * pg_temp.z('vol:' || c.rank || ':' || c.day)) AS wt
  FROM cand c
  WHERE pg_temp.u('act:' || c.rank || ':' || c.day)
        < least(1.0, c.active_p
                     * CASE c.dow WHEN 6 THEN 0.25 WHEN 7 THEN 0.10 ELSE 1.0 END
                     * CASE WHEN c.day = 22 THEN 1.5 ELSE 1.0 END)
)
SELECT rank, sub, day, wt,
       least(1.0, sum(wt) OVER (ORDER BY day, rank ROWS UNBOUNDED PRECEDING) / sum(wt) OVER ()) AS cdf
FROM active;
CREATE INDEX ON t_actor_day (cdf);

CREATE TEMP TABLE t_ver (
  aggregate_type text NOT NULL,
  aggregate_id   uuid NOT NULL,
  v              int  NOT NULL,
  actor_sub      text NOT NULL,
  offline_heavy  boolean NOT NULL,
  client_ts      timestamptz NOT NULL,
  server_ts      timestamptz NOT NULL,
  payload        jsonb NOT NULL,
  PRIMARY KEY (aggregate_id, v)
) ON COMMIT DROP;

CREATE TEMP TABLE t_loc ON COMMIT DROP AS
SELECT l.k, l.id, l.sub, a.offline_heavy, l.created_at, l.name, l.lat, l.lon,
       CASE WHEN pg_temp.u('dead:' || l.k) < 0.12 THEN 0.0 ELSE exp(pg_temp.z('pop:' || l.k)) END AS pop_w,
       pg_temp.u('sev:' || (l.k % 8)) AS sev
FROM (
  SELECT k, md5('loc:' || k)::uuid AS id, ad.sub,
         pg_temp.t0() + ad.day * interval '1 day' + pg_temp.tod('loc:' || k) AS created_at,
         'R' || (1 + k % 8) || '-' || lpad(k::text, 3, '0') AS name,
         round((8 + 2.5 * (k % 8) + pg_temp.u('lat:' || k))::numeric, 5)::float8 AS lat,
         round((-80 + 3.0 * (k % 8) + pg_temp.u('lon:' || k))::numeric, 5)::float8 AS lon
  FROM generate_series(1, 200) k
  CROSS JOIN LATERAL (
    SELECT sub, day FROM t_actor_day
    WHERE cdf >= power(pg_temp.u('locpick:' || k), 2)
    ORDER BY cdf LIMIT 1
  ) ad
) l
JOIN t_actor a ON a.sub = l.sub;

INSERT INTO t_ver
SELECT 'location', l.id, v.v, l.sub, l.offline_heavy, c.cts,
       pg_temp.server_ts(c.cts, l.offline_heavy, l.id::text || ':' || v.v),
       jsonb_build_object('id', l.id, 'name', l.name,
                          'latitude', l.lat + v.v * 0.0001, 'longitude', l.lon + v.v * 0.0001,
                          'createdAt', l.created_at, 'updatedAt', c.cts)
FROM t_loc l
CROSS JOIN LATERAL generate_series(0, least(3, floor(pg_temp.ex('lupd:' || l.id::text, 0.8))::int)) v(v)
CROSS JOIN LATERAL (SELECT pg_temp.client_ts(l.created_at, v.v, l.id::text) AS cts) c;

DELETE FROM t_ver t
WHERE EXISTS (SELECT 1 FROM t_ver x
              WHERE x.aggregate_id = t.aggregate_id AND x.v <= t.v AND x.server_ts >= pg_temp.t_end());

CREATE TEMP TABLE t_site ON COMMIT DROP AS
SELECT b.k, b.id, b.sub, a.offline_heavy, a.takes_photos, b.created_at, loc.id AS location_id, loc.server0 AS parent_server0,
       loc.sev, loc.lat, loc.lon
FROM (
  SELECT k, md5('site:' || k)::uuid AS id, ad.sub,
         pg_temp.t0() + ad.day * interval '1 day' + pg_temp.tod('site:' || k) AS created_at
  FROM generate_series(1, 2000) k
  CROSS JOIN LATERAL (
    SELECT sub, day FROM t_actor_day
    WHERE cdf >= pg_temp.u('sitepick:' || k)
    ORDER BY cdf LIMIT 1
  ) ad
) b
JOIN t_actor a ON a.sub = b.sub
CROSS JOIN LATERAL (
  SELECT l.id, l.sev, l.lat, l.lon, lv.server_ts AS server0
  FROM t_loc l
  JOIN t_ver lv ON lv.aggregate_id = l.id AND lv.v = 0
  WHERE l.pop_w > 0
    AND l.created_at <= b.created_at
    AND (l.sub = b.sub OR lv.server_ts <= b.created_at)
  ORDER BY -ln(1 - pg_temp.u('sloc:' || b.k || ':' || l.k)) / l.pop_w
  LIMIT 1
) loc;

INSERT INTO t_ver
SELECT 'site_assessment', s.id, v.v, s.sub, s.offline_heavy, c.cts,
       CASE WHEN v.v = 0 THEN greatest(raw.srv, s.parent_server0 + interval '1 second') ELSE raw.srv END,
       jsonb_build_object('id', s.id, 'locationId', s.location_id,
                          'name', 'Assessment ' || lpad(s.k::text, 4, '0'),
                          'notes', CASE WHEN v.v = 0 THEN ''
                                        ELSE 'Rev ' || v.v || ': ' ||
                                             (ARRAY['roof damage', 'flooding', 'structural cracks',
                                                    'road access blocked', 'utilities offline'])
                                               [1 + floor(5 * pg_temp.u(s.id::text || ':note:' || v.v))::int] END,
                          'createdAt', s.created_at, 'updatedAt', c.cts)
FROM t_site s
CROSS JOIN LATERAL generate_series(0, least(8, floor(pg_temp.ex('supd:' || s.id::text, 4.0))::int)) v(v)
CROSS JOIN LATERAL (SELECT pg_temp.client_ts(s.created_at, v.v, s.id::text) AS cts) c
CROSS JOIN LATERAL (SELECT pg_temp.server_ts(c.cts, s.offline_heavy, s.id::text || ':' || v.v) AS srv) raw;

DELETE FROM t_ver t
WHERE EXISTS (SELECT 1 FROM t_ver x
              WHERE x.aggregate_id = t.aggregate_id AND x.v <= t.v AND x.server_ts >= pg_temp.t_end());

CREATE TEMP TABLE t_photo ON COMMIT DROP AS
SELECT md5('photo:' || s.k || ':' || j.j)::uuid AS id, s.id AS site_id, s.sub, s.offline_heavy,
       s.parent_server0, s.sev, s.lat, s.lon,
       s.created_at + least(
         interval '8 minutes' * exp(1.2 * pg_temp.z('pd1:' || s.k))
           + CASE WHEN pg_temp.u('pd2:' || s.k) < 0.02 THEN interval '20 hours' ELSE interval '0' END
           + (j.j - 1) * interval '45 seconds' * (0.5 + pg_temp.u('pd3:' || s.k || ':' || j.j)),
         interval '30 hours') AS created_at
FROM (
  SELECT st.k, st.id, st.sub, st.offline_heavy, st.takes_photos, st.sev, st.lat, st.lon, st.created_at,
         sv.server_ts AS parent_server0
  FROM t_site st
  JOIN t_ver sv ON sv.aggregate_id = st.id AND sv.v = 0
) s
CROSS JOIN LATERAL generate_series(1,
  CASE WHEN NOT s.takes_photos OR pg_temp.u('ph0:' || s.k) < 0.06 THEN 0
       ELSE greatest(1, 10 - floor(pg_temp.ex('phn:' || s.k, 2.3))::int) END) j(j);

INSERT INTO t_ver
SELECT 'photo', p.id, v.v, p.sub, p.offline_heavy, c.cts,
       CASE WHEN v.v = 0 THEN greatest(raw.srv, p.parent_server0 + interval '1 second') ELSE raw.srv END,
       jsonb_build_object('id', p.id, 'siteAssessmentId', p.site_id,
                          's3Key', 'photos/' || p.site_id::text || '/' || p.id::text || '.jpg',
                          'latitude', round((p.lat + 0.02 * (pg_temp.u(p.id::text || ':la') - 0.5))::numeric, 6)::float8,
                          'longitude', round((p.lon + 0.02 * (pg_temp.u(p.id::text || ':lo') - 0.5))::numeric, 6)::float8,
                          'condition', CASE
                              WHEN pg_temp.u(p.id::text || ':cond:' || v.v) < p.sev * 0.6 THEN 'bad'
                              WHEN pg_temp.u(p.id::text || ':cond:' || v.v) < p.sev * 0.6 + 0.3 THEN 'moderate'
                              ELSE 'good' END,
                          'createdAt', p.created_at, 'updatedAt', c.cts)
FROM t_photo p
CROSS JOIN LATERAL generate_series(0, least(5, floor(pg_temp.ex('pupd:' || p.id::text, 2.6))::int)) v(v)
CROSS JOIN LATERAL (SELECT pg_temp.client_ts(p.created_at, v.v, p.id::text) AS cts) c
CROSS JOIN LATERAL (SELECT pg_temp.server_ts(c.cts, p.offline_heavy, p.id::text || ':' || v.v) AS srv) raw;

DELETE FROM t_ver t
WHERE EXISTS (SELECT 1 FROM t_ver x
              WHERE x.aggregate_id = t.aggregate_id AND x.v <= t.v AND x.server_ts >= pg_temp.t_end());

DELETE FROM t_ver t
USING t_actor a
WHERE a.sub = t.actor_sub AND t.v > 0
  AND t.client_ts >= pg_temp.t0() + a.churn_day * interval '1 day';

INSERT INTO t_ver
SELECT t.aggregate_type, t.aggregate_id, t.v + 1, t.actor_sub, t.offline_heavy, st.cts,
       pg_temp.server_ts(st.cts, t.offline_heavy, t.aggregate_id::text || ':storm'),
       t.payload || jsonb_build_object('updatedAt', st.cts)
FROM (SELECT DISTINCT ON (aggregate_id) * FROM t_ver ORDER BY aggregate_id, v DESC) t
JOIN t_actor a ON a.sub = t.actor_sub AND a.churn_day > 58
CROSS JOIN LATERAL (
  SELECT pg_temp.t0() + interval '58 days 8 hours' + interval '3 hours' * pg_temp.u(t.aggregate_id::text || ':storm:ts') AS cts
) st
WHERE t.aggregate_type IN ('site_assessment', 'photo')
  AND t.server_ts < pg_temp.t0() + interval '58 days 8 hours'
  AND pg_temp.u(t.aggregate_id::text || ':storm') < CASE t.aggregate_type WHEN 'photo' THEN 0.2 ELSE 0.5 END;

INSERT INTO disaster_locations (id, name, latitude, longitude, created_at, updated_at, created_by)
SELECT t.aggregate_id, t.payload ->> 'name', (t.payload ->> 'latitude')::float8, (t.payload ->> 'longitude')::float8,
       (t.payload ->> 'createdAt')::timestamptz, (t.payload ->> 'updatedAt')::timestamptz, t.actor_sub
FROM (SELECT DISTINCT ON (aggregate_id) * FROM t_ver WHERE aggregate_type = 'location'
      ORDER BY aggregate_id, v DESC) t;

INSERT INTO site_assessments (id, location_id, name, notes, created_at, updated_at, created_by)
SELECT t.aggregate_id, (t.payload ->> 'locationId')::uuid, t.payload ->> 'name', t.payload ->> 'notes',
       (t.payload ->> 'createdAt')::timestamptz, (t.payload ->> 'updatedAt')::timestamptz, t.actor_sub
FROM (SELECT DISTINCT ON (aggregate_id) * FROM t_ver WHERE aggregate_type = 'site_assessment'
      ORDER BY aggregate_id, v DESC) t;

INSERT INTO photos (id, site_assessment_id, s3_key, latitude, longitude, condition, created_at, updated_at, created_by)
SELECT t.aggregate_id, (t.payload ->> 'siteAssessmentId')::uuid, t.payload ->> 's3Key',
       (t.payload ->> 'latitude')::float8, (t.payload ->> 'longitude')::float8, t.payload ->> 'condition',
       (t.payload ->> 'createdAt')::timestamptz, (t.payload ->> 'updatedAt')::timestamptz, t.actor_sub
FROM (SELECT DISTINCT ON (aggregate_id) * FROM t_ver WHERE aggregate_type = 'photo'
      ORDER BY aggregate_id, v DESC) t;

CREATE TEMP TABLE t_evt ON COMMIT DROP AS
WITH base AS (
  SELECT md5('evt:' || aggregate_id::text || ':' || v)::uuid AS id, aggregate_type, aggregate_id,
         aggregate_type || '.upserted' AS event_type, payload, actor_sub, server_ts AS occurred_at
  FROM t_ver
), dup AS (
  SELECT md5('dup:' || c.c || ':' || b.id::text)::uuid AS id, b.aggregate_type, b.aggregate_id, b.event_type,
         b.payload, b.actor_sub,
         b.occurred_at + interval '3 seconds'
           + interval '1 second' * pg_temp.ex('dupd:' || c.c || ':' || b.id::text, 90) AS occurred_at
  FROM base b
  CROSS JOIN LATERAL generate_series(1,
    CASE WHEN pg_temp.u('dup:' || b.id::text) < 0.003 THEN 2
         WHEN pg_temp.u('dup:' || b.id::text) < 0.03 THEN 1
         ELSE 0 END) c(c)
)
SELECT * FROM base UNION ALL SELECT * FROM dup;

UPDATE t_evt SET occurred_at = pg_temp.after_outage(occurred_at, id::text);

INSERT INTO domain_events (id, aggregate_type, aggregate_id, event_type, schema_version, payload, actor_sub, occurred_at, published_at)
SELECT e.id, e.aggregate_type, e.aggregate_id, e.event_type, 1, e.payload, e.actor_sub, e.occurred_at,
       CASE
         WHEN e.occurred_at > m.as_of - interval '20 minutes' OR pg_temp.u('stuck:' || e.id::text) < 0.004 THEN NULL
         ELSE pg_temp.after_outage(
                e.occurred_at + interval '1 second' * CASE
                  WHEN e.occurred_at >= pg_temp.t0() + interval '70 days 13 hours'
                   AND e.occurred_at <  pg_temp.t0() + interval '70 days 15 hours'
                    THEN 120 + 480 * pg_temp.u('pub:' || e.id::text)
                  ELSE 0.5 + least(pg_temp.ex('pub:' || e.id::text, 2), 300)
                END,
                e.id::text || ':pub')
       END
FROM t_evt e
CROSS JOIN (SELECT max(occurred_at) AS as_of FROM t_evt) m;

INSERT INTO processed_events (event_id, consumer_name, processed_at)
SELECT id, 'photo-processor',
       published_at + interval '1 second' * (0.8 + least(pg_temp.ex(id::text || ':pp', 2.5), 120))
FROM domain_events
WHERE event_type = 'photo.upserted' AND published_at IS NOT NULL
  AND published_at <= pg_temp.t_end() - interval '10 minutes'
  AND pg_temp.u(id::text || ':pp:miss') >= 0.003
UNION ALL
SELECT id, 'dashboard-aggregator',
       published_at + interval '1 second' * CASE
         WHEN published_at >= pg_temp.t0() + interval '51 days 11 hours'
          AND published_at <  pg_temp.t0() + interval '51 days 15 hours'
           THEN 180 + 2400 * pg_temp.u(id::text || ':da:slow')
         ELSE 0.5 + least(pg_temp.ex(id::text || ':da', 1.5), 60)
       END
FROM domain_events
WHERE event_type = 'photo.upserted' AND published_at IS NOT NULL
  AND published_at <= pg_temp.t_end() - interval '8 hours'
  AND pg_temp.u(id::text || ':da:miss') >= 0.002
UNION ALL
SELECT id, 'notifier',
       published_at + interval '1 second' * (0.3 + least(pg_temp.ex(id::text || ':nt', 0.8), 30))
FROM domain_events
WHERE event_type = 'photo.upserted' AND published_at IS NOT NULL
  AND published_at <= pg_temp.t_end() - interval '15 hours'
  AND pg_temp.u(id::text || ':nt:miss') >= 0.01;

COMMIT;
ANALYZE;
