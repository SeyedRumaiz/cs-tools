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

ALTER TABLE chat_routing.chat_queue_engineer_assignment
  DROP CONSTRAINT chat_queue_engineer_assignment_engineer_id_fkey;

ALTER TABLE chat_routing.chat_queue_engineer_assignment RENAME COLUMN engineer_id TO engineer_email;
ALTER TABLE chat_routing.chat_queue_engineer_assignment RENAME COLUMN conversation_id TO case_id;

ALTER TABLE chat_routing.cs_engineer_status RENAME COLUMN chat_status TO status;

-- email's original values can't be restored (never stored anywhere once
-- dropped). Existing rows get a placeholder so the restored NOT
-- NULL/UNIQUE constraints are satisfiable; a real rollback means every
-- engineer needs to set their presence again to get a real email attached.
ALTER TABLE chat_routing.cs_engineer_status ADD COLUMN email TEXT;
UPDATE chat_routing.cs_engineer_status SET email = user_id || '@unknown.invalid';
ALTER TABLE chat_routing.cs_engineer_status ALTER COLUMN email SET NOT NULL;
ALTER TABLE chat_routing.cs_engineer_status ADD CONSTRAINT engineers_email_key UNIQUE (email);

ALTER TABLE chat_routing.cs_engineer_status RENAME COLUMN user_id TO engineer_id;
ALTER TABLE chat_routing.cs_engineer_status RENAME TO engineers;

ALTER TABLE chat_routing.chat_queue_engineer_assignment
  ADD CONSTRAINT chat_queue_engineer_assignment_engineer_email_fkey
  FOREIGN KEY (engineer_email) REFERENCES chat_routing.engineers (email);
