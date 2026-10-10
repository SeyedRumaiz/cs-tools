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

-- Transactional outbox for row changes, so csm-flow-service can react to data
-- changing without polling every table or being told about them out of band.
--
-- WHY A TRIGGER AND NOT APPLICATION CODE: rows reach these tables by several
-- paths -- the loader's bulk CopyFrom merge, its row-by-row fallback, its
-- update_only mode, and whatever writes them after ServiceNow is gone. A
-- trigger sees all of them. Diffing in one writer would miss the others, and
-- the loader in particular does a blind upsert that never reads the old row,
-- so it could not produce a before/after even if asked.
--
-- WHY AN OUTBOX AND NOT LISTEN/NOTIFY: NOTIFY is fire-and-forget. A listener
-- that is disconnected -- a redeploy, a network blip -- misses every event sent
-- meanwhile, with no way to discover what it missed. An approval notice is not
-- something to lose to a rolling restart. Rows here persist until a consumer
-- claims and marks them, so a restart resumes rather than skips.

-- Wrapped in a transaction deliberately. `make migrate` runs each file with
-- `psql -f`, not `--single-transaction`, so every statement would otherwise
-- commit on its own. For a file that drops and recreates a trigger that means
-- a window where the table has no trigger at all, and a write landing in it
-- produces no outbox row and no notice -- silently, with nothing to retry.
--
-- Postgres makes DDL transactional, so the swap becomes one step: a concurrent
-- write waits for the lock instead of slipping through the gap. It also makes
-- the whole migration all-or-nothing, rather than half-applied and unrecorded
-- in csm_migration_applied_migration if a later statement fails.
BEGIN;

CREATE TABLE IF NOT EXISTS event_outbox (
    id           BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_type  TEXT        NOT NULL,
    entity_id    UUID        NOT NULL,
    -- changes is {"<column>": {"from": …, "to": …}} for the columns that
    -- actually differ; snapshot is the row as it now stands. Together they are
    -- the entity.changed payload csm-flow-service already consumes, so the
    -- publisher is a shape-preserving relay rather than a translator.
    changes      JSONB       NOT NULL,
    snapshot     JSONB       NOT NULL,
    occurred_on  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Null until a consumer has published it. No status column: published_on
    -- being set IS the status, the same derivation scheduled_task_run and
    -- sla_clocks use.
    published_on TIMESTAMPTZ
);

-- The drain query: oldest unpublished first. Partial, because a drained outbox
-- is almost entirely published rows and the index should not carry them.
CREATE INDEX IF NOT EXISTS idx_event_outbox_unpublished
    ON event_outbox (id)
    WHERE published_on IS NULL;

-- Emits one outbox row per UPDATE that actually changed something, with only
-- the differing columns in `changes`. An UPDATE that rewrites a row to its own
-- values -- which the loader's upsert does constantly, since it re-merges every
-- synced record whether or not it moved -- produces nothing. Without that
-- guard, every sync tick would look like a change to every consumer.
CREATE OR REPLACE FUNCTION trg_event_outbox() RETURNS trigger AS $$
DECLARE
    diff JSONB := '{}'::jsonb;
    col  TEXT;
    oldv JSONB := to_jsonb(OLD);
    newv JSONB := to_jsonb(NEW);
BEGIN
    FOR col IN SELECT jsonb_object_keys(newv) LOOP
        -- updated_on moves on every upsert regardless of content, so treating
        -- it as a change would defeat the no-op guard entirely.
        IF col <> 'updated_on' AND oldv -> col IS DISTINCT FROM newv -> col THEN
            diff := diff || jsonb_build_object(col, jsonb_build_object('from', oldv -> col, 'to', newv -> col));
        END IF;
    END LOOP;

    IF diff = '{}'::jsonb THEN
        RETURN NULL;
    END IF;

    INSERT INTO event_outbox (entity_type, entity_id, changes, snapshot)
    VALUES (TG_TABLE_NAME, NEW.id, diff, newv);

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

-- Attached to change_request only for now. Adding a table is one more CREATE
-- TRIGGER and nothing else -- the function is table-agnostic via TG_TABLE_NAME.
DROP TRIGGER IF EXISTS change_request_outbox ON change_request;
CREATE TRIGGER change_request_outbox
    AFTER UPDATE ON change_request
    FOR EACH ROW EXECUTE FUNCTION trg_event_outbox();

COMMIT;
