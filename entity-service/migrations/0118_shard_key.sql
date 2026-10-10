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

-- Add shard_key to csm_migration_job so the unique-pending index can
-- distinguish multiple concurrent shard jobs for the same table+type.
-- Non-sharded jobs keep the default '' and behave exactly as before.
ALTER TABLE csm_migration_job ADD COLUMN IF NOT EXISTS shard_key TEXT NOT NULL DEFAULT '';

DROP INDEX IF EXISTS idx_migration_job_unique_pending;
CREATE UNIQUE INDEX IF NOT EXISTS idx_migration_job_unique_pending
    ON csm_migration_job (table_name, job_type, shard_key)
    WHERE status IN ('pending', 'running');
