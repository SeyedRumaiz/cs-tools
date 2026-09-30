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

-- The source of truth for which case(s) an engineer holds -- see
-- assignee_id, not a column on cs_engineer_status.
CREATE TYPE chat_routing.chat_conversation_state AS ENUM (
    'OPEN',
    'ACTIVE',
    'RESOLVED',
    'CONVERTED_CHAT',
    'CONVERTED_CASE',
    'ABANDONED',
    'CLOSED'
);

CREATE TABLE chat_routing.chat_conversation (
    work_item_id        uuid PRIMARY KEY REFERENCES chat_routing.work_item (id),
    case_id             text NOT NULL,
    conversation_id     text NOT NULL,
    assignee_id         text REFERENCES chat_routing.cs_engineer_status (user_id) ON DELETE SET NULL,
    state               chat_routing.chat_conversation_state NOT NULL DEFAULT 'OPEN',
    accepted_at         timestamptz,
    session_ended_at    timestamptz,
    case_info           jsonb,
    entity_case_id      text,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_chat_conversation_case_id
    ON chat_routing.chat_conversation (case_id);

CREATE INDEX idx_chat_conversation_conversation_id
    ON chat_routing.chat_conversation (conversation_id);

CREATE INDEX idx_chat_conversation_active_assignee
    ON chat_routing.chat_conversation (assignee_id, state)
    WHERE assignee_id IS NOT NULL AND session_ended_at IS NULL;

-- At most one open (non-ended) chat per customer+project -- see
-- ErrDuplicateOpenChat in internal/router/workitem.go.
CREATE UNIQUE INDEX uq_chat_conversation_open_customer_project
    ON chat_routing.chat_conversation (
        COALESCE(case_info ->> 'customerEmail', ''),
        COALESCE(case_info ->> 'projectId', '')
    )
    WHERE session_ended_at IS NULL;
