-- FIFO of not-yet-assigned escalations, oldest first -- populated when
-- Escalate finds no AVAILABLE engineer, drained as engineers free up (see
-- Router.SetPresence / Router.Completed). order_key is a plain sortable
-- integer rather than created_at/id, because Decline needs to re-queue a
-- case at the FRONT (that customer already waited once) -- something a
-- purely chronological key can't express without moving every existing row.
CREATE TABLE escalation_queue (
  id          BIGSERIAL PRIMARY KEY,
  order_key   BIGINT NOT NULL,
  case_id     TEXT NOT NULL,
  case_info   JSONB NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Pop-the-head query: ORDER BY order_key, id -- id breaks ties between rows
-- that were assigned the same order_key by two concurrent enqueues.
CREATE INDEX idx_escalation_queue_order ON escalation_queue (order_key, id);
