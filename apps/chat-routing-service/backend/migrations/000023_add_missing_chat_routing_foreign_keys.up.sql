-- Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
--
-- WSO2 LLC. licenses this file to you under the Apache License,
-- Version 2.0 (the "License"); you may not use this file except
-- in compliance with the License.
-- You may obtain a copy of the License at
--
-- http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing,
-- software distributed under the License is distributed on an
-- "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
-- KIND, either express or implied.  See the License for the
-- specific language governing permissions and limitations
-- under the License.

-- Adds the three foreign keys this schema was missing between tables that
-- have always lived in the same "chat_routing" schema, in the same
-- database, with no cross-service boundary involved -- unlike case_id/
-- entity_case_id on chat_conversation, which point at identifiers owned by
-- a different service's own schema (customer-portal/entity-service) and
-- are deliberately left convention-only for that reason (see this
-- project's db-schema-review notes on each service keeping its own
-- schema). These three had no such reason; they were simply never added.
-- Verified against the actual write paths (router.Router.CreateWorkItem/
-- Escalate/Accept/Decline/timeoutOne) that the referenced row always
-- exists before the referencing row is written, so none of these should
-- fail on the current application code's own inserts.
--
-- Run each verification query below against the live database before
-- applying this migration, and confirm every one returns zero rows --
-- ADD CONSTRAINT fails outright on the first existing violation otherwise:
--
--   -- orphaned chat_conversation.assignee_id values:
--   SELECT case_id, assignee_id FROM chat_routing.chat_conversation
--   WHERE assignee_id IS NOT NULL
--     AND assignee_id NOT IN (SELECT user_id FROM chat_routing.cs_engineer_status);
--
--   -- orphaned chat_queue.chat_conversation_id values:
--   SELECT chat_conversation_id FROM chat_routing.chat_queue
--   WHERE chat_conversation_id NOT IN (SELECT case_id FROM chat_routing.chat_conversation);
--
--   -- orphaned chat_queue_engineer_assignment.case_id values (after the
--   -- previous migration's rename):
--   SELECT id, case_id FROM chat_routing.chat_queue_engineer_assignment
--   WHERE case_id NOT IN (SELECT case_id FROM chat_routing.chat_conversation);

-- chat_conversation.assignee_id -> cs_engineer_status.user_id
--
-- ON DELETE SET NULL: assignee_id is already nullable (an unassigned/
-- AI-handled conversation has no assignee), and a chat_conversation row is
-- never itself deleted (only soft-ended via session_ended_at/state), so
-- this only matters if a cs_engineer_status row is ever removed out from
-- under a conversation that still references it. In that case the
-- conversation should survive as unassigned rather than block the
-- engineer's row from being removed (RESTRICT) or, worse, disappear
-- itself (CASCADE would delete a live conversation because an unrelated
-- engineer record was cleaned up).
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
