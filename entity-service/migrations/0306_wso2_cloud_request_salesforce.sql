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

-- The Salesforce side of an organization upgrade (migration 0287).
--
-- An upgrade is a customer upgrade or an internal one. A customer upgrade names
-- the customer's Salesforce Account, which must have an active opportunity when
-- the request is raised and again when it is approved. Once approved, the org's
-- Organization__c record is created or updated under that account (Sales
-- Entity's PUT /organizations/{organizationId}/link), and the outcome is
-- recorded here. An internal upgrade never touches Salesforce.
--
-- Every upgrade names the WSO2 Cloud region the org is in: WSO2 Cloud runs a
-- billing API per region, and the upgrade is applied through that one. The
-- entity service requires it on new upgrades; the check below only forbids it
-- where it means nothing (other request types, older upgrades).
--
-- upgrade_kind is NULL for every other type, and for upgrades raised before
-- this migration (they are treated as internal: no check, no link).
--
-- sf_link_status is NULL until the link is tried, then linked or failed; a
-- failed link keeps its reason in sf_link_error and can be tried again.
--
-- Uses IF NOT EXISTS and guarded constraints, so it can be re-run.

ALTER TABLE wso2_cloud_request
    ADD COLUMN IF NOT EXISTS upgrade_kind   VARCHAR(10),
    -- The customer's Salesforce Account Id, 18 characters as Salesforce spells it.
    ADD COLUMN IF NOT EXISTS sf_account_id  VARCHAR(18),
    -- The WSO2 Cloud region the org is in (us, eu), stored on its Organization__c.
    ADD COLUMN IF NOT EXISTS region         VARCHAR(20),
    ADD COLUMN IF NOT EXISTS sf_link_status VARCHAR(10),
    ADD COLUMN IF NOT EXISTS sf_link_error  TEXT,
    ADD COLUMN IF NOT EXISTS sf_linked_on   TIMESTAMPTZ;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'wso2_cloud_request_upgrade_kind_check') THEN
        ALTER TABLE wso2_cloud_request ADD CONSTRAINT wso2_cloud_request_upgrade_kind_check
            CHECK (upgrade_kind IS NULL OR (type = 'upgrade_organization' AND upgrade_kind IN ('customer', 'internal')));
    END IF;
    -- A customer upgrade names an account and a region; an internal upgrade a
    -- region and no account; nothing else names either. An earlier draft of
    -- this file allowed a region on customer upgrades only, under another name.
    ALTER TABLE wso2_cloud_request DROP CONSTRAINT IF EXISTS wso2_cloud_request_customer_has_account;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'wso2_cloud_request_upgrade_salesforce_fields') THEN
        ALTER TABLE wso2_cloud_request ADD CONSTRAINT wso2_cloud_request_upgrade_salesforce_fields
            CHECK (CASE WHEN upgrade_kind = 'customer' THEN sf_account_id IS NOT NULL AND region IS NOT NULL
                        WHEN upgrade_kind = 'internal' THEN sf_account_id IS NULL
                        ELSE sf_account_id IS NULL AND region IS NULL END);
    END IF;
    -- Only an approved customer upgrade is linked.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'wso2_cloud_request_link_on_approved_customer') THEN
        ALTER TABLE wso2_cloud_request ADD CONSTRAINT wso2_cloud_request_link_on_approved_customer
            CHECK (sf_link_status IS NULL OR (upgrade_kind = 'customer' AND status = 'approved'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'wso2_cloud_request_link_status_check') THEN
        ALTER TABLE wso2_cloud_request ADD CONSTRAINT wso2_cloud_request_link_status_check
            CHECK (sf_link_status IS NULL OR sf_link_status IN ('linked', 'failed'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'wso2_cloud_request_link_error_length') THEN
        ALTER TABLE wso2_cloud_request ADD CONSTRAINT wso2_cloud_request_link_error_length
            CHECK (sf_link_error IS NULL OR char_length(sf_link_error) <= 500);
    END IF;
END $$;
