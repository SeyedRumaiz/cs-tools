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

-- problem.business_service, impact and urgency, for "[WSO2 Cloud Ops] Post
-- resolution tasks": the problem it creates for an incident resolved with a
-- workaround copies all three from the incident, and Postgres had nowhere to
-- put them. Same choice list as incident (1 - High, 2 - Medium, 3 - Low), but
-- an enum per table, as incident and incident_task already have.
--
-- All nullable with no default: existing problems keep NULL, as ServiceNow
-- leaves a problem nobody filled in.

DO $$ BEGIN
    CREATE TYPE problem_impact_enum AS ENUM ('HIGH', 'MEDIUM', 'LOW');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE problem_urgency_enum AS ENUM ('HIGH', 'MEDIUM', 'LOW');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

ALTER TABLE problem ADD COLUMN IF NOT EXISTS service_id UUID REFERENCES service(id) ON DELETE SET NULL;
ALTER TABLE problem ADD COLUMN IF NOT EXISTS impact problem_impact_enum;
ALTER TABLE problem ADD COLUMN IF NOT EXISTS urgency problem_urgency_enum;

CREATE INDEX IF NOT EXISTS idx_problem_service_id ON problem (service_id);
