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

-- Customer-health risk tracking, migrated off a standalone MySQL database
-- (apps/csm-portal/backend's own internal/risk package, the monorepo's only
-- other database dependency) onto entity-service's Postgres. project_id/
-- account_id are the MySQL source's project_sys_id/account_sys_id (an
-- upstream sys_id) reformatted with dashes into UUID syntax -- the same
-- convention entity-service already uses for every upstream-sourced row --
-- so they reference project(id)/account(id) directly rather than carrying a
-- separate sys_id column. Enum values are UPPERCASE, entity-service's own
-- convention, not the MySQL source's lowercase; the Go code reading these
-- tables must compare against the new values.
--
-- These 4 tables don't carry entity-service's generic created_on/updated_on/
-- created_by/updated_by audit quartet: the source data has its own
-- domain-specific equivalents instead (opened_on/opened_by_email,
-- reviewed_on/reviewed_by_email, ...), and that quartet isn't optional
-- metadata elsewhere in this schema, so it isn't force-fitted here.

BEGIN;

DO $$ BEGIN
    CREATE TYPE project_health_status_enum AS ENUM ('TO_BE_REVIEWED', 'HEALTHY', 'AT_RISK');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE project_risk_status_enum AS ENUM ('OPEN', 'CLOSED');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE risk_action_item_priority_enum AS ENUM ('HIGH', 'MEDIUM', 'LOW');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE risk_action_item_status_enum AS ENUM ('OPEN', 'IN_PROGRESS', 'RESOLVED', 'CANCELLED');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

CREATE TABLE IF NOT EXISTS project_health_status (
    id UUID PRIMARY KEY,
    project_id UUID NOT NULL REFERENCES project(id),
    account_id UUID NOT NULL REFERENCES account(id),
    status project_health_status_enum NOT NULL DEFAULT 'TO_BE_REVIEWED',
    reviewed_by_email VARCHAR(255),
    reviewed_on TIMESTAMPTZ,
    UNIQUE (project_id)
);

CREATE INDEX IF NOT EXISTS idx_project_health_status_account_id ON project_health_status (account_id);
CREATE INDEX IF NOT EXISTS idx_project_health_status_status ON project_health_status (status);

CREATE TABLE IF NOT EXISTS project_risk (
    id UUID PRIMARY KEY,
    project_id UUID NOT NULL REFERENCES project(id),
    account_id UUID NOT NULL REFERENCES account(id),
    status project_risk_status_enum NOT NULL DEFAULT 'OPEN',
    opened_comment TEXT NOT NULL,
    opened_by_email VARCHAR(255) NOT NULL,
    opened_on TIMESTAMPTZ NOT NULL,
    closed_comment TEXT,
    closed_by_email VARCHAR(255),
    closed_on TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_project_risk_project_id ON project_risk (project_id);
CREATE INDEX IF NOT EXISTS idx_project_risk_account_id ON project_risk (account_id);
CREATE INDEX IF NOT EXISTS idx_project_risk_status ON project_risk (status);

CREATE TABLE IF NOT EXISTS risk_action_item (
    id UUID PRIMARY KEY,
    risk_id UUID NOT NULL REFERENCES project_risk(id),
    project_id UUID NOT NULL REFERENCES project(id),
    account_id UUID NOT NULL REFERENCES account(id),
    title VARCHAR(500) NOT NULL,
    description TEXT,
    priority risk_action_item_priority_enum NOT NULL DEFAULT 'MEDIUM',
    status risk_action_item_status_enum NOT NULL DEFAULT 'OPEN',
    assigned_to_email VARCHAR(255),
    due_date DATE,
    resolution_comment TEXT,
    resolved_by_email VARCHAR(255),
    resolved_on TIMESTAMPTZ,
    created_by_email VARCHAR(255) NOT NULL,
    created_on TIMESTAMPTZ NOT NULL,
    updated_on TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_risk_action_item_risk_id ON risk_action_item (risk_id);
CREATE INDEX IF NOT EXISTS idx_risk_action_item_project_id ON risk_action_item (project_id);
CREATE INDEX IF NOT EXISTS idx_risk_action_item_account_id ON risk_action_item (account_id);
CREATE INDEX IF NOT EXISTS idx_risk_action_item_status ON risk_action_item (status);

CREATE TABLE IF NOT EXISTS action_item_comment (
    id UUID PRIMARY KEY,
    action_item_id UUID NOT NULL REFERENCES risk_action_item(id),
    comment TEXT NOT NULL,
    created_by_email VARCHAR(255) NOT NULL,
    created_on TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_action_item_comment_action_item_id ON action_item_comment (action_item_id);

COMMIT;
