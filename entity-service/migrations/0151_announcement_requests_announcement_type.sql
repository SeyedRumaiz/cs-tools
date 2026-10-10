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

-- Replaces announcement_requests' own bespoke is_security_announcement
-- boolean with the same announcement_type_enum (GENERAL/SECURITY) the
-- "announcement" table already uses (migration
-- 0088_announcement_add_announcement_type, which also defines the enum type
-- itself -- not redeclared here) -- one shared vocabulary for "is this a
-- security announcement" across the whole flow (pre-publish request and
-- published case) instead of two independently-typed columns doing the same
-- job.
--
-- Numbered after both of its prerequisites (0088 for the enum type, 0120
-- for announcement_requests itself, the table this migration alters) --
-- migrations run in sorted filename order, so this must sort after both or
-- a from-scratch schema bootstrap fails outright. Also numbered past 0150
-- deliberately: 0141-0150 are already claimed by the parallel
-- feature/rls-project-scoping work (PR #2094) as of this writing -- a
-- different concern (RLS policies), but sharing that number range would be
-- needlessly confusing even though this repo's own convention tolerates
-- duplicate migration numbers (tracked by full basename, not the number).

BEGIN;

ALTER TABLE announcement_requests
    ADD COLUMN IF NOT EXISTS announcement_type announcement_type_enum NOT NULL DEFAULT 'GENERAL';

-- Guarded on the old column still existing: re-running this file after
-- is_security_announcement has already been migrated and dropped must not
-- fail referencing a column that is gone by design, not by accident.
DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'announcement_requests' AND column_name = 'is_security_announcement'
    ) THEN
        UPDATE announcement_requests
        SET announcement_type = CASE
            WHEN is_security_announcement THEN 'SECURITY'::announcement_type_enum
            ELSE 'GENERAL'::announcement_type_enum
        END;
    END IF;
END $$;

ALTER TABLE announcement_requests DROP COLUMN IF EXISTS is_security_announcement;

COMMIT;
