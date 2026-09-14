DROP INDEX domain_events_outbox_seq_idx;
ALTER TABLE domain_events DROP COLUMN outbox_seq;
