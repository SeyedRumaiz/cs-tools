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

-- u_line_item_id (a plain string identifier) moves to sf_id; u_line_item (the
-- reference field) becomes a typed UUID FK on sf_opportunity_product. Existing
-- line_item_id_ref values were raw sys_ids and cannot be cast; NULL them so
-- the next field_backfill repopulates via sysid_to_uuid.
ALTER TABLE customer_engagement
    RENAME COLUMN line_item_id TO sf_id;

ALTER TABLE customer_engagement
    RENAME COLUMN line_item_id_ref TO line_item_id;

ALTER TABLE customer_engagement
    ALTER COLUMN line_item_id TYPE UUID
        USING NULL::UUID;

ALTER TABLE customer_engagement
    ADD CONSTRAINT fk_customer_engagement_line_item
        FOREIGN KEY (line_item_id) REFERENCES sf_opportunity_product(id) ON DELETE SET NULL;
