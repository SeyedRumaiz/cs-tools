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
