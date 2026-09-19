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

-- Renames escalation_queue to chat_queue and drops order_key: queue order
-- is now derived from created_at instead of a maintained integer
-- sequence.
--
-- A pure created_at sort can't express "requeue at the front" (a declined
-- or timed-out case needs to go back ahead of everyone still waiting,
-- since that customer already waited once). Adding one boolean, requeued,
-- handles it: requeued rows sort ahead of non-requeued ones, and within
-- each group it's plain created_at order. Bonus: enqueueing no longer
-- needs a table lock or MAX/MIN computation.
ALTER TABLE chat_routing.escalation_queue RENAME TO chat_queue;

DROP INDEX IF EXISTS chat_routing.idx_escalation_queue_order;

ALTER TABLE chat_routing.chat_queue
  DROP COLUMN order_key,
  ADD COLUMN requeued BOOLEAN NOT NULL DEFAULT FALSE;

-- Pop-the-head query: ORDER BY requeued DESC, created_at ASC, id ASC.
-- id breaks ties between rows inserted in the same instant.
CREATE INDEX idx_chat_queue_order ON chat_routing.chat_queue (requeued DESC, created_at ASC, id ASC);
