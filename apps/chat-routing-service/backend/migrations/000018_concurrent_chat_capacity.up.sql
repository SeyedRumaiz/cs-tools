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
