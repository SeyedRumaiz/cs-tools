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

-- Add job_type to csm_migration_row_error so errors can be filtered and
-- cleared by the job type that produced them, without joining csm_migration_job.
-- Existing rows get an empty string default (historical data, type unknown).
ALTER TABLE csm_migration_row_error
    ADD COLUMN IF NOT EXISTS job_type TEXT NOT NULL DEFAULT '';
