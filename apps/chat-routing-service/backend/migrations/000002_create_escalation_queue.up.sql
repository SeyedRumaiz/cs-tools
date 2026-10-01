CREATE TABLE chat_routing.escalation_queue (
  id          BIGSERIAL PRIMARY KEY,
  order_key   BIGINT NOT NULL,
  case_id     TEXT NOT NULL,
  case_info   JSONB NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_escalation_queue_order ON chat_routing.escalation_queue (order_key, id);
