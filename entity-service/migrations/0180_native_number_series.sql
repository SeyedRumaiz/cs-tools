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

-- Native allocation of human-readable record numbers and work-item wso2_ids,
-- for when this database stops receiving externally allocated numbers.
--
-- Nothing calls these objects yet. The existing portal/GitHub numbering
-- (next_portal_work_item_number, next_portal_wso2_id, next_github_*,
-- migrations 0113/0115/0140) stays live and unchanged while numbers are still
-- allocated externally. Every sequence here starts at 1; a separate cutover
-- script (entity-service/scripts/cutover/seed_native_numbering.sql) moves each
-- one past the highest externally allocated number after the final sync.
--
-- Format: PREFIX + zero-padded digits, e.g. CS0378372. One sequence per
-- series. Numbers are unique per column (work_item_number_key etc.), so a
-- series is a namespace inside one column: every work-item type that shares
-- the CS prefix MUST draw from the same sequence, or two types would mint
-- the same CS number and the second insert would fail.

SET lock_timeout = '5s';

-- The series catalogue. A table rather than a CASE inside the function:
-- the prefix, width and backing sequence of every series are then queryable
-- data, and the cutover script iterates this table instead of carrying its
-- own copy of the list.
CREATE TABLE number_series (
    series        TEXT PRIMARY KEY CHECK (series ~ '^[A-Z]+$'),
    digits        SMALLINT NOT NULL CHECK (digits BETWEEN 1 AND 18),
    sequence_name TEXT NOT NULL UNIQUE
);

-- Plain CREATE SEQUENCE (no IF NOT EXISTS) on purpose: a same-named leftover
-- of unknown value must fail this migration, not silently seed a series
-- (the same hazard 0140 removed by dropping cases_number_seq).
CREATE SEQUENCE cs_number_seq START 1;     -- every case-like work item
CREATE SEQUENCE inc_number_seq START 1;    -- incident
CREATE SEQUENCE prb_number_seq START 1;    -- problem
CREATE SEQUENCE chg_number_seq START 1;    -- change_request
CREATE SEQUENCE task_number_seq START 1;   -- incident_task
CREATE SEQUENCE ctask_number_seq START 1;  -- change_task
CREATE SEQUENCE ptask_number_seq START 1;  -- problem_task
CREATE SEQUENCE ict_number_seq START 1;    -- incident_alert_task
CREATE SEQUENCE chat_number_seq START 1;   -- conversation
CREATE SEQUENCE cstask_number_seq START 1; -- customer_call
CREATE SEQUENCE csprj_number_seq START 1;  -- project
CREATE SEQUENCE acct_number_seq START 1;   -- account
CREATE SEQUENCE esc_number_seq START 1;    -- escalation
CREATE SEQUENCE cmp_number_seq START 1;    -- communication_plan
CREATE SEQUENCE kb_number_seq START 1;     -- knowledge_article
CREATE SEQUENCE ibitm_number_seq START 1;  -- deployed_product
CREATE SEQUENCE dep_number_seq START 1;    -- deployment
CREATE SEQUENCE alt_number_seq START 1;    -- incident_alert

-- OUT reuses outage_number_seq, which native outage creation already draws
-- from (outage_repo.go). No tracked migration created it, so live databases
-- carry a hand-made one: IF NOT EXISTS leaves that untouched. A database
-- built from migrations gets it at 10000, the value that code documents, so
-- natively created outages sit above the externally allocated range while
-- both still exist. A second OUT sequence would collide with that one.
CREATE SEQUENCE IF NOT EXISTS outage_number_seq START 10000;

INSERT INTO number_series (series, digits, sequence_name) VALUES
    ('CS',     7, 'cs_number_seq'),
    ('INC',    7, 'inc_number_seq'),
    ('PRB',    7, 'prb_number_seq'),
    ('CHG',    7, 'chg_number_seq'),
    ('TASK',   7, 'task_number_seq'),
    ('CTASK',  7, 'ctask_number_seq'),
    ('PTASK',  7, 'ptask_number_seq'),
    ('ICT',    7, 'ict_number_seq'),
    ('CSTASK', 7, 'cstask_number_seq'),
    ('CSPRJ',  7, 'csprj_number_seq'),
    ('ACCT',   7, 'acct_number_seq'),
    ('ESC',    7, 'esc_number_seq'),
    ('CMP',    7, 'cmp_number_seq'),
    ('KB',     7, 'kb_number_seq'),
    ('OUT',    7, 'outage_number_seq'),
    ('IBITM',  7, 'ibitm_number_seq'),
    ('DEP',    9, 'dep_number_seq'),
    ('CHAT',   9, 'chat_number_seq'),
    ('ALT',    9, 'alt_number_seq');

-- next_series_number('CS') -> 'CS0000001'. Concurrency-safe by construction:
-- nextval() never hands the same value to two sessions, and is not rolled
-- back, so an aborted insert leaves a gap, never a duplicate.
CREATE OR REPLACE FUNCTION next_series_number(p_series TEXT)
RETURNS TEXT AS $$
DECLARE
    v_digits   SMALLINT;
    v_sequence TEXT;
    v_next     TEXT;
BEGIN
    SELECT digits, sequence_name INTO v_digits, v_sequence
    FROM number_series
    WHERE series = p_series;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'next_series_number: unknown number series %', COALESCE(p_series, '<null>');
    END IF;

    -- GREATEST(width, length): pad up to the width, never truncate past it
    -- (bare LPAD would turn 10000000 into "1000000"; see migration 0140).
    v_next := nextval(v_sequence::regclass)::TEXT;
    RETURN p_series || LPAD(v_next, GREATEST(v_digits, LENGTH(v_next)), '0');
END;
$$ LANGUAGE plpgsql;

-- Which series a work item's number comes from. NULL for a type with no
-- series, so a work-item type added later fails loudly in
-- next_work_item_number instead of borrowing someone else's series.
CREATE OR REPLACE FUNCTION work_item_number_series(p_type work_item_type_enum)
RETURNS TEXT AS $$
    SELECT CASE p_type
        WHEN 'CASE'                     THEN 'CS'
        WHEN 'SERVICE_REQUEST'          THEN 'CS'
        WHEN 'SECURITY_REPORT_ANALYSIS' THEN 'CS'
        WHEN 'ANNOUNCEMENT'             THEN 'CS'
        WHEN 'ENGAGEMENT'               THEN 'CS'
        WHEN 'INCIDENT'                 THEN 'INC'
        WHEN 'PROBLEM'                  THEN 'PRB'
        WHEN 'CHANGE_REQUEST'           THEN 'CHG'
        WHEN 'INCIDENT_TASK'            THEN 'TASK'
        WHEN 'CHANGE_TASK'              THEN 'CTASK'
        WHEN 'PROBLEM_TASK'             THEN 'PTASK'
        WHEN 'INCIDENT_ALERT_TASK'      THEN 'ICT'
        WHEN 'CONVERSATION'             THEN 'CHAT'
    END;
$$ LANGUAGE sql IMMUTABLE PARALLEL SAFE;

CREATE OR REPLACE FUNCTION next_work_item_number(p_type work_item_type_enum)
RETURNS TEXT AS $$
DECLARE
    v_series TEXT := work_item_number_series(p_type);
BEGIN
    IF v_series IS NULL THEN
        RAISE EXCEPTION 'next_work_item_number: no number series for work item type %', p_type;
    END IF;
    RETURN next_series_number(v_series);
END;
$$ LANGUAGE plpgsql;

-- wso2_id: "<project.key>-<per-project counter>", the format externally
-- allocated ids already use. A new counter column rather than reusing
-- portal_wso2_id_counter (0140): that one numbers the "<key>-PORTAL-<n>" ids
-- and keeps that meaning while both schemes are live. A constant default
-- makes this a catalog-only change (no table rewrite).
ALTER TABLE project ADD COLUMN IF NOT EXISTS wso2_id_counter INTEGER NOT NULL DEFAULT 0;

-- One UPDATE ... RETURNING, so concurrent creates in the same project
-- serialise on that project's row lock and each gets its own value; no
-- separate SELECT ... FOR UPDATE needed. The counter does roll back with an
-- aborted transaction, so ids stay gap-free per project. The final guard
-- against any collision (e.g. a counter seeded too low) is the existing
-- UNIQUE constraint work_item_wso2_id_key; no second one is added here.
CREATE OR REPLACE FUNCTION next_wso2_id(p_project_id UUID)
RETURNS TEXT AS $$
DECLARE
    v_project_key  TEXT;
    v_next_counter INTEGER;
BEGIN
    UPDATE project
    SET wso2_id_counter = wso2_id_counter + 1
    WHERE id = p_project_id
    RETURNING key, wso2_id_counter INTO v_project_key, v_next_counter;

    IF v_project_key IS NULL THEN
        RAISE EXCEPTION 'next_wso2_id: no project with id %', p_project_id;
    END IF;

    RETURN v_project_key || '-' || v_next_counter::TEXT;
END;
$$ LANGUAGE plpgsql;

RESET lock_timeout;
