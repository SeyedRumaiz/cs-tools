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

-- Reverses 000018_concurrent_chat_capacity.up.sql. Restores column/
-- constraint/index shape only -- per-conversation accepted_at/
-- session_ended_at history and any max_concurrent_chats > 1 overrides
-- cannot be reconstructed back onto a single-case-per-engineer model.

DROP INDEX chat_routing.idx_chat_conversation_active_assignee;
ALTER TABLE chat_routing.chat_conversation DROP COLUMN case_info;
ALTER TABLE chat_routing.chat_conversation DROP COLUMN session_ended_at;
ALTER TABLE chat_routing.chat_conversation DROP COLUMN accepted_at;

ALTER TABLE chat_routing.cs_engineer_status DROP COLUMN max_concurrent_chats;

DROP INDEX chat_routing.idx_engineers_available_since;
ALTER TABLE chat_routing.cs_engineer_status DROP CONSTRAINT chk_available_since_only_when_available;

ALTER TABLE chat_routing.cs_engineer_status ADD COLUMN current_case_id TEXT;
ALTER TABLE chat_routing.cs_engineer_status ADD COLUMN current_case JSONB;
ALTER TABLE chat_routing.cs_engineer_status ADD COLUMN accepted_at TIMESTAMPTZ;

ALTER TABLE chat_routing.cs_engineer_status
  ADD CONSTRAINT chk_current_case_consistency
  CHECK ((current_case_id IS NULL) = (current_case IS NULL));
ALTER TABLE chat_routing.cs_engineer_status
  ADD CONSTRAINT chk_available_since_only_when_available
  CHECK (available_since IS NULL OR (chat_status = 'AVAILABLE' AND current_case_id IS NULL));
ALTER TABLE chat_routing.cs_engineer_status
  ADD CONSTRAINT chk_accepted_only_with_case
  CHECK (accepted_at IS NULL OR current_case_id IS NOT NULL);

CREATE INDEX idx_engineers_available_since
  ON chat_routing.cs_engineer_status (available_since)
  WHERE chat_status = 'AVAILABLE' AND current_case_id IS NULL;
