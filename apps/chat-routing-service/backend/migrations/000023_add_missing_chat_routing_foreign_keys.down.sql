ALTER TABLE chat_routing.chat_queue_engineer_assignment
  DROP CONSTRAINT chat_queue_engineer_assignment_case_id_fkey;

ALTER TABLE chat_routing.chat_queue
  DROP CONSTRAINT chat_queue_chat_conversation_id_fkey;

ALTER TABLE chat_routing.chat_conversation
  DROP CONSTRAINT chat_conversation_assignee_id_fkey;
