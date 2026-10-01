-- Restores the stored counter columns, zeroed -- assignment_log's history
-- isn't replayed back into them -- and drops the log.
ALTER TABLE chat_routing.engineers
  ADD COLUMN chats_today       INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN chats_today_date  DATE;

DROP INDEX chat_routing.idx_assignment_log_email_assigned_at;
DROP TABLE chat_routing.assignment_log;
