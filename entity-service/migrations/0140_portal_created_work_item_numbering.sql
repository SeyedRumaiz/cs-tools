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

-- Resolves the product decision CLAUDE.md's "CreateCase and case numbers"
-- section left open: how work_item.number/wso2_id get generated for a
-- record created directly in the portal, as opposed to synced from
-- ServiceNow or filed from a GitHub issue (which already have their own
-- numbering -- see migrations 0115/0113).
--
-- number: real synced numbers are "CS" + 7 digits, in ONE series shared by
-- every work-item type, still being actively allocated by the ServiceNow
-- sync (max observed CS0442200 per CLAUDE.md). Reusing that same format for
-- portal-created records would eventually collide with a freshly-synced
-- number. A visually distinct prefix rules that out by construction -- the
-- same reasoning migration 0115 already used for GitHub-sourced records.
-- cases_number_seq/cases_wso2_id_seq are dropped, not reused, on the shared
-- live database: both are pre-existing manual leftovers (left at 63, unused,
-- not attached to any column, and never created by any tracked migration
-- file) from an earlier abandoned attempt at this same problem -- starting
-- fresh from a clean, documented sequence is safer than inheriting an
-- arbitrary value of unknown provenance. IF EXISTS makes this a no-op on a
-- database that never had them (e.g. one built purely from migrations/).
DROP SEQUENCE IF EXISTS cases_number_seq;
DROP SEQUENCE IF EXISTS cases_wso2_id_seq;

CREATE SEQUENCE IF NOT EXISTS portal_work_item_number_seq START 1;

CREATE OR REPLACE FUNCTION next_portal_work_item_number()
RETURNS TEXT AS $$
DECLARE
    v_next TEXT;
BEGIN
    -- LPAD truncates (from the right) when the input is already longer than
    -- the target width, so a bare LPAD(..., 6, '0') would make sequence
    -- value 1000000 render as "100000" -- identical to value 100000's own
    -- output. GREATEST(6, ...) keeps the usual 6-digit zero-padding below
    -- that point and simply stops padding (never truncates) once the
    -- sequence itself grows past 6 digits.
    v_next := nextval('portal_work_item_number_seq')::TEXT;
    RETURN 'CS-PORTAL-' || LPAD(v_next, GREATEST(6, LENGTH(v_next)), '0');
END;
$$ LANGUAGE plpgsql;

-- wso2_id: required (work_item_wso2_id_required_by_type, migration 0021)
-- for CASE/SERVICE_REQUEST/ANNOUNCEMENT/ENGAGEMENT/SECURITY_REPORT_ANALYSIS.
-- Real wso2_ids are "<project.key>-<per-project counter>"; per CLAUDE.md
-- that per-project counter's sync is also still active and already has
-- gaps, so a fresh counter starting at 1 for a project risks colliding with
-- an id ServiceNow has already assigned (or will assign) for that same
-- project. The project.key portion must stay the real key -- that's what
-- makes the id recognisable as belonging to the project -- so the
-- collision-proofing has to be a distinct marker inside the id instead of a
-- wholesale distinct prefix.
--
-- The counter lives directly on project (not a separate counter table) so
-- incrementing it is one atomic UPDATE ... RETURNING, safe under concurrent
-- creates for the same project without an explicit row lock.
ALTER TABLE project ADD COLUMN IF NOT EXISTS portal_wso2_id_counter INTEGER NOT NULL DEFAULT 0;

CREATE OR REPLACE FUNCTION next_portal_wso2_id(p_project_id UUID)
RETURNS TEXT AS $$
DECLARE
    v_project_key TEXT;
    v_next_counter INTEGER;
BEGIN
    UPDATE project
    SET portal_wso2_id_counter = portal_wso2_id_counter + 1
    WHERE id = p_project_id
    RETURNING key, portal_wso2_id_counter INTO v_project_key, v_next_counter;

    IF v_project_key IS NULL THEN
        RAISE EXCEPTION 'next_portal_wso2_id: no project with id %', p_project_id;
    END IF;

    RETURN v_project_key || '-PORTAL-' || v_next_counter::TEXT;
END;
$$ LANGUAGE plpgsql;
