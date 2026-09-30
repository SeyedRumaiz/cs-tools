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
