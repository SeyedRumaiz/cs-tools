-- Combined chat-routing-service migrations for local Postgres setup.
-- One-time local-dev helper -- safe to delete after running.
-- Applies migrations/*.up.sql in numeric order against the SAME
-- database entity-service uses -- everything here is schema-qualified
-- to chat_routing, so it won't collide with entity-service's public
-- schema tables.

-- ===== migrations/000001_create_engineers.up.sql =====
-- This service shares its Postgres database with entity-service, so it
-- keeps its own tables in a dedicated schema to avoid name collisions.
-- The connection's search_path is set to resolve unqualified table names
-- into this schema automatically.
CREATE SCHEMA IF NOT EXISTS chat_routing;

-- Engineer live-chat routing presence, plus the case they're currently
-- handling, if any. One row per engineer email, created on first presence
-- update -- an engineer with no row here just hasn't sent one yet, and the
-- app layer treats that as OFFLINE.
CREATE TYPE chat_routing.engineer_status AS ENUM ('AVAILABLE', 'BUSY', 'OFFLINE');

CREATE TABLE chat_routing.engineers (
  email            TEXT PRIMARY KEY,
  status           chat_routing.engineer_status NOT NULL DEFAULT 'OFFLINE',

  -- Set when a mid-session engineer requests OFFLINE. The actual
  -- transition is deferred until their current session ends.
  pending_offline  BOOLEAN NOT NULL DEFAULT FALSE,

  -- The case this engineer is currently handling, if any. current_case_id
  -- is kept alongside the full current_case JSON blob so we can do fast,
  -- indexable equality checks without parsing JSON every time.
  current_case_id  TEXT,
  current_case     JSONB,

  -- Set to now() whenever this engineer becomes AVAILABLE with no current
  -- case, NULL otherwise. Assignment picks the AVAILABLE engineer with the
  -- oldest available_since, i.e. a FIFO queue expressed as a sort key.
  available_since  TIMESTAMPTZ,

  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

  CONSTRAINT chk_current_case_consistency
    CHECK ((current_case_id IS NULL) = (current_case IS NULL)),
  CONSTRAINT chk_available_since_only_when_available
    CHECK (available_since IS NULL OR (status = 'AVAILABLE' AND current_case_id IS NULL))
);

-- Backs the "pick the longest-idle available engineer" assignment query.
CREATE INDEX idx_engineers_available_since
  ON chat_routing.engineers (available_since)
  WHERE status = 'AVAILABLE' AND current_case_id IS NULL;

-- ===== migrations/000002_create_escalation_queue.up.sql =====
-- FIFO of not-yet-assigned escalations, oldest first. Populated when
-- Escalate finds no AVAILABLE engineer, drained as engineers free up.
-- order_key is a plain sortable integer rather than created_at/id because
-- Decline needs to re-queue a case at the front (that customer already
-- waited once), which a purely chronological key can't express without
-- moving every existing row.
CREATE TABLE chat_routing.escalation_queue (
  id          BIGSERIAL PRIMARY KEY,
  order_key   BIGINT NOT NULL,
  case_id     TEXT NOT NULL,
  case_info   JSONB NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Pop-the-head query: ORDER BY order_key, id. id breaks ties between rows
-- assigned the same order_key by concurrent enqueues.
CREATE INDEX idx_escalation_queue_order ON chat_routing.escalation_queue (order_key, id);

-- ===== migrations/000003_add_sticky_routing_and_chat_counts.up.sql =====
-- Adds sticky customer routing and daily per-engineer chat counts.
-- Escalate now prefers routing a customer back to whoever last handled
-- them (see customer_engineer_assignments below); if that engineer isn't
-- AVAILABLE, or there's no prior assignment, it falls back to whichever
-- AVAILABLE engineer has taken the fewest chats today, ties still broken
-- by available_since.
ALTER TABLE chat_routing.engineers
  ADD COLUMN chats_today       INTEGER NOT NULL DEFAULT 0,

  -- Calendar day chats_today was last incremented on. Compared against
  -- CURRENT_DATE at read/write time instead of a job resetting every row
  -- at midnight -- a stale count from a previous day just reads as 0.
  ADD COLUMN chats_today_date  DATE;

-- One row per customer: which engineer they were most recently assigned
-- to. Updated on every assignment -- sticky match, load-balanced fallback,
-- queue drain, or a decline reassignment all count. Read at the top of
-- Escalate, and ignored if that engineer isn't currently available.
CREATE TABLE chat_routing.customer_engineer_assignments (
  customer_email  TEXT PRIMARY KEY,
  engineer_email  TEXT NOT NULL REFERENCES chat_routing.engineers (email),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_customer_engineer_assignments_engineer
  ON chat_routing.customer_engineer_assignments (engineer_email);

-- ===== migrations/000004_engineer_id_primary_key.up.sql =====
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

-- Switches engineers' primary key from email to engineer_id, the IdP's
-- stable per-account userid claim, not a locally-generated ID. email stays
-- required and unique since most routes still identify an engineer by
-- email; only POST /route/presence (the one place a new row is created)
-- also carries engineerId, since this service has no way to mint that ID
-- itself.
--
-- Presence rows are ephemeral pre-launch state, so instead of backfilling
-- engineer_id for existing rows, this just truncates. CASCADE also clears
-- customer_engineer_assignments, whose FK points at email, so those rows
-- don't dangle once the engineers they reference are gone.
TRUNCATE chat_routing.engineers CASCADE;

-- The FK depends on the PK's backing index specifically, so it has to be
-- dropped before the PK swap and re-attached to the new UNIQUE(email)
-- constraint below -- otherwise dropping the old PK would silently
-- cascade-drop the FK too.
ALTER TABLE chat_routing.customer_engineer_assignments
  DROP CONSTRAINT customer_engineer_assignments_engineer_email_fkey;

ALTER TABLE chat_routing.engineers
  DROP CONSTRAINT engineers_pkey,
  ADD COLUMN engineer_id TEXT NOT NULL,
  ADD CONSTRAINT engineers_pkey PRIMARY KEY (engineer_id),
  ADD CONSTRAINT engineers_email_key UNIQUE (email);

ALTER TABLE chat_routing.customer_engineer_assignments
  ADD CONSTRAINT customer_engineer_assignments_engineer_email_fkey
  FOREIGN KEY (engineer_email) REFERENCES chat_routing.engineers (email);

-- ===== migrations/000005_dynamic_chat_counts.up.sql =====
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

-- Replaces the stored, lazily-reset chats_today/chats_today_date columns
-- with an append-only log queried dynamically (COUNT(*) per engineer per
-- day). Trades an O(1) column read for an indexed COUNT(*), but picks up a
-- free audit trail of who was assigned what and when.
CREATE TABLE chat_routing.assignment_log (
  id           BIGSERIAL PRIMARY KEY,
  email        TEXT NOT NULL REFERENCES chat_routing.engineers (email),
  case_id      TEXT NOT NULL,
  assigned_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Backs the "today" COUNT(*) (email, assigned_at range scan) and any
-- future "history for this engineer" lookup.
CREATE INDEX idx_assignment_log_email_assigned_at
  ON chat_routing.assignment_log (email, assigned_at);

ALTER TABLE chat_routing.engineers
  DROP COLUMN chats_today,
  DROP COLUMN chats_today_date;

-- ===== migrations/000006_add_pending_status.up.sql =====
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

-- Adds PENDING: an engineer a case is assigned to is now marked PENDING,
-- not BUSY, until they explicitly accept it. BUSY is now reserved for an
-- accepted, in-progress session. PENDING and BUSY both count as "has a
-- current case" everywhere that check already exists, so nothing else
-- here needs to change.
ALTER TYPE chat_routing.engineer_status ADD VALUE 'PENDING';

-- ===== migrations/000007_create_chat_stub_workitem_tables.up.sql =====
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

-- ===== migrations/000008_rename_chat_queue_drop_order_key.up.sql =====
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

-- ===== migrations/000009_create_chat_queue_engineer_assignment.up.sql =====
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

-- Persistent audit trail of assignment outcomes: one row per assignment,
-- appended once the outcome is known (CONNECTED on accept, REJECTED on
-- decline, TIMED_OUT on timeout). This service only ever pings one
-- candidate engineer per case at a time, so there's no multi-candidate
-- "awaiting response" state to track here -- append-only, same style as
-- assignment_log.
CREATE TYPE chat_routing.assignment_outcome AS ENUM ('CONNECTED', 'REJECTED', 'TIMED_OUT');

CREATE TABLE chat_routing.chat_queue_engineer_assignment (
  id             BIGSERIAL PRIMARY KEY,
  case_id        TEXT NOT NULL,
  engineer_email TEXT NOT NULL REFERENCES chat_routing.engineers (email),
  status         chat_routing.assignment_outcome NOT NULL,
  occurred_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Backs outcome-history lookups by case and by engineer. No live query
-- uses these yet, but both are an obvious future need for audit data.
CREATE INDEX idx_chat_queue_engineer_assignment_case
  ON chat_routing.chat_queue_engineer_assignment (case_id, occurred_at);
CREATE INDEX idx_chat_queue_engineer_assignment_engineer
  ON chat_routing.chat_queue_engineer_assignment (engineer_email, occurred_at);

-- ===== migrations/000010_chat_conversation_state.up.sql =====
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

-- Adds a lifecycle state to chat_conversation -- previously there was no
-- way to tell an open chat from a finished one. Transition rules live in
-- application code, not here; this migration only adds the column and enum.
--
-- Only OPEN -> ACTIVE is wired up today (Router.Accept). RESOLVED,
-- CONVERTED_CHAT, CONVERTED_CASE, ABANDONED, and CLOSED exist for features
-- that don't exist yet, so the column/enum is ready without another
-- migration later.
CREATE TYPE chat_routing.chat_conversation_state AS ENUM (
  'OPEN', 'ACTIVE', 'RESOLVED', 'CONVERTED_CHAT', 'CONVERTED_CASE', 'ABANDONED', 'CLOSED'
);

ALTER TABLE chat_routing.chat_conversation
  ADD COLUMN state chat_routing.chat_conversation_state NOT NULL DEFAULT 'OPEN';

-- Existing rows: ACTIVE if already accepted (engineer_id set), otherwise OPEN.
UPDATE chat_routing.chat_conversation SET state = 'ACTIVE' WHERE engineer_id IS NOT NULL;

-- conversation_id duplicated work_item_id/case_id and was never queried by;
-- dropped along with the Go code that used to send it.
ALTER TABLE chat_routing.chat_conversation DROP COLUMN conversation_id;

-- ===== migrations/000011_drop_assignment_log.up.sql =====
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

-- assignment_log is redundant -- the "fewest chats today" ranking (see
-- popAvailableEngineer in internal/router/state.go) can be derived directly
-- from chat_conversation (engineer_id, updated_at) instead of maintaining a
-- separate history table.
--
-- This changes the metric slightly: assignment_log counted every case an
-- engineer was assigned, including ones later declined or timed out.
-- chat_conversation.engineer_id is only set once Router.Accept confirms the
-- engineer, so the derived count is "chats accepted today" instead --
-- arguably fairer, but worth knowing if this ranking ever looks off.
DROP TABLE IF EXISTS chat_routing.assignment_log;

-- ===== migrations/000012_remove_pending_status.up.sql =====
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

-- ===== migrations/000013_drop_customer_engineer_assignments.up.sql =====
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

-- Drops customer_engineer_assignments ("sticky routing") -- it's fully
-- derivable rather than something this feature needs to maintain as its own
-- table. Router.Escalate's sticky-engineer preference goes with it; every
-- escalation now goes straight to the least-busy-available ranking (see
-- popAvailableEngineer in internal/router/state.go).
DROP TABLE IF EXISTS chat_routing.customer_engineer_assignments;

-- ===== migrations/000014_rename_engineer_status_table.up.sql =====
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

-- Renames engineers to cs_engineer_status and drops its email column --
-- engineer_id (added in 000004) becomes the sole identifier, renamed to
-- user_id, since email already lives in the user table. status is renamed
-- to chat_status since this table only ever tracked chat presence, not a
-- general engineer status.
--
-- Every caller that used to identify an engineer by email now uses user_id
-- instead: this service's Router methods/routes, its SDK, and csm-portal's
-- chat handlers and SSE hub keys (engineerHubKey) -- see
-- internal/router/state.go.
--
-- chat_queue_engineer_assignment's FK depends on the email-backed unique
-- constraint being dropped below, so it's dropped and re-pointed at the new
-- user_id PK explicitly here, alongside its own case_id/engineer_email ->
-- conversation_id/engineer_id rename.
ALTER TABLE chat_routing.chat_queue_engineer_assignment
  DROP CONSTRAINT chat_queue_engineer_assignment_engineer_email_fkey;

ALTER TABLE chat_routing.engineers RENAME TO cs_engineer_status;
ALTER TABLE chat_routing.cs_engineer_status RENAME COLUMN engineer_id TO user_id;
ALTER TABLE chat_routing.cs_engineer_status DROP COLUMN email;
ALTER TABLE chat_routing.cs_engineer_status RENAME COLUMN status TO chat_status;

ALTER TABLE chat_routing.chat_queue_engineer_assignment RENAME COLUMN case_id TO conversation_id;
ALTER TABLE chat_routing.chat_queue_engineer_assignment RENAME COLUMN engineer_email TO engineer_id;
ALTER TABLE chat_routing.chat_queue_engineer_assignment
  ADD CONSTRAINT chat_queue_engineer_assignment_engineer_id_fkey
  FOREIGN KEY (engineer_id) REFERENCES chat_routing.cs_engineer_status (user_id);

-- ===== migrations/000015_redesign_chat_queue.up.sql =====
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

-- Redesigns chat_queue around a natural key: chat_conversation_id (this
-- service's existing CaseInfo.ConversationID, already available at
-- Router.Escalate time) replaces the bigserial id PK.
--
-- status is now a 2-value enum, WAITING_FOR_ENGINEER / ASSIGNED. One row
-- exists per escalation from creation (Router.Escalate) until Router.Accept
-- confirms the engineer, and is only ever updated in place -- never deleted
-- and re-inserted -- until it's finally removed. That keeps its original
-- created_at through a decline, timeout, or reassignment, which is what
-- makes plain (created_at, chat_conversation_id) ordering enough on its
-- own. `requeued` (added in 000002/000008) is dropped along with it,
-- superseded by the same behavior.
--
-- case_info (JSONB) is kept even though it duplicates data that will
-- eventually live in a real case table, because no such table is queryable
-- from this service yet: entity-service's case data lives elsewhere, and
-- this service's own stand-in work_item/chat_conversation tables aren't
-- populated until after Router.Escalate returns. Dropping case_info now
-- would mean a queued customer loses their subject/message/name. Revisit
-- once those stand-in tables are retired in favor of entity-service's real
-- work item.
CREATE TYPE chat_routing.chat_queue_status AS ENUM ('WAITING_FOR_ENGINEER', 'ASSIGNED');

CREATE TABLE chat_routing.chat_queue_new (
  chat_conversation_id  TEXT PRIMARY KEY,
  case_info             JSONB NOT NULL,
  status                chat_routing.chat_queue_status NOT NULL DEFAULT 'WAITING_FOR_ENGINEER',
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Carry over any in-flight rows using case_id as the new natural key --
-- case_id and conversation_id were always the same value in practice (see
-- enqueueCase in internal/router/state.go). Every row is marked
-- WAITING_FOR_ENGINEER conservatively: the old shape can't tell us which
-- rows were already handed to an engineer, and guessing ASSIGNED risks
-- silently dropping a customer.
INSERT INTO chat_routing.chat_queue_new (chat_conversation_id, case_info, status, created_at)
SELECT case_id, case_info, 'WAITING_FOR_ENGINEER', created_at FROM chat_routing.chat_queue
ON CONFLICT (chat_conversation_id) DO NOTHING;

DROP TABLE chat_routing.chat_queue;
ALTER TABLE chat_routing.chat_queue_new RENAME TO chat_queue;

CREATE INDEX idx_chat_queue_waiting_order
  ON chat_routing.chat_queue (created_at, chat_conversation_id)
  WHERE status = 'WAITING_FOR_ENGINEER';

-- ===== migrations/000016_rename_chat_conversation_assignee.up.sql =====
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

-- Renames chat_conversation.engineer_id to assignee_id, matching the
-- "assignee" naming a normal work item uses -- a chat_conversation can be
-- genuinely unassigned (still AI-handled), same as a work item pre-triage.
-- Pure rename: this column was never foreign-keyed to cs_engineer_status.
ALTER TABLE chat_routing.chat_conversation RENAME COLUMN engineer_id TO assignee_id;

-- ===== migrations/000017_remove_pending_offline.up.sql =====
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

-- Drops pending_offline -- it shouldn't be a stored column at all. An
-- engineer's OFFLINE request now takes effect on chat_status immediately
-- (see SetPresence in internal/router/state.go), instead of being deferred
-- until their session ends.
--
-- That alone would break SweepExpiredPending, which only looked at
-- chat_status = 'BUSY' to find an unconfirmed case to reassign. It now also
-- matches chat_status = 'OFFLINE' rows with an unconfirmed case
-- (current_case_id IS NOT NULL AND accepted_at IS NULL), and Router.Accept's
-- own gate (isStuckPending, replacing isPendingAccept) was widened the same
-- way, so an engineer can still confirm a case after going OFFLINE.
ALTER TABLE chat_routing.cs_engineer_status DROP COLUMN pending_offline;

-- ===== migrations/000018_concurrent_chat_capacity.up.sql =====
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

-- Adds configurable concurrent-chat capacity per engineer (confirmed via
-- the mentor 2026-09-10: real concurrent capacity, not just a data-model
-- cleanup -- see the project's db-schema-review-2026-09-07-outcomes.md,
-- "Resolved (2026-09-10): concurrent chats per engineer, confirmed via
-- mentor"). Base/default capacity is 1, matching today's behavior exactly
-- until a specific engineer's limit is raised.
--
-- cs_engineer_status.current_case_id/current_case/accepted_at assumed
-- exactly one case per engineer. Sajith's own suggestion from the schema
-- review -- derive "is this engineer on a case, and which one(s)" from
-- chat_conversation instead -- is exactly what concurrency needs, so this
-- migration drops those three columns entirely in favor of reading
-- chat_conversation.assignee_id/state/accepted_at directly. See
-- internal/router/state.go for the corresponding application-code changes.
--
-- current_case_id/current_case/accepted_at are referenced by three
-- objects that must be dropped before the columns themselves (same lesson
-- as 000012_remove_pending_status.up.sql's own comment on this exact
-- pitfall): chk_current_case_consistency, idx_engineers_available_since
-- (a partial index), and chk_accepted_only_with_case.
DROP INDEX chat_routing.idx_engineers_available_since;
ALTER TABLE chat_routing.cs_engineer_status DROP CONSTRAINT chk_current_case_consistency;
ALTER TABLE chat_routing.cs_engineer_status DROP CONSTRAINT chk_available_since_only_when_available;
ALTER TABLE chat_routing.cs_engineer_status DROP CONSTRAINT chk_accepted_only_with_case;

ALTER TABLE chat_routing.cs_engineer_status DROP COLUMN current_case_id;
ALTER TABLE chat_routing.cs_engineer_status DROP COLUMN current_case;
ALTER TABLE chat_routing.cs_engineer_status DROP COLUMN accepted_at;

-- chat_status (AVAILABLE/BUSY/OFFLINE) is now a pure manual toggle --
-- do-not-disturb / gone vs. accepting work -- independent of how many
-- cases the engineer is actually holding. Whether they can take another
-- case is answered by comparing an active chat_conversation count against
-- this new limit, not by chat_status. So available_since's own invariant
-- loosens to "set whenever chat_status = AVAILABLE", not "... and idle".
ALTER TABLE chat_routing.cs_engineer_status
  ADD CONSTRAINT chk_available_since_only_when_available
  CHECK (available_since IS NULL OR chat_status = 'AVAILABLE');

CREATE INDEX idx_engineers_available_since
  ON chat_routing.cs_engineer_status (available_since)
  WHERE chat_status = 'AVAILABLE';

-- Per-engineer override, defaulting to 1 (today's behavior, unchanged
-- until someone's limit is deliberately raised). No admin UI to set a
-- non-default value yet -- until one exists, this is a manual UPDATE via
-- pgAdmin, same as every other one-off data change in this project so far.
ALTER TABLE chat_routing.cs_engineer_status
  ADD COLUMN max_concurrent_chats INT NOT NULL DEFAULT 1
  CHECK (max_concurrent_chats BETWEEN 1 AND 20);

-- chat_conversation gains its own accepted_at (replacing the one dropped
-- above -- confirmation is now tracked per conversation, since an engineer
-- can have several) and session_ended_at, which marks a chat session as
-- over WITHOUT touching `state`. Deliberately kept separate from `state`:
-- HandleCompleteSession's own design already treats "the live chat session
-- ended" as distinct from "the case is resolved/closed" (an engineer can
-- end the chat while the underlying case stays open) -- reusing `state` for
-- this would conflate the two. session_ended_at IS NULL is what "counts
-- toward this engineer's active-chat capacity" actually means.
ALTER TABLE chat_routing.chat_conversation ADD COLUMN accepted_at TIMESTAMPTZ;
ALTER TABLE chat_routing.chat_conversation ADD COLUMN session_ended_at TIMESTAMPTZ;

-- Also gains its own case_info JSONB -- the full display blob (subject,
-- customer email/name, message) that used to live only in
-- cs_engineer_status.current_case (dropped above) and, transiently, in
-- chat_queue.case_info (deleted at Accept). Without a durable copy here,
-- GetPresence could no longer rehydrate an ALREADY-ACCEPTED session's
-- details after a refresh, since chat_queue's own row is long gone by
-- then. Populated once, at CreateWorkItem time (before any assignment),
-- so it's present for the row's whole lifecycle -- queued, assigned, and
-- accepted alike.
ALTER TABLE chat_routing.chat_conversation ADD COLUMN case_info JSONB;

-- Backs both the capacity/active-count lookups (popAvailableEngineer,
-- SetPresence's queue-drain loop) and the timeout sweep's scan for
-- assigned-but-unconfirmed conversations.
CREATE INDEX idx_chat_conversation_active_assignee
  ON chat_routing.chat_conversation (assignee_id, state)
  WHERE assignee_id IS NOT NULL AND session_ended_at IS NULL;

-- ===== migrations/000019_add_entity_case_id.up.sql =====
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

-- Records the real entity-service case ID once a chat is converted into
-- one (Router.ConvertToCase). No FK, since that table lives in a different
-- service's data model. NULL for the life of a chat that never converts.
ALTER TABLE chat_routing.chat_conversation
  ADD COLUMN entity_case_id TEXT NULL;

-- ===== migrations/000020_lower_max_concurrent_chats.up.sql =====
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


-- ===== migrations/000021_split_case_and_conversation_identity.up.sql =====
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

-- Splits case identity (one live-engineer-chat escalation instance) from
-- conversation identity (the stable Novera AI-chat conversation it came
-- from). Until now these were always the same value -- backend-v2 sent its
-- own conversationId as caseId on every escalation (see customer-portal/
-- backend-v2's HandleEscalate, before this change) -- so chat_conversation
-- never needed its own conversation_id column. In fact it had one once,
-- but it was dropped in 000010_chat_conversation_state.up.sql as
-- "duplicated work_item_id/case_id and never queried by". Now that a
-- single Novera conversation can produce more than one caseId over its
-- lifetime (a fresh UUID per new escalation -- see backend-v2's
-- HandleEscalate), the two are no longer interchangeable and this column
-- is reintroduced for real use: every read path that needs to tell a
-- customer's AI-chat connection from their live-chat case now has a place
-- to get it from other than case_id.
--
-- Forward-only migration, per instruction -- no down.sql. This is a
-- one-way step in this feature's evolution, not a toggle.

-- Step 1: add the column, nullable at first so the backfill below can run
-- before the NOT NULL constraint is enforced.
ALTER TABLE chat_routing.chat_conversation ADD COLUMN conversation_id TEXT;

-- Step 2: backfill every existing row. Every row created before this
-- migration was created back when caseId and conversationId were forced
-- equal at the point of origin (customer-portal/backend-v2's old
-- HandleEscalate: `CaseID: req.ConversationID`), so case_id IS the correct
-- historical conversation_id for these rows -- there is no other source to
-- backfill from, and none is needed.
UPDATE chat_routing.chat_conversation SET conversation_id = case_id WHERE conversation_id IS NULL;

-- Step 3: enforce NOT NULL now that every row has a value. Every future
-- INSERT (router.Router.CreateWorkItem) supplies this from CaseInfo.
-- ConversationID going forward.
ALTER TABLE chat_routing.chat_conversation ALTER COLUMN conversation_id SET NOT NULL;

-- Step 4: index it -- nothing queries by conversation_id yet inside this
-- service (every internal lookup here is still by case_id), but
-- csm-portal/backend's own future needs (e.g. "every case that ever
-- belonged to this Novera conversation") and ad hoc support queries
-- against this column should not require a sequential scan.
CREATE INDEX idx_chat_conversation_conversation_id
  ON chat_routing.chat_conversation (conversation_id);

-- Step 5: duplicate-open-chat protection. Enforce at most one non-ended
-- live chat per (customerEmail, projectId) pair -- a customer should never
-- be able to have two concurrent pending/active escalations for the same
-- project, whether from two browser tabs racing each other or from a
-- retried request. Application code (router.Router.CreateWorkItem) already
-- checks this before inserting; this partial unique index is the race-safe
-- backstop for the window between that check and the insert.
--
-- Scoped to WHERE session_ended_at IS NULL so a customer is free to start a
-- new live chat as soon as their previous one has ended or converted --
-- this index only ever constrains currently-open rows, never historical
-- ones.
--
-- COALESCE(..., '') is defensive, not a statement that empty values are
-- valid: a NULL in either side of a multi-column unique index means
-- Postgres treats that row as never conflicting with any other row (NULL
-- is never equal to NULL for uniqueness purposes), which would silently
-- leave a row with a missing customerEmail or projectId completely
-- unprotected -- coalescing to '' instead makes such a row still fall
-- into a (shared, degenerate) uniqueness domain, failing closed rather
-- than open. This does not replace validating that both fields are always
-- present on the way in -- see router.Router.CreateWorkItem's own
-- application-level check, which rejects a request missing either field
-- before it ever reaches this index.
--
-- Verify before applying that no existing data would violate this -- run
-- the following against the live database and confirm it returns zero
-- rows:
--
--   SELECT COALESCE(case_info->>'customerEmail', ''),
--          COALESCE(case_info->>'projectId', ''),
--          COUNT(*)
--   FROM chat_routing.chat_conversation
--   WHERE session_ended_at IS NULL
--   GROUP BY 1, 2
--   HAVING COUNT(*) > 1;
--
-- If that returns any rows, end (session_ended_at = now()) every but the
-- most recent chat_conversation row in each duplicate group before running
-- this migration -- see the project's db-schema-review psql notes for the
-- established pattern of confirming a specific duplicate with the
-- customer/case history before touching any row.
CREATE UNIQUE INDEX uq_chat_conversation_open_customer_project
  ON chat_routing.chat_conversation (
    (COALESCE(case_info ->> 'customerEmail', '')),
    (COALESCE(case_info ->> 'projectId', ''))
  )
  WHERE session_ended_at IS NULL;
