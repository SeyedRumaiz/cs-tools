-- Renames escalation_queue to chat_queue and drops order_key: queue order
-- is now derived from created_at instead of a maintained integer
-- sequence.
--
-- A pure created_at sort can't express "requeue at the front" (a declined
-- or timed-out case needs to go back ahead of everyone still waiting,
-- since that customer already waited once). Adding one boolean, requeued,
-- handles it: requeued rows sort ahead of non-requeued ones, and within
-- each group it's plain created_at order. Bonus: enqueueing no longer
-- needs a table lock or MAX/MIN computation.
ALTER TABLE chat_routing.escalation_queue RENAME TO chat_queue;

DROP INDEX IF EXISTS chat_routing.idx_escalation_queue_order;

ALTER TABLE chat_routing.chat_queue
  DROP COLUMN order_key,
  ADD COLUMN requeued BOOLEAN NOT NULL DEFAULT FALSE;

-- Pop-the-head query: ORDER BY requeued DESC, created_at ASC, id ASC.
-- id breaks ties between rows inserted in the same instant.
CREATE INDEX idx_chat_queue_order ON chat_routing.chat_queue (requeued DESC, created_at ASC, id ASC);
