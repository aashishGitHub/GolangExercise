CREATE INDEX IF NOT EXISTS domain_events_aggregate_idx ON domain_events (aggregate_id, occurred_at);
CREATE INDEX IF NOT EXISTS domain_events_actor_time_idx ON domain_events (actor_sub, occurred_at);
CREATE INDEX IF NOT EXISTS processed_events_consumer_time_idx ON processed_events (consumer_name, processed_at);
ANALYZE domain_events;
ANALYZE processed_events;
