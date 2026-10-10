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

DO $$ BEGIN
    CREATE TYPE customer_engagement_type_enum AS ENUM (
        'FIREFIGHTING',
        'CONSULTANCY',
        'TRAINING',
        'ENTERPRISE_CSM_TAM',
        'ARCHITECTURE_REVIEW',
        'CUSTOMER_ONBOARDING',
        'QSP'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- Rename from engagement_type_id (raw sys_id) to engagement_type (enum label).
-- Existing rows held sys_ids that cannot cast to enum values; NULL them out so
-- the next field_backfill repopulates correctly via u_type.u_name.
ALTER TABLE customer_engagement
    RENAME COLUMN engagement_type_id TO engagement_type;

ALTER TABLE customer_engagement
    ALTER COLUMN engagement_type TYPE customer_engagement_type_enum
        USING NULL::customer_engagement_type_enum;
