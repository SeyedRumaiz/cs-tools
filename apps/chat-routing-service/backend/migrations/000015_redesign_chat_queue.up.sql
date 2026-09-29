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

-- Redesigns chat_queue around a natural key: chat_conversation_id (this
-- service's existing CaseInfo.ConversationID, already available at
-- Router.Escalate time) replaces the bigserial id PK.
--
-- status is now a 2-value enum, WAITING_FOR_ENGINEER / ASSIGNED. One row
-- exists per escalation from creation (Router.Escalate) until Router.Accept
-- confirms the engineer, and is only ever updated in place -- never deleted
-- and re-inserted -- until it's finally removed. That keeps its original
-- created_at through a decline, timeout, or reassignment, which is what
-- makes plain (created_at, chat_conversation_id) ordering enough on its
-- own. `requeued` (added in 000002/000008) is dropped along with it,
-- superseded by the same behavior.
--
-- case_info (JSONB) is kept even though it duplicates data that will
-- eventually live in a real case table, because no such table is queryable
-- from this service yet: entity-service's case data lives elsewhere, and
-- this service's own stand-in work_item/chat_conversation tables aren't
-- populated until after Router.Escalate returns. Dropping case_info now
-- would mean a queued customer loses their subject/message/name. Revisit
-- once those stand-in tables are retired in favor of entity-service's real
-- work item.
CREATE TYPE chat_routing.chat_queue_status AS ENUM ('WAITING_FOR_ENGINEER', 'ASSIGNED');

CREATE TABLE chat_routing.chat_queue_new (
  chat_conversation_id  TEXT PRIMARY KEY,
  case_info             JSONB NOT NULL,
  status                chat_routing.chat_queue_status NOT NULL DEFAULT 'WAITING_FOR_ENGINEER',
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Carry over any in-flight rows using case_id as the new natural key --
-- case_id and conversation_id were always the same value in practice (see
-- enqueueCase in internal/router/state.go). Every row is marked
-- WAITING_FOR_ENGINEER conservatively: the old shape can't tell us which
-- rows were already handed to an engineer, and guessing ASSIGNED risks
-- silently dropping a customer.
INSERT INTO chat_routing.chat_queue_new (chat_conversation_id, case_info, status, created_at)
SELECT case_id, case_info, 'WAITING_FOR_ENGINEER', created_at FROM chat_routing.chat_queue
ON CONFLICT (chat_conversation_id) DO NOTHING;

DROP TABLE chat_routing.chat_queue;
ALTER TABLE chat_routing.chat_queue_new RENAME TO chat_queue;

CREATE INDEX idx_chat_queue_waiting_order
  ON chat_routing.chat_queue (created_at, chat_conversation_id)
  WHERE status = 'WAITING_FOR_ENGINEER';
