-- Replaces the stored, lazily-reset chats_today/chats_today_date columns
-- with an append-only log queried dynamically (COUNT(*) per engineer per
-- day). Trades an O(1) column read for an indexed COUNT(*), but picks up a
-- free audit trail of who was assigned what and when.
CREATE TABLE chat_routing.assignment_log (
  id           BIGSERIAL PRIMARY KEY,
  email        TEXT NOT NULL REFERENCES chat_routing.engineers (email),
  case_id      TEXT NOT NULL,
  assigned_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Backs the "today" COUNT(*) (email, assigned_at range scan) and any
-- future "history for this engineer" lookup.
CREATE INDEX idx_assignment_log_email_assigned_at
  ON chat_routing.assignment_log (email, assigned_at);

ALTER TABLE chat_routing.engineers
  DROP COLUMN chats_today,
  DROP COLUMN chats_today_date;
