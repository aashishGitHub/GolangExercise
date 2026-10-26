# FieldSync analytics practice

Analytics SQL (window functions, percentiles, cohorts) on a seeded copy of FieldSync's schema, in its own database so it never touches the app's `fieldsync` database. Only migrations 000001 and 000002 are applied.

Postgres comes from the repo's `docker-compose.yml`; `psql` is not installed on the host, so everything runs through the container. Run from `offline-sync-app/`.

## Create the database

```
docker compose up -d postgres
docker compose exec -T postgres psql -U fieldsync -d postgres -c "CREATE DATABASE fieldsync_analytics"
docker compose exec -T postgres psql -U fieldsync -d fieldsync_analytics -v ON_ERROR_STOP=1 < migrations/000001_init_schema.up.sql
docker compose exec -T postgres psql -U fieldsync -d fieldsync_analytics -v ON_ERROR_STOP=1 < migrations/000002_add_domain_events.up.sql
```

## Seed (deterministic and re-runnable: it truncates and refills)

```
docker compose exec -T postgres psql -U fieldsync -d fieldsync_analytics -v ON_ERROR_STOP=1 < analytics/seed/seed.sql
```

The script refuses to run unless the database name contains `analytics`. Same output every run: randomness comes from `md5(key)`, not `random()`, so it does not depend on row order or plan.

## Run one query

```
docker compose exec -T postgres psql -U fieldsync -d fieldsync_analytics < analytics/queries/04_latency_percentiles.sql
```

Each file in `queries/` is: business question, SQL, expected result shape, "say this in the interview" notes.

## Run the tests

```
go vet ./analytics/...
go test -tags=integration ./analytics/...
```

The test connects to `ANALYTICS_DATABASE_URL` (default `postgres://fieldsync:fieldsync@localhost:5432/fieldsync_analytics?sslmode=disable`), reseeds, and asserts invariants for every query file, that results do not change with the session time zone, that two seeds are byte-identical, and that the EXPLAIN-study indexes change the plans. It refuses to run against a database whose name does not contain `analytics`.

## EXPLAIN study

```
docker compose exec -T postgres psql -U fieldsync -d fieldsync_analytics < analytics/drop_indexes.sql
(echo "EXPLAIN (ANALYZE, BUFFERS)"; cat analytics/explain/01_aggregate_history.sql) | docker compose exec -T postgres psql -U fieldsync -d fieldsync_analytics
docker compose exec -T postgres psql -U fieldsync -d fieldsync_analytics < analytics/indexes.sql
(echo "EXPLAIN (ANALYZE, BUFFERS)"; cat analytics/explain/01_aggregate_history.sql) | docker compose exec -T postgres psql -U fieldsync -d fieldsync_analytics
```

Repeat for `02_actor_recent_activity.sql` and `03_consumer_recent_processing.sql`. Results are in `EXPLAIN.md`. `explain/01_aggregate_history.sql` hard-codes one aggregate id that exists in the current seed; if you change the seed, pick a new one.

## Files

- `seed/seed.sql` generator
- `queries/*.sql` 14 queries
- `indexes.sql`, `drop_indexes.sql`, `explain/` the EXPLAIN study
- `analytics_test.go` integration tests (build tag `integration`)
- `QUESTIONS.md` 12 interview questions and the one-decision dashboard
- `EXPLAIN.md` measured before/after
