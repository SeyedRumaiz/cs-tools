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

BEGIN;

-- case_attachment (migration 0106) was built assuming every row's bytes live
-- in SFTPGo, addressed by storage_key -- true for the CSM-native (Postgres)
-- data source, but DATA_SOURCE=postgres-servicenow-dual-write now also writes
-- metadata-only rows here for ServiceNow-sourced attachments (see
-- CaseRepository.CreateCaseAttachmentFromServiceNow): under dual-write, file
-- bytes stay in ServiceNow only (SFTPGo is never used in that mode), so those
-- rows have no storage_key at all.
--
-- The real invariant this column used to enforce ("every row can be traced
-- back to where its bytes live") still holds without a NOT NULL constraint:
-- a ServiceNow-sourced row's own id IS sysidToUUID(the real ServiceNow
-- attachment sys_id) -- the same identity convention case/deployment/
-- deployed_product/incident/problem/change_request already use for every
-- other ServiceNow-first Postgres row -- so uuidToSysid(id) alone, with no
-- extra column, losslessly recovers the sys_id a ServiceNow API call needs.
-- A NULL storage_key is therefore unambiguous ("this row's bytes are not in
-- SFTPGo, resolve them via ServiceNow using this row's own id") given that
-- the data source is a deployment-wide setting, not a per-row choice -- there
-- is no row for which "NULL" could mean anything else.
--
-- No replacement CHECK constraint is added: the only other candidate
-- invariant ("storage_key is NULL only for a row this process itself created
-- via the ServiceNow path") can't be verified from the row alone -- it's a
-- statement about how the row came to exist, not about its current data --
-- so a CHECK constraint here would either be vacuous or require tracking
-- provenance this table was never meant to carry. Simpler, and equally
-- correct given the deployment-wide data source invariant above, to just
-- drop NOT NULL.
--
-- DROP COLUMN ... DROP NOT NULL is idempotent in Postgres: re-running this
-- against an already-nullable column is a no-op, not an error, so no
-- DO $$ ... $$ guard is needed (unlike an ADD CONSTRAINT, which does need
-- one for a repeat run).
ALTER TABLE case_attachment ALTER COLUMN storage_key DROP NOT NULL;

COMMIT;
