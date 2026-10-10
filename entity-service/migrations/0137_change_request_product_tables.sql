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

-- Two junction tables fanned out from change_request.u_deployment_products via
-- expand_list. Each sys_id in the list resolves to exactly one table: if it
-- exists in product it lands in change_request_product; if it exists in
-- product_version it lands in change_request_product_version. A UUID present
-- in neither produces a row error in both mappings (genuine missing data).
CREATE TABLE IF NOT EXISTS change_request_product (
    id                UUID PRIMARY KEY,
    change_request_id UUID NOT NULL REFERENCES change_request(id) ON DELETE CASCADE,
    product_id        UUID NOT NULL REFERENCES product(id) ON DELETE CASCADE,
    UNIQUE (change_request_id, product_id)
);

CREATE INDEX IF NOT EXISTS idx_change_request_product_change_request_id ON change_request_product (change_request_id);
CREATE INDEX IF NOT EXISTS idx_change_request_product_product_id ON change_request_product (product_id);

CREATE TABLE IF NOT EXISTS change_request_product_version (
    id                UUID PRIMARY KEY,
    change_request_id UUID NOT NULL REFERENCES change_request(id) ON DELETE CASCADE,
    product_version_id UUID NOT NULL REFERENCES product_version(id) ON DELETE CASCADE,
    UNIQUE (change_request_id, product_version_id)
);

CREATE INDEX IF NOT EXISTS idx_change_request_product_version_change_request_id ON change_request_product_version (change_request_id);
CREATE INDEX IF NOT EXISTS idx_change_request_product_version_product_version_id ON change_request_product_version (product_version_id);
