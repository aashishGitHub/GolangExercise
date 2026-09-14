CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Client-generated GUID primary keys throughout: the offline client mints the
-- id before it ever reaches the server, which is what makes sync idempotent
-- (replaying the same record is just re-applying the same row, not creating
-- a duplicate).
CREATE TABLE disaster_locations (
    id          UUID PRIMARY KEY,
    name        TEXT NOT NULL,
    latitude    DOUBLE PRECISION NOT NULL,
    longitude   DOUBLE PRECISION NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL,
    created_by  TEXT NOT NULL -- Cognito `sub`, stamped server-side, never client-supplied
);

CREATE TABLE site_assessments (
    id           UUID PRIMARY KEY,
    location_id  UUID NOT NULL REFERENCES disaster_locations(id),
    name         TEXT NOT NULL,
    notes        TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL,
    created_by   TEXT NOT NULL
);
CREATE INDEX site_assessments_location_id_idx ON site_assessments(location_id);

CREATE TABLE photos (
    id                  UUID PRIMARY KEY,
    site_assessment_id  UUID NOT NULL REFERENCES site_assessments(id),
    s3_key              TEXT NOT NULL,
    latitude            DOUBLE PRECISION NOT NULL,
    longitude           DOUBLE PRECISION NOT NULL,
    condition           TEXT NOT NULL CHECK (condition IN ('good', 'moderate', 'bad')),
    created_at          TIMESTAMPTZ NOT NULL,
    updated_at          TIMESTAMPTZ NOT NULL,
    created_by          TEXT NOT NULL
);
CREATE INDEX photos_site_assessment_id_idx ON photos(site_assessment_id);
