-- This service shares its Postgres database with entity-service (same
-- server, same database, e.g. "db") but keeps its own tables in a
-- dedicated schema so the two services' migration histories and table
-- namespaces never collide -- see this service's README for how the
-- connection's search_path is set to resolve unqualified table names
-- (engineers, escalation_queue, ...) into this schema automatically.
CREATE SCHEMA IF NOT EXISTS chat_routing;

-- Engineer live-chat routing presence: Available/Busy/Offline, plus the
-- case they're currently handling (if any). One row per engineer email,
-- created on first presence update (see chat-routing-service's
-- internal/router package) -- an engineer this table has never seen a
-- presence update from simply has no row, and the application layer
-- defaults that to OFFLINE (see Router.GetPresence).
CREATE TYPE chat_routing.engineer_status AS ENUM ('AVAILABLE', 'BUSY', 'OFFLINE');

CREATE TABLE chat_routing.engineers (
  email            TEXT PRIMARY KEY,
  status           chat_routing.engineer_status NOT NULL DEFAULT 'OFFLINE',

  -- Set when a mid-session engineer requests OFFLINE: the transition is
  -- deferred until their current session ends (see Router.Completed).
  pending_offline  BOOLEAN NOT NULL DEFAULT FALSE,

  -- The case this engineer is currently handling, if any. current_case_id
  -- is kept alongside the full current_case JSON blob purely for fast,
  -- indexable equality checks (e.g. Decline's "is this really their case"
  -- check) without parsing JSON on every call.
  current_case_id  TEXT,
  current_case     JSONB,

  -- Set to now() whenever this engineer becomes AVAILABLE with no current
  -- case; NULL otherwise. Assignment always picks the AVAILABLE engineer
  -- with the oldest available_since -- a FIFO/round-robin queue expressed
  -- as a sort key instead of a separate list, mirroring the in-memory
  -- prototype's "available []string" FIFO one column removed.
  available_since  TIMESTAMPTZ,

  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

  CONSTRAINT chk_current_case_consistency
    CHECK ((current_case_id IS NULL) = (current_case IS NULL)),
  CONSTRAINT chk_available_since_only_when_available
    CHECK (available_since IS NULL OR (status = 'AVAILABLE' AND current_case_id IS NULL))
);

-- Assignment's "pick the longest-idle available engineer" query
-- (Router.Escalate / Router.Decline's reassignment path).
CREATE INDEX idx_engineers_available_since
  ON chat_routing.engineers (available_since)
  WHERE status = 'AVAILABLE' AND current_case_id IS NULL;
