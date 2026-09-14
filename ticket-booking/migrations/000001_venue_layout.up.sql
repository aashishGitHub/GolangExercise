-- Venue-time (static) schema: the blueprint, reused across every event held
-- at a venue. deep-dive.md §8's "blueprints vs tonight's guest list" split —
-- event_seats (the write-path, per-event table) lands in migration 000002.

CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE venues (
    venue_id       BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name           TEXT NOT NULL,
    city           TEXT NOT NULL,
    layout_version INT NOT NULL DEFAULT 1,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sections (
    section_id    BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    venue_id      BIGINT NOT NULL REFERENCES venues (venue_id),
    name          TEXT NOT NULL,
    tier          TEXT NOT NULL,
    display_order INT NOT NULL,
    -- Template default: an event-publisher run seeds event_seats.sellable
    -- from this. event_seats.sellable is the actual source of truth once an
    -- event exists (see 000002) — this column only decides the default for
    -- a *new* event at that venue.
    closed        BOOLEAN NOT NULL DEFAULT false
);
CREATE INDEX sections_venue_idx ON sections (venue_id, display_order);

CREATE TABLE seat_rows (
    row_id        BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    section_id    BIGINT NOT NULL REFERENCES sections (section_id),
    label         TEXT NOT NULL,
    display_order INT NOT NULL
);
CREATE INDEX seat_rows_section_idx ON seat_rows (section_id, display_order);

CREATE TABLE seats (
    seat_id    BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    row_id     BIGINT NOT NULL REFERENCES seat_rows (row_id),
    seat_label TEXT NOT NULL,
    x_coord    INT NOT NULL,
    y_coord    INT NOT NULL,
    UNIQUE (row_id, seat_label)
);
CREATE INDEX seats_row_idx ON seats (row_id);

CREATE TABLE events (
    event_id       BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    venue_id       BIGINT NOT NULL REFERENCES venues (venue_id),
    artist         TEXT NOT NULL,
    title          TEXT NOT NULL,
    starts_at      TIMESTAMPTZ NOT NULL,
    onsale_at      TIMESTAMPTZ NOT NULL,
    home_region    TEXT NOT NULL DEFAULT 'us-east-1',
    -- DRAFT -> ON_SALE (flipped by cmd/event-publisher) -> CLOSED
    status         TEXT NOT NULL DEFAULT 'DRAFT',
    layout_version INT NOT NULL DEFAULT 1,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX events_venue_idx ON events (venue_id);
-- Search = pg_trgm + GIN, not OpenSearch (docs/plan.md decision #8). Accelerates
-- both `ILIKE '%term%'` and the `%`/similarity() operators.
CREATE INDEX events_title_trgm_idx ON events USING gin (title gin_trgm_ops);
CREATE INDEX events_artist_trgm_idx ON events USING gin (artist gin_trgm_ops);

CREATE TABLE event_price_tiers (
    event_id    BIGINT NOT NULL REFERENCES events (event_id),
    tier        TEXT NOT NULL,
    price_cents INT NOT NULL,
    PRIMARY KEY (event_id, tier)
);
