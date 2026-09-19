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

-- Replaces the stored, lazily-reset chats_today/chats_today_date columns
-- with an append-only log queried dynamically (COUNT(*) per engineer per
-- day). Trades an O(1) column read for an indexed COUNT(*), but picks up a
-- free audit trail of who was assigned what and when.
CREATE TABLE chat_routing.assignment_log (
  id           BIGSERIAL PRIMARY KEY,
  email        TEXT NOT NULL REFERENCES chat_routing.engineers (email),
  case_id      TEXT NOT NULL,
  assigned_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Backs the "today" COUNT(*) (email, assigned_at range scan) and any
-- future "history for this engineer" lookup.
CREATE INDEX idx_assignment_log_email_assigned_at
  ON chat_routing.assignment_log (email, assigned_at);

ALTER TABLE chat_routing.engineers
  DROP COLUMN chats_today,
  DROP COLUMN chats_today_date;
