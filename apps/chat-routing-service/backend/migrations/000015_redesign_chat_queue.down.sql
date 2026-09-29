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

-- ASSIGNED rows have no equivalent in the old shape (a popped row was
-- deleted outright, not tracked), so they're dropped here rather than
-- carried over.
CREATE TABLE chat_routing.chat_queue_old (
  id          BIGSERIAL PRIMARY KEY,
  requeued    BOOLEAN NOT NULL DEFAULT FALSE,
  case_id     TEXT NOT NULL,
  case_info   JSONB NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO chat_routing.chat_queue_old (case_id, case_info, requeued, created_at)
SELECT chat_conversation_id, case_info, false, created_at
FROM chat_routing.chat_queue
WHERE status = 'WAITING_FOR_ENGINEER';

DROP TABLE chat_routing.chat_queue;
ALTER TABLE chat_routing.chat_queue_old RENAME TO chat_queue;
DROP TYPE chat_routing.chat_queue_status;

CREATE INDEX idx_chat_queue_order ON chat_routing.chat_queue (requeued DESC, created_at ASC, id ASC);
