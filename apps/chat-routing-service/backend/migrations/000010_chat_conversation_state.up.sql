-- Adds a lifecycle state to chat_conversation -- previously there was no
-- way to tell an open chat from a finished one. Transition rules live in
-- application code, not here; this migration only adds the column and enum.
--
-- Only OPEN -> ACTIVE is wired up today (Router.Accept). RESOLVED,
-- CONVERTED_CHAT, CONVERTED_CASE, ABANDONED, and CLOSED exist for features
-- that don't exist yet, so the column/enum is ready without another
-- migration later.
CREATE TYPE chat_routing.chat_conversation_state AS ENUM (
  'OPEN', 'ACTIVE', 'RESOLVED', 'CONVERTED_CHAT', 'CONVERTED_CASE', 'ABANDONED', 'CLOSED'
);

ALTER TABLE chat_routing.chat_conversation
  ADD COLUMN state chat_routing.chat_conversation_state NOT NULL DEFAULT 'OPEN';

-- Existing rows: ACTIVE if already accepted (engineer_id set), otherwise OPEN.
UPDATE chat_routing.chat_conversation SET state = 'ACTIVE' WHERE engineer_id IS NOT NULL;

-- conversation_id duplicated work_item_id/case_id and was never queried by;
-- dropped along with the Go code that used to send it.
ALTER TABLE chat_routing.chat_conversation DROP COLUMN conversation_id;
