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

-- One row per active (unaccepted) escalation, from Escalate until Accept.
CREATE TYPE chat_routing.chat_queue_status AS ENUM (
    'WAITING_FOR_ENGINEER',
    'ASSIGNED'
);

CREATE TABLE chat_routing.chat_queue (
    chat_conversation_id   text PRIMARY KEY REFERENCES chat_routing.chat_conversation (case_id) ON DELETE RESTRICT,
    case_info              jsonb NOT NULL,
    status                 chat_routing.chat_queue_status NOT NULL DEFAULT 'WAITING_FOR_ENGINEER',
    created_at             timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_chat_queue_waiting_order
    ON chat_routing.chat_queue (created_at, chat_conversation_id)
    WHERE status = 'WAITING_FOR_ENGINEER';
