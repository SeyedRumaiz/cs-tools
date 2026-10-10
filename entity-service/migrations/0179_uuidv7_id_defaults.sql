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

-- Time-ordered primary keys for rows this database creates itself.
--
-- Today most root-table ids are supplied by the caller: they are copied from
-- the source system on sync, so `id UUID PRIMARY KEY` was declared with no
-- default. Once this database allocates ids on its own, a random (v4) UUID
-- scatters every insert across the whole primary-key B-tree. UUIDv7 leads
-- with a millisecond timestamp, so new keys land on the right-hand edge of
-- the index: fewer page splits, a hot set that fits in cache, and WAL that
-- does not grow with index size.
--
-- Scope, and what is deliberately NOT touched:
--   * Only the column DEFAULT changes. Existing ids are never rewritten, and
--     a caller that still supplies an id keeps winning over the default.
--   * Tables whose id is itself `REFERENCES work_item(id)` ("case",
--     incident, change_request, ...) keep NO default: their id must be the
--     parent work_item's id, and a default there could only ever mask a
--     missing value.
--   * Integer/identity-keyed tables are untouched.
--   * Tables already defaulting to gen_random_uuid() switch only when they
--     are append-heavy (event/log/history/delivery rows written per action
--     or per run). Catalogue and configuration tables (subcategories,
--     playbook templates, schedule zones/shifts, saved filters, ...) stay as
--     they are: a handful of rows gains nothing from ordered keys.
--   * cloud_status_events is append-heavy but is left out: it is created by
--     the legacy-numbered 000086 migration, which some runners apply before
--     the 4-digit files and others after, so an ALTER here would either fail
--     or leave databases disagreeing on the default. Switch it in a migration
--     numbered after the legacy files are renumbered.
--
-- Note: a UUIDv7 discloses its creation time to anyone who can see the id.
-- For these tables that is the same information created_on already exposes.
--
-- Requires PostgreSQL 18 (uuidv7() is built in from 18). Fail with a clear
-- message instead of "function uuidv7() does not exist" on an older server.
DO $$
BEGIN
    IF current_setting('server_version_num')::int < 180000 THEN
        RAISE EXCEPTION 'migration 0179 requires PostgreSQL 18 or later (found %)',
            current_setting('server_version');
    END IF;
END
$$;

-- SET DEFAULT is catalog-only (no table rewrite, no scan) but still takes a
-- brief ACCESS EXCLUSIVE lock per table. Fail fast instead of queueing behind
-- a long-running query and stalling every reader of that table behind us.
-- Each statement is idempotent, so a timed-out run is simply re-run.
SET lock_timeout = '5s';

-- (i) Root tables whose id had no default.
ALTER TABLE account ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE account_contact ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE account_github_repo ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE account_relationship ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE approval_stage ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE approval_stage_approver ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE case_escalation ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE case_escalation_notification_list ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE catalog_item ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE catalog_item_category ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE catalog_variable ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE cloud_monitor ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE comment ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE communication_plan ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE communication_task_definition ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE customer_call ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE customer_engagement ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE customer_engagement_allocation_resource ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE customer_engagement_status_update ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE daily_usage_summary ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE deployed_product ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE deployment ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE deployment_information ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE deployment_node ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE "group" ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE hourly_usage_summary ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE incident_alert ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE knowledge_article ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE knowledge_base ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE monthly_usage_summary ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE outage ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE outage_affected_ci ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE product ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE product_usage_map ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE product_version ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE product_vulnerability ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE project ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE project_contact ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE project_contact_group ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE project_group ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE project_group_role ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE project_role ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE project_type ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE role ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE schedule ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE schedule_span ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE service ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE service_availability ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE service_commitment ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE service_offering ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE service_portfolio ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE sf_invoice ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE sf_opportunity ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE sf_opportunity_link ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE sf_opportunity_product ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE skill ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE skill_category ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE skill_category_link ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE skill_level ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE skill_level_type ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE sla ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE sla_policy ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE sr_category ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE sr_category_routing_rule ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE tag ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE team ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE team_member ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE team_schedule_absence_activity ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE team_schedule_assignment_activity ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE time_card ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE time_card_approver ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE "user" ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE user_role ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE user_schedule ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE user_skill ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE user_skill_history ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE work_item ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE work_item_activity ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE work_item_attachment ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE work_item_feedback ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE work_item_tag ALTER COLUMN id SET DEFAULT uuidv7();
ALTER TABLE work_item_watcher ALTER COLUMN id SET DEFAULT uuidv7();

-- (ii) Append-heavy tables switching from gen_random_uuid().
ALTER TABLE alert_incident_mapping ALTER COLUMN id SET DEFAULT uuidv7();          -- one row per ingested monitoring alert
ALTER TABLE announcement_request_deliveries ALTER COLUMN id SET DEFAULT uuidv7(); -- one row per recipient per announcement
ALTER TABLE announcement_request_updates ALTER COLUMN id SET DEFAULT uuidv7();    -- status history per announcement request
ALTER TABLE case_attachment ALTER COLUMN id SET DEFAULT uuidv7();                 -- grows with case traffic
ALTER TABLE comment_edit_history ALTER COLUMN id SET DEFAULT uuidv7();            -- one row per comment edit
ALTER TABLE event_publish_failures ALTER COLUMN id SET DEFAULT uuidv7();          -- failure log
ALTER TABLE outage_communication_log ALTER COLUMN id SET DEFAULT uuidv7();        -- send log
ALTER TABLE plg_ingest_failure ALTER COLUMN id SET DEFAULT uuidv7();              -- failure log
ALTER TABLE plg_lifecycle_history ALTER COLUMN id SET DEFAULT uuidv7();           -- append-only history
ALTER TABLE plg_note ALTER COLUMN id SET DEFAULT uuidv7();                        -- comment trail, no delete
ALTER TABLE plg_note_revision ALTER COLUMN id SET DEFAULT uuidv7();               -- one row per note edit
ALTER TABLE plg_playbook_run ALTER COLUMN id SET DEFAULT uuidv7();                -- one row per run started
ALTER TABLE plg_playbook_run_task ALTER COLUMN id SET DEFAULT uuidv7();           -- task copies per run
ALTER TABLE scheduled_task_run ALTER COLUMN id SET DEFAULT uuidv7();              -- one row per scheduled job run
ALTER TABLE sn_writeback_failures ALTER COLUMN id SET DEFAULT uuidv7();           -- failure log

RESET lock_timeout;
