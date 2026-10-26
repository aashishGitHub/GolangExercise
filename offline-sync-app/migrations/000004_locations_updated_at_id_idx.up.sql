-- CONCURRENTLY does not block writes during the build, but cannot run inside a transaction: keep it the only statement here.
CREATE INDEX CONCURRENTLY disaster_locations_updated_at_id_idx ON disaster_locations (updated_at, id);
