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

-- Record-triggered incident report flows: notice incident state changes.
--
-- Ports two ServiceNow flows, both "Incident Updated where State changes to X":
--   - Create Incident Report Task  (-> In Progress) creates an incident_task
--   - Incident Report Generator    (-> Resolved)    writes incident.incident_report
-- "Changes to" needs the old row and the new one, which only an AFTER UPDATE
-- trigger sees for every writer. 0051's trg_event_outbox is table-agnostic
-- (TG_TABLE_NAME / NEW.id) and already skips updates that change nothing, so
-- adding a producer is one CREATE TRIGGER -- the same step 000087 took for
-- outage. IncidentReportDrainer reads the rows back.
--
-- UPDATE only. Both flows trigger on "Updated", never on "Created", so an
-- incident inserted already In Progress creates no task -- same as
-- ServiceNow.
--
-- Wrapped in a transaction for the same reason 0051 gives: `make migrate`
-- runs each file with `psql -f`, and a drop-then-create outside one would
-- leave a window with no trigger on the table at all.
BEGIN;

-- Retry bookkeeping for consumers that must not lose a change.
--
-- 0051's consumers mark a row published at claim time, before acting on it:
-- a crash in between loses that notice, which is the right trade for an
-- email. A lost incident report task is the worse failure, so the incident
-- drainer marks a row published only in the same transaction as the write
-- it caused, and a failure rolls both back. That needs somewhere to count
-- failures and when the last one happened, so retries back off and a row
-- that always fails is eventually parked instead of retried forever. All
-- three default or are nullable, so 0051's consumers and their queries are
-- untouched.
ALTER TABLE event_outbox ADD COLUMN IF NOT EXISTS attempts        INTEGER NOT NULL DEFAULT 0;
ALTER TABLE event_outbox ADD COLUMN IF NOT EXISTS last_error      TEXT;
ALTER TABLE event_outbox ADD COLUMN IF NOT EXISTS last_attempt_on TIMESTAMPTZ;

DROP TRIGGER IF EXISTS incident_outbox ON incident;
CREATE TRIGGER incident_outbox
    AFTER UPDATE ON incident
    FOR EACH ROW EXECUTE FUNCTION trg_event_outbox();

COMMIT;
