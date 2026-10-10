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

-- u_work_state and resolution_code live on sn_customerservice_case, the one
-- table behind every case type, so service requests, engagements and security
-- report analyses carry them too, not just cases. Same enum types as "case"
-- (0023), since the values come from the same ServiceNow choice lists.
-- Same DDL as cs-tools entity-service 0184.

ALTER TABLE service_request
  ADD COLUMN IF NOT EXISTS work_state case_work_state_enum,
  ADD COLUMN IF NOT EXISTS resolution_code case_resolution_code_enum;

ALTER TABLE engagement
  ADD COLUMN IF NOT EXISTS work_state case_work_state_enum,
  ADD COLUMN IF NOT EXISTS resolution_code case_resolution_code_enum;

ALTER TABLE security_report_analysis
  ADD COLUMN IF NOT EXISTS work_state case_work_state_enum,
  ADD COLUMN IF NOT EXISTS resolution_code case_resolution_code_enum;
