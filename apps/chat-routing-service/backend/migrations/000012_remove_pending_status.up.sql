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

-- Removes PENDING from the engineer status enum (added in 000006) --
-- pending status should be derivable rather than stored directly.
-- pending_offline was already a plain boolean, not an enum value, so only
-- PENDING needs to go here.
--
-- What PENDING actually meant was "assignment not yet confirmed by
-- Router.Accept". That's tracked now with a new accepted_at TIMESTAMPTZ
-- column instead of a fourth enum value: current_case_id set with
-- accepted_at still NULL means pending; once Accept runs, accepted_at is
-- set and the engineer reads as genuinely BUSY. See the isPendingAccept/
-- externalStatus helpers in internal/router/state.go.
--
-- This is deliberately not derived from chat_conversation.state (added in
-- 000010) even though it looks similar. chat_conversation is a temporary
-- stand-in table and every write to it is best-effort -- a failed write
-- must never block real routing/timeout behavior. Tying the engineers
-- table's PENDING/BUSY distinction to it could leave an engineer stuck
-- showing BUSY forever if their case's stand-in row never got written.
-- accepted_at lives on engineers itself so this table's state machine has
-- no dependency on the stand-in.
--
-- Postgres has no ALTER TYPE ... DROP VALUE, so the enum is rebuilt:
-- create the 3-value type, migrate the column across it (mapping existing
-- PENDING rows to BUSY, backfilling accepted_at below so already-accepted
-- sessions aren't mistaken for newly-pending ones), then swap it in under
-- the old name.
ALTER TABLE chat_routing.engineers ADD COLUMN accepted_at TIMESTAMPTZ;

-- Backfill: a row already BUSY with a case keeps looking accepted, using
-- updated_at as the closest approximation of the real accept time. PENDING
-- rows are left NULL (that's exactly what "pending" now means), same as
-- idle engineers.
UPDATE chat_routing.engineers SET accepted_at = updated_at
WHERE status = 'BUSY' AND current_case_id IS NOT NULL;

CREATE TYPE chat_routing.engineer_status_new AS ENUM ('AVAILABLE', 'BUSY', 'OFFLINE');

ALTER TABLE chat_routing.engineers ALTER COLUMN status DROP DEFAULT;

-- chk_available_since_only_when_available and idx_engineers_available_since
-- both embed an 'AVAILABLE' literal compiled against the old engineer_status
-- type. The ALTER COLUMN ... TYPE below re-validates them against the new
-- type by comparing the two enum types directly (engineer_status_new =
-- engineer_status) rather than re-compiling the literal, which fails with
-- "operator does not exist". Drop both here, recreate them once the type
-- swap is done.
ALTER TABLE chat_routing.engineers DROP CONSTRAINT chk_available_since_only_when_available;
DROP INDEX chat_routing.idx_engineers_available_since;

ALTER TABLE chat_routing.engineers
  ALTER COLUMN status TYPE chat_routing.engineer_status_new
  USING (CASE WHEN status::text = 'PENDING' THEN 'BUSY' ELSE status::text END)::chat_routing.engineer_status_new;

ALTER TABLE chat_routing.engineers ALTER COLUMN status SET DEFAULT 'OFFLINE';

DROP TYPE chat_routing.engineer_status;
ALTER TYPE chat_routing.engineer_status_new RENAME TO engineer_status;

ALTER TABLE chat_routing.engineers
  ADD CONSTRAINT chk_available_since_only_when_available
  CHECK (available_since IS NULL OR (status = 'AVAILABLE' AND current_case_id IS NULL));

CREATE INDEX idx_engineers_available_since
  ON chat_routing.engineers (available_since)
  WHERE status = 'AVAILABLE' AND current_case_id IS NULL;

ALTER TABLE chat_routing.engineers
  ADD CONSTRAINT chk_accepted_only_with_case
  CHECK (accepted_at IS NULL OR current_case_id IS NOT NULL);
