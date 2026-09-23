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
