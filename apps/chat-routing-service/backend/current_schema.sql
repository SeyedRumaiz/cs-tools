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

-- GENERATED REFERENCE SNAPSHOT -- not applied by golang-migrate and not
-- read by this service at runtime. Shows the chat_routing schema as it
-- looks today, in one place, instead of reconstructed by reading all of
-- migrations/ in order. migrations/ remains the only source of truth for
-- actually applying schema changes -- see this file's own comment in
-- README.md's "Data model" section for why (an applied migration is
-- frozen; you can't retroactively edit an old one to change what already-
-- migrated databases look like).
--
-- Regenerate after adding a migration:
--   pg_dump --schema-only --schema=chat_routing --no-owner --no-privileges \
--     --no-tablespaces <connection-args>
-- then reformat to match this file's grouping (each table with its own
-- indexes/constraints, matching README.md's "Data model" table order) and
-- re-check it against that section for drift.

CREATE SCHEMA IF NOT EXISTS chat_routing;

-- ===== Types =====

CREATE TYPE chat_routing.engineer_status AS ENUM (
    'AVAILABLE',
    'BUSY',
    'OFFLINE'
);

CREATE TYPE chat_routing.chat_conversation_state AS ENUM (
    'OPEN',
    'ACTIVE',
    'RESOLVED',
    'CONVERTED_CHAT',
    'CONVERTED_CASE',
    'ABANDONED',
    'CLOSED'
);

CREATE TYPE chat_routing.chat_queue_status AS ENUM (
    'WAITING_FOR_ENGINEER',
    'ASSIGNED'
);

CREATE TYPE chat_routing.assignment_outcome AS ENUM (
    'CONNECTED',
    'REJECTED',
    'TIMED_OUT'
);

-- ===== cs_engineer_status =====
-- An engineer's manual chat_status and configurable concurrent-chat
-- capacity. Which cases they're actually holding is NOT here -- see
-- chat_conversation.assignee_id.

CREATE TABLE chat_routing.cs_engineer_status (
    -- Constraint name is "engineers_pkey", not "cs_engineer_status_pkey" --
    -- renaming a table in Postgres doesn't rename its constraints, and this
    -- table was created as "engineers" (see migrations/000014).
    user_id               text CONSTRAINT engineers_pkey PRIMARY KEY,
    chat_status           chat_routing.engineer_status NOT NULL DEFAULT 'OFFLINE',
    max_concurrent_chats  integer NOT NULL DEFAULT 1,
    available_since       timestamptz,
    updated_at            timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT chk_max_concurrent_chats
        CHECK (max_concurrent_chats >= 1 AND max_concurrent_chats <= 10),
    CONSTRAINT chk_available_since_only_when_available
        CHECK (available_since IS NULL OR chat_status = 'AVAILABLE')
);

CREATE INDEX idx_engineers_available_since
    ON chat_routing.cs_engineer_status (available_since)
    WHERE chat_status = 'AVAILABLE';

-- ===== work_item / comment =====
-- Local stand-in for entity-service's eventual real work-item schema --
-- see internal/router/workitem.go's own package comment.

CREATE TABLE chat_routing.work_item (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id         text NOT NULL,
    subject            text NOT NULL,
    work_item_number   text,
    created_at         timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE chat_routing.comment (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    work_item_id   uuid NOT NULL REFERENCES chat_routing.work_item (id),
    content        text NOT NULL,
    created_by     text NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_comment_work_item_id
    ON chat_routing.comment (work_item_id, created_at);

-- ===== chat_conversation =====
-- The source of truth for which case(s) an engineer holds -- see
-- assignee_id, not a column on cs_engineer_status.

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

-- ===== chat_queue =====
-- One row per active (unaccepted) escalation, from Escalate until Accept.

CREATE TABLE chat_routing.chat_queue (
    -- Constraint name is "chat_queue_new_pkey" -- migrations/000015
    -- rebuilt this table as "chat_queue_new" (a new PK, a natural-key
    -- redesign) then renamed it to "chat_queue"; Postgres doesn't rename
    -- constraints along with the table.
    chat_conversation_id   text CONSTRAINT chat_queue_new_pkey PRIMARY KEY REFERENCES chat_routing.chat_conversation (case_id) ON DELETE RESTRICT,
    case_info              jsonb NOT NULL,
    status                 chat_routing.chat_queue_status NOT NULL DEFAULT 'WAITING_FOR_ENGINEER',
    created_at             timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_chat_queue_waiting_order
    ON chat_routing.chat_queue (created_at, chat_conversation_id)
    WHERE status = 'WAITING_FOR_ENGINEER';

-- ===== chat_queue_engineer_assignment =====
-- Append-only audit trail of assignment outcomes (accept/decline/timeout)
-- -- not written at assignment time, so it can't answer "how many chats
-- was this engineer assigned today" on its own.

CREATE TABLE chat_routing.chat_queue_engineer_assignment (
    id            bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    case_id       text NOT NULL REFERENCES chat_routing.chat_conversation (case_id) ON DELETE RESTRICT,
    engineer_id   text NOT NULL REFERENCES chat_routing.cs_engineer_status (user_id),
    status        chat_routing.assignment_outcome NOT NULL,
    occurred_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_chat_queue_engineer_assignment_case
    ON chat_routing.chat_queue_engineer_assignment (case_id, occurred_at);

CREATE INDEX idx_chat_queue_engineer_assignment_engineer
    ON chat_routing.chat_queue_engineer_assignment (engineer_id, occurred_at);
