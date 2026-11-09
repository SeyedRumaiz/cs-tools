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

-- wso2_cloud_request: the requests raised from the CSM portal's WSO2 Cloud tab
-- (upgrade or delete an organization; lock, unlock, disable, enable or delete a
-- user). Every change to a WSO2 Cloud organization or user is a request that
-- another person approves or rejects; this table is the record of them. It
-- replaces the Asgardeo Operations Portal's AAP_REQUEST / AAP_APPROVER tables
-- for this work.
--
-- Entity-service only stores the requests. The CSM backend decides who may
-- raise and resolve them, and calls the WSO2 Cloud API: on approval it applies
-- the change there first and records the approval here after.
--
-- type / status / tier hold the API's own lower-case spelling, so no mapping
-- layer sits between the column and the wire.
--
-- target_label, previous_tier and previous_limits are snapshots taken when the
-- request is raised: once an organization or user is deleted its name is gone
-- from WSO2 Cloud, and once an upgrade is approved the live tier is the new one.
--
-- Entity-service owns this table; it is not a ServiceNow sync table. No
-- row-level security: every route is internal-only.
--
-- Additive and idempotent: safe to re-run.

CREATE SEQUENCE IF NOT EXISTS wcr_number_seq START 1;

INSERT INTO number_series (series, digits, sequence_name) VALUES
    ('WCR', 7, 'wcr_number_seq')
ON CONFLICT (series) DO NOTHING;

CREATE TABLE IF NOT EXISTS wso2_cloud_request (
    id               UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    -- WCR0000001: shown to people, never reused (a cancelled request is deleted,
    -- its number is not given out again). Numbers can have gaps: an insert the
    -- one-pending rule refuses has already taken its number from the sequence.
    number           TEXT         NOT NULL DEFAULT next_series_number('WCR'),
    type             VARCHAR(30)  NOT NULL CHECK (type IN (
                                      'upgrade_organization', 'delete_organization',
                                      'lock_user', 'unlock_user', 'disable_user',
                                      'enable_user', 'delete_user')),
    -- The organization's UUID or the user's SCIM id in WSO2 Cloud.
    target_id        VARCHAR(255) NOT NULL CHECK (btrim(target_id) <> ''),
    -- The organization's handle or the user's email, as it was when raised.
    target_label     VARCHAR(320) NOT NULL CHECK (btrim(target_label) <> ''),
    -- Required for lock_user and disable_user (checked by the service).
    reason           TEXT         CHECK (reason IS NULL OR char_length(reason) <= 2000),
    -- Optional: the subject of the customer's consent email, for a deletion.
    customer_consent TEXT         CHECK (customer_consent IS NULL OR char_length(customer_consent) <= 1000),
    -- upgrade_organization only: what is asked for, and what it was when raised.
    tier             VARCHAR(20)  CHECK (tier IS NULL OR tier IN ('free', 'payg', 'enterprise')),
    limits           JSONB        CHECK (limits IS NULL OR jsonb_typeof(limits) = 'object'),
    previous_tier    VARCHAR(20)  CHECK (previous_tier IS NULL OR previous_tier IN ('free', 'payg', 'enterprise')),
    previous_limits  JSONB        CHECK (previous_limits IS NULL OR jsonb_typeof(previous_limits) = 'object'),
    status           VARCHAR(20)  NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    -- Taken from the caller's token, never the request body.
    requested_by     VARCHAR(255) NOT NULL,
    requested_on     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    resolved_by      VARCHAR(255),
    resolved_on      TIMESTAMPTZ,
    -- The approver's comment; required to reject (checked by the service).
    remark           TEXT         CHECK (remark IS NULL OR char_length(remark) <= 2000),
    -- An upgrade carries a tier; no other type does.
    CONSTRAINT wso2_cloud_request_upgrade_has_tier
        CHECK ((type = 'upgrade_organization') = (tier IS NOT NULL)),
    -- A pending request is unresolved; a resolved one says who and when.
    CONSTRAINT wso2_cloud_request_resolution_matches_status
        CHECK ((status = 'pending') = (resolved_by IS NULL AND resolved_on IS NULL))
);

CREATE UNIQUE INDEX IF NOT EXISTS wso2_cloud_request_number_key
    ON wso2_cloud_request (number);

-- One pending request per type and target: a second "lock this user" while the
-- first is still waiting is refused.
CREATE UNIQUE INDEX IF NOT EXISTS wso2_cloud_request_one_pending_key
    ON wso2_cloud_request (type, target_id) WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_wso2_cloud_request_status_requested_on
    ON wso2_cloud_request (status, requested_on DESC);

CREATE INDEX IF NOT EXISTS idx_wso2_cloud_request_target_id
    ON wso2_cloud_request (target_id);

COMMENT ON TABLE wso2_cloud_request IS
    'Requests raised from the CSM portal''s WSO2 Cloud tab, each approved or rejected by another person.';
