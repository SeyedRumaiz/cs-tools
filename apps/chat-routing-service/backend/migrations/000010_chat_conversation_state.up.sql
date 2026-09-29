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
