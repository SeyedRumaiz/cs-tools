ALTER TABLE chat_routing.chat_conversation
  ADD COLUMN conversation_id TEXT NOT NULL DEFAULT '';
ALTER TABLE chat_routing.chat_conversation ALTER COLUMN conversation_id DROP DEFAULT;

ALTER TABLE chat_routing.chat_conversation DROP COLUMN state;
DROP TYPE IF EXISTS chat_routing.chat_conversation_state;
