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

-- Reverses 000020_lower_max_concurrent_chats.up.sql: restores the original
-- 1-20 range (with its original, unnamed-but-deterministic constraint
-- name) and named constraint this migration used. Does not and cannot
-- restore any specific value the up-migration's clamp UPDATE lowered --
-- an engineer whose max_concurrent_chats was, say, 15 before the up
-- migration ran ends up at 10 there and stays at 10 here; only the
-- allowed *range* is restored, not that lost data.
ALTER TABLE chat_routing.cs_engineer_status
  DROP CONSTRAINT chk_max_concurrent_chats;

ALTER TABLE chat_routing.cs_engineer_status
  ADD CONSTRAINT cs_engineer_status_max_concurrent_chats_check
  CHECK (max_concurrent_chats BETWEEN 1 AND 20);
