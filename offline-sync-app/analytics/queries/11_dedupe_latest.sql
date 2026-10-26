/*
BUSINESS QUESTION
  "How many distinct writes did field staff really make, versus how many outbox rows exist?
  Count each logical write once, keeping the latest copy, and tell me the duplicate rate."
*/
WITH ranked AS (
  SELECT id, event_type, occurred_at,
         row_number() OVER (PARTITION BY aggregate_id, payload ->> 'updatedAt'
                            ORDER BY occurred_at DESC, id DESC) AS rn,
         count(*)     OVER (PARTITION BY aggregate_id, payload ->> 'updatedAt')              AS copies
  FROM domain_events
)
SELECT event_type,
       count(*)                                        AS raw_rows,
       count(*) FILTER (WHERE rn = 1)                  AS distinct_writes,
       count(*) FILTER (WHERE rn > 1)                  AS duplicate_rows,
       round(count(*) FILTER (WHERE rn > 1)::numeric / count(*), 4) AS duplicate_rate,
       max(copies)                                     AS max_copies_of_one_write
FROM ranked
GROUP BY event_type
ORDER BY event_type;
/*
RESULT SHAPE
  One row per event type. raw_rows = distinct_writes + duplicate_rows. distinct_writes equals
  count(DISTINCT (aggregate_id, payload->>'updatedAt')). duplicate_rate in [0,1].

SAY THIS IN THE INTERVIEW
  - Dedupe = define the business key first. Here a logical write is (aggregate_id,
    payload.updatedAt): the same record version. Event id cannot be the key, it is a fresh uuid
    on every row, including duplicates.
  - row_number() OVER (PARTITION BY key ORDER BY occurred_at DESC, id DESC) = 1 keeps the
    latest copy; flip to ASC to keep the first. The id tiebreaker makes the choice stable when
    two copies share a timestamp.
  - Keep-latest vs keep-first is a semantic choice: latest has the freshest publish state,
    first preserves original arrival time. Say which and why.
  - In THIS system exact duplicates should be rare: the upsert guard is "updated_at >" so a
    replayed request is ignored and writes no event. The seed injects ~3% anyway (replay /
    backfill / relay double-publish) because an analyst cannot assume upstream guarantees.
  - Analysis only: domain_events is the audit log, never DELETE the duplicates. If you must
    materialise a clean set, use a VIEW or CREATE TABLE AS on the rn = 1 rows.
  - count(*) FILTER vs a WHERE rn = 1: FILTER lets raw and distinct counts sit in one pass.
  - Consumer idempotency is per event id, so duplicates are processed again downstream: this
    is why a customer's dashboard can over-count unless it dedupes on the business key.
  - Index: (aggregate_id, (payload->>'updatedAt')) expression index would turn the window's
    sort into an index scan, but the query touches every row either way.
*/
