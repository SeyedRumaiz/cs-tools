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
