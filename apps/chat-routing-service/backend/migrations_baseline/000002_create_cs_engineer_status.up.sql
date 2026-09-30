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
