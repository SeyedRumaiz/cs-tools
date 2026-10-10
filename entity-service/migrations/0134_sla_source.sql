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

-- source discriminates who owns a "sla"/"sla_policy" row now that this
-- service can write its own SLA clocks (the CSM-native SLA engine) directly
-- into the same tables the ServiceNow sync already populates (migrations
-- 000051/000052). 'SERVICENOW' rows are exclusively owned by that sync
-- process and must never be mutated by anything else; 'CSM' rows are
-- exclusively owned by the CSM-native engine (internal/service/sn_case_service.go's
-- case-lifecycle hooks, and the background recompute worker). The two never
-- share a row. Every pre-existing row predates this column and is real
-- ServiceNow-synced data, hence the NOT NULL DEFAULT below rather than a
-- nullable column with application-level "treat NULL as servicenow" logic.
DO $$ BEGIN
    CREATE TYPE sla_source_enum AS ENUM ('SERVICENOW', 'CSM');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

ALTER TABLE sla_policy ADD COLUMN IF NOT EXISTS source sla_source_enum NOT NULL DEFAULT 'SERVICENOW';
ALTER TABLE sla ADD COLUMN IF NOT EXISTS source sla_source_enum NOT NULL DEFAULT 'SERVICENOW';

CREATE INDEX IF NOT EXISTS idx_sla_policy_source ON sla_policy (source);
CREATE INDEX IF NOT EXISTS idx_sla_source ON sla (source);
