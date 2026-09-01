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

-- Switches chat_routing.engineers' primary key from email to engineer_id --
-- the IdP's stable per-account "userid" claim (see csm-portal/backend's
-- internal/middleware.UserInfo.UserID, which decodes that same claim on
-- every authenticated request), not a locally-generated ID. email stays a
-- required, unique column: every route this service exposes still
-- identifies an engineer by email (GET /route/presence/{email}, the
-- escalate/completed/decline bodies) -- only POST /route/presence, the one
-- place a new row is created, also carries engineerId, since this service
-- has no way to mint that identifier itself.
--
-- This is a pre-launch service whose presence rows are ephemeral working
-- state (an engineer just needs to set their status again after this
-- runs), so rather than a synthetic backfill for rows that predate
-- engineer_id, existing rows are simply cleared. CASCADE also clears
-- customer_engineer_assignments, whose FK points at email -- those sticky
-- assignments would otherwise dangle once the engineers they point to are
-- gone.
TRUNCATE chat_routing.engineers CASCADE;

-- customer_engineer_assignments' FK depends on the PK's backing index
-- specifically, not "any unique constraint on email" -- drop it before the
-- PK swap and re-attach it to the new UNIQUE(email) constraint below,
-- rather than CASCADE-dropping it silently along with the old PK. (Found
-- by actually running this migration -- CASCADE here would have silently
-- dropped a real FK constraint instead of just clearing rows.)
ALTER TABLE chat_routing.customer_engineer_assignments
  DROP CONSTRAINT customer_engineer_assignments_engineer_email_fkey;

ALTER TABLE chat_routing.engineers
  DROP CONSTRAINT engineers_pkey,
  ADD COLUMN engineer_id TEXT NOT NULL,
  ADD CONSTRAINT engineers_pkey PRIMARY KEY (engineer_id),
  ADD CONSTRAINT engineers_email_key UNIQUE (email);

ALTER TABLE chat_routing.customer_engineer_assignments
  ADD CONSTRAINT customer_engineer_assignments_engineer_email_fkey
  FOREIGN KEY (engineer_email) REFERENCES chat_routing.engineers (email);
