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

-- Junction table fanned out from change_request.u_deployments via expand_list,
-- same shape as work_item_watcher: one row per (change_request, deployment) pair.
-- No DEFAULT on id: expand_list always supplies a deterministic id from Go,
-- matching the work_item_watcher pattern (see 0042_work_item_watcher_table.sql).
CREATE TABLE IF NOT EXISTS change_request_deployment (
    id                UUID PRIMARY KEY,
    change_request_id UUID NOT NULL REFERENCES change_request(id) ON DELETE CASCADE,
    deployment_id     UUID NOT NULL REFERENCES deployment(id) ON DELETE CASCADE,
    UNIQUE (change_request_id, deployment_id)
);

CREATE INDEX IF NOT EXISTS idx_change_request_deployment_change_request_id ON change_request_deployment (change_request_id);
CREATE INDEX IF NOT EXISTS idx_change_request_deployment_deployment_id ON change_request_deployment (deployment_id);
