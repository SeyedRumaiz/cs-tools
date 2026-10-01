CREATE SCHEMA IF NOT EXISTS chat_routing;

CREATE TYPE chat_routing.engineer_status AS ENUM ('AVAILABLE', 'BUSY', 'OFFLINE');

CREATE TABLE chat_routing.engineers (
  email            TEXT PRIMARY KEY,
  status           chat_routing.engineer_status NOT NULL DEFAULT 'OFFLINE',
  pending_offline  BOOLEAN NOT NULL DEFAULT FALSE,
  current_case_id  TEXT,
  current_case     JSONB,
  available_since  TIMESTAMPTZ,
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

  CONSTRAINT chk_current_case_consistency
    CHECK ((current_case_id IS NULL) = (current_case IS NULL)),
  CONSTRAINT chk_available_since_only_when_available
    CHECK (available_since IS NULL OR (status = 'AVAILABLE' AND current_case_id IS NULL))
);

CREATE INDEX idx_engineers_available_since
  ON chat_routing.engineers (available_since)
  WHERE status = 'AVAILABLE' AND current_case_id IS NULL;
