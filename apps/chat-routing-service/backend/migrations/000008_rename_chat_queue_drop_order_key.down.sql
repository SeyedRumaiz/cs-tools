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

DROP INDEX IF EXISTS chat_routing.idx_chat_queue_order;

ALTER TABLE chat_routing.chat_queue
  DROP COLUMN requeued,
  ADD COLUMN order_key BIGINT NOT NULL DEFAULT 0;

ALTER TABLE chat_routing.chat_queue RENAME TO escalation_queue;

CREATE INDEX idx_escalation_queue_order ON chat_routing.escalation_queue (order_key, id);
