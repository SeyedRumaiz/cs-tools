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

-- opportunity_id was VARCHAR(32) holding a raw sys_id; convert to a typed UUID
-- FK referencing sf_opportunity. Existing values cannot be cast (wrong format);
-- NULL them so the next field_backfill repopulates via sysid_to_uuid.
ALTER TABLE customer_engagement
    ALTER COLUMN opportunity_id TYPE UUID
        USING NULL::UUID;

ALTER TABLE customer_engagement
    ADD CONSTRAINT fk_customer_engagement_opportunity
        FOREIGN KEY (opportunity_id) REFERENCES sf_opportunity(id) ON DELETE SET NULL;
