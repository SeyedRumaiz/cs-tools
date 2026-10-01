DROP INDEX IF EXISTS chat_routing.idx_chat_queue_order;

ALTER TABLE chat_routing.chat_queue
  DROP COLUMN requeued,
  ADD COLUMN order_key BIGINT NOT NULL DEFAULT 0;

ALTER TABLE chat_routing.chat_queue RENAME TO escalation_queue;

CREATE INDEX idx_escalation_queue_order ON chat_routing.escalation_queue (order_key, id);
