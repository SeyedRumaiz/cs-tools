-- FIFO of not-yet-assigned escalations, oldest first. Populated when
-- Escalate finds no AVAILABLE engineer, drained as engineers free up.
-- order_key is a plain sortable integer rather than created_at/id because
-- Decline needs to re-queue a case at the front (that customer already
-- waited once), which a purely chronological key can't express without
-- moving every existing row.
CREATE TABLE chat_routing.escalation_queue (
  id          BIGSERIAL PRIMARY KEY,
  order_key   BIGINT NOT NULL,
  case_id     TEXT NOT NULL,
  case_info   JSONB NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Pop-the-head query: ORDER BY order_key, id. id breaks ties between rows
-- assigned the same order_key by concurrent enqueues.
CREATE INDEX idx_escalation_queue_order ON chat_routing.escalation_queue (order_key, id);
