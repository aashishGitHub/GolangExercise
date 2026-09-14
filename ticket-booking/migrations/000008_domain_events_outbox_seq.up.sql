-- Real bug found by Phase 7's projector integration test (overflow test:
-- 220 real held/released transitions produced only 112 "applied" bitset
-- changes): domain_events was ordered by created_at (TIMESTAMPTZ) alone,
-- which is NOT a strict total order under rapid sequential inserts —
-- Postgres timestamp resolution and query planning give no guarantee two
-- rows inserted microseconds apart come back in insertion order. A
-- projector applying two same-direction transitions back to back (out of
-- their real order) silently no-ops the second one via apply()'s own
-- idempotency check — correct behavior for a genuine duplicate, wrong
-- when the real cause was ordering, not duplication.
--
-- outbox_seq is a BIGSERIAL assigned at insert time — a true monotonic
-- tiebreaker every consumer (outbox relay AND projector) should order by
-- instead of created_at.
ALTER TABLE domain_events ADD COLUMN outbox_seq BIGSERIAL;
CREATE INDEX domain_events_outbox_seq_idx ON domain_events (outbox_seq);
