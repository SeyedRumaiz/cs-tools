-- This service shares its Postgres database with entity-service, so it
-- keeps its own tables in a dedicated schema to avoid name collisions.
-- The connection's search_path is set to resolve unqualified table names
-- into this schema automatically.
CREATE SCHEMA IF NOT EXISTS chat_routing;

-- Engineer live-chat routing presence, plus the case they're currently
-- handling, if any. One row per engineer email, created on first presence
-- update -- an engineer with no row here just hasn't sent one yet, and the
-- app layer treats that as OFFLINE.
CREATE TYPE chat_routing.engineer_status AS ENUM ('AVAILABLE', 'BUSY', 'OFFLINE');

CREATE TABLE chat_routing.engineers (
  email            TEXT PRIMARY KEY,
  status           chat_routing.engineer_status NOT NULL DEFAULT 'OFFLINE',

  -- Set when a mid-session engineer requests OFFLINE. The actual
  -- transition is deferred until their current session ends.
  pending_offline  BOOLEAN NOT NULL DEFAULT FALSE,

  -- The case this engineer is currently handling, if any. current_case_id
  -- is kept alongside the full current_case JSON blob so we can do fast,
  -- indexable equality checks without parsing JSON every time.
  current_case_id  TEXT,
  current_case     JSONB,

  -- Set to now() whenever this engineer becomes AVAILABLE with no current
  -- case, NULL otherwise. Assignment picks the AVAILABLE engineer with the
  -- oldest available_since, i.e. a FIFO queue expressed as a sort key.
  available_since  TIMESTAMPTZ,

  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

  CONSTRAINT chk_current_case_consistency
    CHECK ((current_case_id IS NULL) = (current_case IS NULL)),
  CONSTRAINT chk_available_since_only_when_available
    CHECK (available_since IS NULL OR (status = 'AVAILABLE' AND current_case_id IS NULL))
);

-- Backs the "pick the longest-idle available engineer" assignment query.
CREATE INDEX idx_engineers_available_since
  ON chat_routing.engineers (available_since)
  WHERE status = 'AVAILABLE' AND current_case_id IS NULL;
