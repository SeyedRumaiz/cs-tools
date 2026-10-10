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

-- Case Paging: a history of who is on the paging chain.
--
-- The Team Schedule's Case Paging tab now changes team_member rows -- who is a
-- team's 1st-3rd responder, its Team leads, the America lead, the CRE and CS
-- heads. Until now only the sync, the roster import and the seed scripts wrote it,
-- so "who changed this team's 2nd responder, and when" had no answer. The
-- same audit trigger the rota tables use (migration 0155) records every
-- insert, update and delete, with the actor the application names for the
-- transaction.
--
-- Re-runnable: the trigger is replaced, and the baseline only writes rows
-- that have no history yet. One transaction with a short lock timeout, as
-- 0155: team_member is also written by the ServiceNow sync, and creating the
-- trigger must not queue those writes behind it.

BEGIN;
SET LOCAL lock_timeout = '5s';

DROP TRIGGER IF EXISTS team_member_audit ON team_member;
CREATE TRIGGER team_member_audit
    AFTER INSERT OR UPDATE OR DELETE ON team_member
    FOR EACH ROW EXECUTE FUNCTION team_schedule_audit_row();

-- What is already there, so "no history" is never ambiguous between "never
-- touched" and "predates the audit" (the same reasoning as 0155's baseline).
INSERT INTO team_schedule_audit (changed_at, table_name, row_id, action, actor, new_row)
SELECT now(), 'team_member', m.id, 'BASELINE', m.created_by, to_jsonb(m)
  FROM team_member m
 WHERE NOT EXISTS (SELECT 1 FROM team_schedule_audit a
                    WHERE a.table_name = 'team_member' AND a.row_id = m.id);

COMMIT;
