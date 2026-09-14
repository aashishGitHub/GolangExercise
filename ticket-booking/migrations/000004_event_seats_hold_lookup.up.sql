-- There is no separate `holds` table — hold state lives entirely in
-- event_seats, keyed by hold_id (docs/plan.md's design). GET/DELETE
-- /holds/{id} and POST /holds/{id}/extend need to look a hold up by its id
-- alone (no event_id in the path), so this partial index makes that a
-- direct lookup instead of a sequential scan. A dedicated `holds` table
-- would be the alternative at real scale; noted as a deliberate
-- simplification for this project's scope, not an oversight.
CREATE INDEX event_seats_hold_id_idx ON event_seats (hold_id) WHERE hold_id IS NOT NULL;
