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

-- The America Team lead: one position above the Americas team's three Team
-- leads.
--
-- At night the CRE escalation ladder calls the three Team leads at Level 1 and
-- the America Team lead at Level 2. All four used to hold role 'lead', so
-- nothing in the data could tell the fourth apart, and the ladder had to be
-- told who it was by email in a public config file -- or guess. A position of
-- its own is the only way to tell them apart.
--
-- Like the heads, the America Team lead is someone a ladder escalates TO, so
-- the rule that a lead never also holds a first-responder tier covers this
-- role too.
--
-- Re-runnable: each constraint is dropped by name before it is added, and the
-- index is created only if absent.

ALTER TABLE team_member DROP CONSTRAINT IF EXISTS team_member_role_check;
ALTER TABLE team_member ADD CONSTRAINT team_member_role_check
    CHECK (role IN ('engineer', 'sub_lead', 'lead', 'americas_team_lead', 'cre_head', 'cs_head'));

ALTER TABLE team_member DROP CONSTRAINT IF EXISTS team_member_alert_tier_not_lead;
ALTER TABLE team_member ADD CONSTRAINT team_member_alert_tier_not_lead
    CHECK (alert_tier IS NULL OR role NOT IN ('lead', 'americas_team_lead', 'cre_head', 'cs_head'));

-- One America Team lead per team: Level 2 at night is one person.
CREATE UNIQUE INDEX IF NOT EXISTS team_member_one_americas_team_lead
    ON team_member (team_id) WHERE role = 'americas_team_lead';
