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

-- Requests raised from the WSO2 Cloud tab in the CSM portal. A request is an
-- organization upgrade or delete, or a user lock, unlock, disable, enable or
-- delete, and someone other than the requester approves or rejects it.
-- Replaces the AAP_REQUEST and AAP_APPROVER tables of the Asgardeo Operations
-- Portal.
--
-- This service only stores the requests. The CSM backend does the permission
-- checks and calls the WSO2 Cloud API. When a request is approved, the backend
-- makes the change in WSO2 Cloud first and then marks the row approved.
--
-- An upgrade follows WSO2 Cloud's billing admin API: it moves one
-- subscription (one product of the organization) to an Enterprise plan billed
-- by wire transfer. The billing account is created in WSO2 Cloud only when the
-- request is approved; billing_account_id records it then, so an approval that
-- is retried after a failure reuses it instead of creating a second one.
--
-- type, status and billing_period use the same lowercase values as the API.
--
-- target_label and previous_plan_code are copied in when the request is
-- created, because WSO2 Cloud won't have them later (a deleted org has no name
-- any more, and an approved upgrade replaces the old plan).
--
-- Not synced from ServiceNow. No RLS, since all the routes are internal.
--
-- Uses IF NOT EXISTS / ON CONFLICT throughout, so it can be re-run.

CREATE SEQUENCE IF NOT EXISTS wcr_number_seq START 1;

INSERT INTO number_series (series, digits, sequence_name) VALUES
    ('WCR', 7, 'wcr_number_seq')
ON CONFLICT (series) DO NOTHING;

CREATE TABLE IF NOT EXISTS wso2_cloud_request (
    id                 UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Shown to users, e.g. WCR0000001. Cancelled requests are deleted, but their
    -- numbers are not reused. Expect gaps: an insert rejected by the one-pending
    -- index below still uses up a number from the sequence.
    number             TEXT         NOT NULL DEFAULT next_series_number('WCR'),
    type               VARCHAR(30)  NOT NULL CHECK (type IN (
                                        'upgrade_organization', 'delete_organization',
                                        'lock_user', 'unlock_user', 'disable_user',
                                        'enable_user', 'delete_user')),
    -- WSO2 Cloud org UUID or user SCIM id.
    target_id          VARCHAR(255) NOT NULL CHECK (btrim(target_id) <> ''),
    -- Org handle or user email at the time the request was made.
    target_label       VARCHAR(320) NOT NULL CHECK (btrim(target_label) <> ''),
    -- Needed for lock_user and disable_user (the service checks this). Sent to
    -- WSO2 Cloud as the notes of an upgrade.
    reason             TEXT         CHECK (reason IS NULL OR char_length(reason) <= 2000),
    -- Subject of the customer's consent email, for deletions. Optional.
    customer_consent   TEXT         CHECK (customer_consent IS NULL OR char_length(customer_consent) <= 1000),
    -- Only set for upgrade_organization: the product and subscription being
    -- upgraded, the Enterprise plan asked for and the plan it had.
    product            VARCHAR(100),
    subscription_id    VARCHAR(255),
    plan_code          VARCHAR(100),
    previous_plan_code VARCHAR(100),
    billing_period     VARCHAR(20)  CHECK (billing_period IS NULL OR billing_period IN ('monthly', 'annual')),
    notify_customer    BOOLEAN      NOT NULL DEFAULT false,
    -- The wire billing account created in WSO2 Cloud while approving. Set at
    -- most once.
    billing_account_id VARCHAR(255),
    status             VARCHAR(20)  NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    -- From the caller's token, not the request body.
    requested_by       VARCHAR(255) NOT NULL,
    requested_on       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    resolved_by        VARCHAR(255),
    resolved_on        TIMESTAMPTZ,
    -- Approver's comment. Needed when rejecting (the service checks this).
    remark             TEXT         CHECK (remark IS NULL OR char_length(remark) <= 2000),
    -- Only upgrades name a subscription and a plan.
    CONSTRAINT wso2_cloud_request_upgrade_has_plan
        CHECK ((type = 'upgrade_organization') = (subscription_id IS NOT NULL AND plan_code IS NOT NULL)),
    CONSTRAINT wso2_cloud_request_billing_account_on_upgrade
        CHECK (billing_account_id IS NULL OR type = 'upgrade_organization'),
    -- resolved_by and resolved_on are set once the request is no longer pending.
    CONSTRAINT wso2_cloud_request_resolution_matches_status
        CHECK ((status = 'pending') = (resolved_by IS NULL AND resolved_on IS NULL))
);

CREATE UNIQUE INDEX IF NOT EXISTS wso2_cloud_request_number_key
    ON wso2_cloud_request (number);

-- Only one pending request per type and target, so a second "lock this user"
-- can't be raised while the first one is still waiting.
CREATE UNIQUE INDEX IF NOT EXISTS wso2_cloud_request_one_pending_key
    ON wso2_cloud_request (type, target_id) WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_wso2_cloud_request_status_requested_on
    ON wso2_cloud_request (status, requested_on DESC);

CREATE INDEX IF NOT EXISTS idx_wso2_cloud_request_target_id
    ON wso2_cloud_request (target_id);

COMMENT ON TABLE wso2_cloud_request IS
    'Requests from the WSO2 Cloud tab in the CSM portal. Someone other than the requester approves or rejects each one.';
