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

-- Persistent audit trail of assignment outcomes: one row per assignment,
-- appended once the outcome is known (CONNECTED on accept, REJECTED on
-- decline, TIMED_OUT on timeout). This service only ever pings one
-- candidate engineer per case at a time, so there's no multi-candidate
-- "awaiting response" state to track here -- append-only, same style as
-- assignment_log.
CREATE TYPE chat_routing.assignment_outcome AS ENUM ('CONNECTED', 'REJECTED', 'TIMED_OUT');

CREATE TABLE chat_routing.chat_queue_engineer_assignment (
  id             BIGSERIAL PRIMARY KEY,
  case_id        TEXT NOT NULL,
  engineer_email TEXT NOT NULL REFERENCES chat_routing.engineers (email),
  status         chat_routing.assignment_outcome NOT NULL,
  occurred_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Backs outcome-history lookups by case and by engineer. No live query
-- uses these yet, but both are an obvious future need for audit data.
CREATE INDEX idx_chat_queue_engineer_assignment_case
  ON chat_routing.chat_queue_engineer_assignment (case_id, occurred_at);
CREATE INDEX idx_chat_queue_engineer_assignment_engineer
  ON chat_routing.chat_queue_engineer_assignment (engineer_email, occurred_at);
