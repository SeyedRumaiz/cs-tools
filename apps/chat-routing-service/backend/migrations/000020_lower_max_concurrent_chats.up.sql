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

-- Lowers the enforced ceiling on cs_engineer_status.max_concurrent_chats
-- from 20 to 10. 000018_concurrent_chat_capacity.up.sql shipped with a
-- 1-20 range from an earlier reading of the requirement; Sajith has since
-- explicitly confirmed the intended maximum is 10 (asked directly: keep at
-- 10, or lower to 5 -- answer was 10). The default of 1 introduced by that
-- same migration is unchanged here. Per instruction, 000018 itself is not
-- modified (it's already applied) -- this is the next forward migration.
--
-- Any existing row whose max_concurrent_chats is already above the new
-- ceiling (11-20 -- e.g. left over from manual capacity testing, see the
-- project's db-schema-review-2026-09-07-outcomes.md) is clamped down to 10
-- BEFORE the new CHECK is added. Without this, adding a stricter CHECK
-- against existing out-of-range data would simply fail the migration.
UPDATE chat_routing.cs_engineer_status
  SET max_concurrent_chats = 10
  WHERE max_concurrent_chats > 10;

-- The CHECK constraint dropped below was added unnamed, via
-- ALTER TABLE ... ADD COLUMN max_concurrent_chats ... CHECK (...) in
-- 000018_concurrent_chat_capacity.up.sql. Postgres assigns an unnamed,
-- single-column CHECK constraint the default name
-- "<table>_<column>_check" (no other check constraint exists on this table
-- to collide with that name and force a "_check1" suffix). Verify this
-- against the live database before applying --
-- SELECT conname FROM pg_constraint WHERE conrelid =
-- 'chat_routing.cs_engineer_status'::regclass AND contype = 'c'; -- per
-- this project's own "a migration reading correctly is not the same as it
-- running correctly" lesson (db-schema-review-2026-09-07-outcomes.md).
ALTER TABLE chat_routing.cs_engineer_status
  DROP CONSTRAINT cs_engineer_status_max_concurrent_chats_check;

ALTER TABLE chat_routing.cs_engineer_status
  ADD CONSTRAINT chk_max_concurrent_chats
  CHECK (max_concurrent_chats BETWEEN 1 AND 10);
