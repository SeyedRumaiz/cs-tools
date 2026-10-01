ALTER TABLE chat_routing.chat_conversation
  ADD CONSTRAINT chat_conversation_assignee_id_fkey
  FOREIGN KEY (assignee_id) REFERENCES chat_routing.cs_engineer_status (user_id)
  ON DELETE SET NULL;

-- chat_queue.chat_conversation_id -> chat_conversation.case_id
--
-- ON DELETE RESTRICT (the default, made explicit here): chat_conversation
-- rows are never hard-deleted by any current code path -- router.Router
-- only ever sets session_ended_at/state on them -- so this should never
-- actually fire. RESTRICT is the defensive choice specifically because it
-- never should: if something ever does try to delete a chat_conversation
-- row while its chat_queue entry is still open (WAITING_FOR_ENGINEER or
-- ASSIGNED, not yet deleted by Accept), that is exactly the kind of
-- mistake this constraint exists to catch loudly, not paper over with a
-- silent CASCADE that would destroy an in-flight queue entry a customer is
-- still waiting on.
ALTER TABLE chat_routing.chat_queue
  ADD CONSTRAINT chat_queue_chat_conversation_id_fkey
  FOREIGN KEY (chat_conversation_id) REFERENCES chat_routing.chat_conversation (case_id)
  ON DELETE RESTRICT;

-- chat_queue_engineer_assignment.case_id -> chat_conversation.case_id
--
-- ON DELETE RESTRICT (the default, made explicit here): this table is a
-- pure append-only audit trail (see README's own description) -- CASCADE
-- would let a chat_conversation deletion silently erase assignment
-- history, and SET NULL would leave an audit row with no case to attach
-- to, which is meaningless for a table whose only purpose is answering
-- "what happened on this case." Since chat_conversation rows are never
-- hard-deleted in practice (see above), RESTRICT should likewise never
-- actually fire -- it is there to fail loudly rather than lose history if
-- that assumption is ever broken.
ALTER TABLE chat_routing.chat_queue_engineer_assignment
  ADD CONSTRAINT chat_queue_engineer_assignment_case_id_fkey
  FOREIGN KEY (case_id) REFERENCES chat_routing.chat_conversation (case_id)
  ON DELETE RESTRICT;
