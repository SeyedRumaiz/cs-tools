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

-- The change request approval flow resolves two approver pools by group name:
--
--   "CAB Approval"  -- the Change Advisory Board: the second approval stage of
--                      a Normal change (right after peer approval); approving
--                      it moves the change to Scheduled.
--   "ECAB Approval" -- the Emergency CAB: the only approval stage of an
--                      Emergency change (no peer approval). A group of its own,
--                      deliberately NOT shared with CAB Approval.
--
-- Both must exist. Created here, idempotently, only when no group of that name
-- is already present: in a synced environment ServiceNow may already have
-- mirrored a group with the same name (with real members), and a second,
-- empty one must not be added next to it. Fixed ids make the rows addressable
-- from tests and seed data; ON CONFLICT (id) keeps a re-run a no-op even if
-- the name was later changed.
--
-- Membership is NOT seeded: who sits on the CAB / ECAB is an operational
-- decision, populated by the ServiceNow sync (team_member.group_id) or by an
-- operator. A change request cannot be sent for approval until the relevant
-- group has at least one eligible (non-creator) member -- the request is
-- rejected with a clear message rather than stranded without approvers.
INSERT INTO "group" (id, created_on, updated_on, created_by, updated_by, name, description, is_active)
SELECT '00000000-0000-4000-8000-00000000ca01'::uuid, now(), now(), 'migration', 'migration',
       'CAB Approval', 'Change Advisory Board: second approval stage of a Normal change request.', true
WHERE NOT EXISTS (SELECT 1 FROM "group" WHERE name = 'CAB Approval')
ON CONFLICT (id) DO NOTHING;

INSERT INTO "group" (id, created_on, updated_on, created_by, updated_by, name, description, is_active)
SELECT '00000000-0000-4000-8000-00000000eca1'::uuid, now(), now(), 'migration', 'migration',
       'ECAB Approval', 'Emergency Change Advisory Board: the only approval stage of an Emergency change request.', true
WHERE NOT EXISTS (SELECT 1 FROM "group" WHERE name = 'ECAB Approval')
ON CONFLICT (id) DO NOTHING;
