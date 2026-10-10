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

-- Two roles that let their holder edit any rota in their own family.
--
-- Until now the whole of the edit permission was "a lead of this team", which
-- means a rota cannot be fixed while its lead is away -- not by a manager, not
-- by the head of CRE, not by anyone. That was a deliberate decision (see
-- requireTeamLead's own comment) and it is being revised, not patched around:
-- the blast radius of one team silently rewriting another's cover is still
-- real, so this is a named, granted, auditable capability rather than a
-- widening of what a lead already holds.
--
-- One role per family, because the two rotas are run by different people and
-- neither group has any business in the other's cover. Someone who genuinely
-- runs both is granted both.
--
-- ON CONFLICT (name), not (id): the ServiceNow sync seeds this table too, so
-- a deployment may already have a row under a different id. Matching the name
-- is what makes re-running safe -- the same reasoning migration 0128 used to
-- seed the ADMIN project role.
-- One transaction, so a failure part-way leaves nothing behind, and a short
-- lock timeout, so a table another writer holds makes this fail fast instead
-- of queuing every later writer behind it. Re-running after either is safe.
BEGIN;
SET LOCAL lock_timeout = '5s';

INSERT INTO role (id, created_on, updated_on, created_by, updated_by, name, description)
VALUES
    (gen_random_uuid(), now(), now(), 'migration', 'migration',
     'cre_rota_admin', 'May edit the rota of any CRE team, not only their own'),
    (gen_random_uuid(), now(), now(), 'migration', 'migration',
     'sre_rota_admin', 'May edit the rota of any SRE team, not only their own')
ON CONFLICT (name) DO NOTHING;

-- Granting these to actual people is a deployment step, not a migration: who
-- runs the CRE and SRE rotas differs per environment and changes over time.
-- There is no role-assignment UI yet (see the CSM portal webapp's own note on
-- AddUserDialog), so until there is, a grant is an INSERT into user_role.
--
-- Grant it only to internal staff. Holding a rota admin role never makes
-- anyone internal: recompute_user_type() is untouched, and the schedule both
-- refuses a caller who is not INTERNAL and, for a rota admin, asks again that
-- the holder is INTERNAL before counting the role. So a grant to somebody who
-- is not already internal staff (holding the admin or internal role) does
-- nothing at all, rather than widening what they can reach elsewhere.

COMMIT;
