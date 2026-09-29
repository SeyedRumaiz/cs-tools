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

-- Renames engineers to cs_engineer_status and drops its email column --
-- engineer_id (added in 000004) becomes the sole identifier, renamed to
-- user_id, since email already lives in the user table. status is renamed
-- to chat_status since this table only ever tracked chat presence, not a
-- general engineer status.
--
-- Every caller that used to identify an engineer by email now uses user_id
-- instead: this service's Router methods/routes, its SDK, and csm-portal's
-- chat handlers and SSE hub keys (engineerHubKey) -- see
-- internal/router/state.go.
--
-- chat_queue_engineer_assignment's FK depends on the email-backed unique
-- constraint being dropped below, so it's dropped and re-pointed at the new
-- user_id PK explicitly here, alongside its own case_id/engineer_email ->
-- conversation_id/engineer_id rename.
ALTER TABLE chat_routing.chat_queue_engineer_assignment
  DROP CONSTRAINT chat_queue_engineer_assignment_engineer_email_fkey;

ALTER TABLE chat_routing.engineers RENAME TO cs_engineer_status;
ALTER TABLE chat_routing.cs_engineer_status RENAME COLUMN engineer_id TO user_id;
ALTER TABLE chat_routing.cs_engineer_status DROP COLUMN email;
ALTER TABLE chat_routing.cs_engineer_status RENAME COLUMN status TO chat_status;

ALTER TABLE chat_routing.chat_queue_engineer_assignment RENAME COLUMN case_id TO conversation_id;
ALTER TABLE chat_routing.chat_queue_engineer_assignment RENAME COLUMN engineer_email TO engineer_id;
ALTER TABLE chat_routing.chat_queue_engineer_assignment
  ADD CONSTRAINT chat_queue_engineer_assignment_engineer_id_fkey
  FOREIGN KEY (engineer_id) REFERENCES chat_routing.cs_engineer_status (user_id);
