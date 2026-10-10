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

-- Seeds the 3 P0/Managed-Services policy rows that are missing from the
-- real, live ServiceNow-synced sla_policy data (confirmed against
-- production: P1-P3 and Query each have Response/Workaround/Resolution
-- rows per plan, but P0 - the most severe tier - has none defined in
-- ServiceNow at all). Named to match the real sync's own naming convention
-- exactly (e.g. "P1 - Response (Managed Services)") so
-- internal/service/sla_policy_resolver.go's name-based lookup finds these
-- exactly like a real synced row, with source='CSM' to mark them as
-- CSM-authored rather than SN-synced (see migration 000088).
--
-- Durations mirror the old, now-deleted sla_clocks hardcoded map's own
-- CATASTROPHIC (P0) entries (15m/4h/48h) -- see git history for
-- entity-service/internal/service/sla_policy.go before commit
-- 116d43522 -- which approximated WSO2's published support policy
-- (https://wso2.com/licenses/support-policy/6.0). No "(Open Source)" P0
-- rows are seeded: P0 is WSO2's most severe production-down tier, sold
-- only under Managed Services / paid support contracts.
INSERT INTO sla_policy (id, created_on, updated_on, created_by, updated_by, name, is_active, target, duration, source)
SELECT gen_random_uuid(), NOW(), NOW(), 'migration-000089', 'migration-000089',
       'P0 - Response (Managed Services)', TRUE, 'RESPONSE'::sla_policy_target_enum, INTERVAL '15 minutes', 'CSM'::sla_source_enum
WHERE NOT EXISTS (SELECT 1 FROM sla_policy WHERE name = 'P0 - Response (Managed Services)');

INSERT INTO sla_policy (id, created_on, updated_on, created_by, updated_by, name, is_active, target, duration, source)
SELECT gen_random_uuid(), NOW(), NOW(), 'migration-000089', 'migration-000089',
       'P0 - Workaround (Managed Services)', TRUE, 'WORKAROUND'::sla_policy_target_enum, INTERVAL '4 hours', 'CSM'::sla_source_enum
WHERE NOT EXISTS (SELECT 1 FROM sla_policy WHERE name = 'P0 - Workaround (Managed Services)');

INSERT INTO sla_policy (id, created_on, updated_on, created_by, updated_by, name, is_active, target, duration, source)
SELECT gen_random_uuid(), NOW(), NOW(), 'migration-000089', 'migration-000089',
       'P0 - Resolution (Managed Services)', TRUE, 'RESOLUTION'::sla_policy_target_enum, INTERVAL '48 hours', 'CSM'::sla_source_enum
WHERE NOT EXISTS (SELECT 1 FROM sla_policy WHERE name = 'P0 - Resolution (Managed Services)');
