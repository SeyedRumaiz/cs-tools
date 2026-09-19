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

-- Restores the stored counter columns, zeroed -- assignment_log's history
-- isn't replayed back into them -- and drops the log.
ALTER TABLE chat_routing.engineers
  ADD COLUMN chats_today       INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN chats_today_date  DATE;

DROP INDEX chat_routing.idx_assignment_log_email_assigned_at;
DROP TABLE chat_routing.assignment_log;
