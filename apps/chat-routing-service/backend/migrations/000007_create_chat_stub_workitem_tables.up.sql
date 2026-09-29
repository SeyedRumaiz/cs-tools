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

-- Local stand-in for entity-service's future generic work_item/
-- chat_conversation/comment schema, reproduced here in chat_routing's own
-- schema so this feature's message/assignment persistence can be built
-- and tested now. Once entity-service ships its real version,
-- csm-portal/backend should move to that instead and these three tables
-- should be dropped -- they're not meant to become a second permanent
-- source of truth.
CREATE TABLE chat_routing.work_item (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  creator_id        TEXT NOT NULL,
  subject           TEXT NOT NULL,
  work_item_number  TEXT,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- case_id is what every message/accept call actually has on hand, so it's
-- the lookup key AddComment and SetConversationEngineer use to find the
-- right work_item.
CREATE TABLE chat_routing.chat_conversation (
  work_item_id     UUID PRIMARY KEY REFERENCES chat_routing.work_item (id),
  conversation_id  TEXT NOT NULL,
  case_id          TEXT NOT NULL,
  -- NULL until the engineer accepts the case.
  engineer_id      TEXT,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- AddComment/SetConversationEngineer both look up by case_id first.
CREATE UNIQUE INDEX idx_chat_conversation_case_id ON chat_routing.chat_conversation (case_id);

CREATE TABLE chat_routing.comment (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  work_item_id  UUID NOT NULL REFERENCES chat_routing.work_item (id),
  content       TEXT NOT NULL,
  created_by    TEXT NOT NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Backs "full transcript for this work item, in order" (DebugWorkItem).
CREATE INDEX idx_comment_work_item_id ON chat_routing.comment (work_item_id, created_at);
