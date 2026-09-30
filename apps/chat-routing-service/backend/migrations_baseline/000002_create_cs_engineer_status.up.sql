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

-- An engineer's manual chat_status and configurable concurrent-chat
-- capacity. Which cases they're actually holding is NOT here -- see
-- chat_conversation.assignee_id (000004).
CREATE TYPE chat_routing.engineer_status AS ENUM (
    'AVAILABLE',
    'BUSY',
    'OFFLINE'
);

CREATE TABLE chat_routing.cs_engineer_status (
    user_id               text PRIMARY KEY,
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
