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

-- Case Paging: a paging-only phone number per person.
--
-- Paging calls the number on a person's Asgardeo profile. Where the profile
-- has none, a lead or a rota admin can store one here, from the Team
-- Schedule's Case Paging tab. It is used by paging and nothing else, and is
-- never written back to Asgardeo. last_test_* record the last "Test call":
-- pending when it is requested, then the outcome csm-notification-service
-- reports.
--
-- id is user_id under another name. The audit trigger (migration 0155)
-- addresses every row by a column called id; generated from user_id, it
-- keeps one person's history under one row id across a number being
-- removed and set again. The trigger reads the actor from app.actor, which
-- every write of this table sets (the row has no updated_by to fall back on).
--
-- Re-runnable: the table and its constraints are created only if missing,
-- and the trigger is replaced. One transaction with a short lock timeout, as
-- 0155 and 0214.

BEGIN;
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS paging_contact (
    user_id          UUID PRIMARY KEY REFERENCES "user"(id) ON DELETE CASCADE,
    id               UUID GENERATED ALWAYS AS (user_id) STORED,
    phone            VARCHAR(16) NOT NULL,
    set_by           VARCHAR(255) NOT NULL,
    set_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_test_at     TIMESTAMPTZ NULL,
    last_test_status VARCHAR(20) NULL,
    CONSTRAINT paging_contact_phone_e164_check
        CHECK (phone ~ '^\+[1-9][0-9]{6,14}$'),
    CONSTRAINT paging_contact_last_test_status_check
        CHECK (last_test_status IS NULL
               OR last_test_status IN ('pending', 'completed', 'no-answer', 'busy', 'failed'))
);

DROP TRIGGER IF EXISTS paging_contact_audit ON paging_contact;
CREATE TRIGGER paging_contact_audit
    AFTER INSERT OR UPDATE OR DELETE ON paging_contact
    FOR EACH ROW EXECUTE FUNCTION team_schedule_audit_row();

COMMIT;
