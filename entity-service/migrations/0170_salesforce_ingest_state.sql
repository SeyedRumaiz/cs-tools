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

-- salesforce_ingest_state is the per-record ledger of the Salesforce → CSM
-- ingest for every object that is not a membership. Memberships record the
-- Salesforce version they were written from in onboarding_step; that table is
-- keyed by membership, so accounts, projects and opportunities need their own
-- place to record it. One row per (entity, Salesforce id), holding the latest
-- outcome: the duplicate guard skips an event whose LastModifiedDate is not
-- newer than event_modified_on here, and a FAILED row with its error text is
-- what the onboarding dashboard and the delayed-retry job read.
--
-- entity is the CSM table the record lands in ('account', 'project', ...),
-- not the Salesforce object name, so the same ledger serves every family
-- without the ingest having to know how Salesforce spells the object.
CREATE TABLE IF NOT EXISTS salesforce_ingest_state (
    entity VARCHAR(50) NOT NULL,
    -- Salesforce record Id (18 characters today; sized like the sf_id columns).
    sf_id VARCHAR(100) NOT NULL,
    -- Salesforce LastModifiedDate the last write was based on; an event that
    -- is not newer than this value is a duplicate and is ignored.
    event_modified_on TIMESTAMPTZ NOT NULL,
    -- Salesforce event operation the last write reflects: CREATED, UPDATED,
    -- DELETED or RESTORED. A row stamped DELETED never blocks a later event,
    -- because an undelete keeps the record's LastModifiedDate.
    event_type VARCHAR(20) NOT NULL,
    status VARCHAR(20) NOT NULL,
    last_error TEXT,
    attempt_count INT NOT NULL DEFAULT 1,
    created_on TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_on TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (entity, sf_id),
    CONSTRAINT salesforce_ingest_state_status_check
        CHECK (status IN ('SUCCEEDED', 'FAILED'))
);

-- The delayed-retry job asks for FAILED rows that have not been touched for a
-- while; the primary key leads with entity, so that question needs its own
-- index.
CREATE INDEX IF NOT EXISTS idx_salesforce_ingest_state_status_updated_on
    ON salesforce_ingest_state (status, updated_on);
