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

-- A small, static reference table backing GET /sla-duration-policy --
-- csm-notification-service's own SLA tracking engine fetches it once at
-- startup to compute each case's due dates itself, rather than depending on
-- the ServiceNow-synced "sla"/"sla_policy" tables (which encode ServiceNow's
-- own condition-query language and have no plain severity column -- not
-- reusable for a simple severity->duration lookup, and empty for any case
-- that didn't come through that sync). This table is independent of both:
-- seeded directly here, by hand, from WSO2's own published Enterprise
-- Support Policy durations, never touched by any external sync.
--
-- severity reuses case_severity_enum (migration 0023) rather than a new
-- TEXT column, so a typo'd severity value is a migration-time error, not a
-- silently-unmatched runtime lookup. clock_type is a plain TEXT, not an
-- enum, since it is this table's own small, closed vocabulary
-- ('response'/'workaround'/'resolution') rather than a value shared with
-- any other table.
CREATE TABLE IF NOT EXISTS sla_duration_policy (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    severity   case_severity_enum NOT NULL,
    clock_type TEXT NOT NULL,
    duration   INTERVAL NOT NULL,
    UNIQUE (severity, clock_type)
);

-- case_severity_enum's own labels are 'S0'..'S4' (migration 0023), mapped by
-- this repo's own caseSeverityFromEnum (internal/repository/case_repo.go) as
-- S0=Catastrophic, S1=Critical, S2=High, S3=Medium, S4=Low -- the same
-- mapping every case.* event's own Priority field already goes through
-- (CaseCreatedPayload.Priority is strings.ToUpper(string(derefSeverity(...))),
-- e.g. "CATASTROPHIC"), so GET /sla-duration-policy's severity values line up
-- with a case.created payload's own Priority with no extra translation on
-- either side.
--
-- Durations are WSO2's published Enterprise Support Policy
-- (https://wso2.com/licenses/support-policy/6.0):
--   S0 (Catastrophic): 15m / 4h  / 48h
--   S1 (Critical):      1h / 24h / 48h
--   S2 (High):          4h / 48h / 72h
--   S3 (Medium):        6h / 72h / 7d   (resolution approximates "1 Business
--                                        Week" -- the weekend-roll this
--                                        implies is handled downstream in
--                                        csm-notification-service, which
--                                        alone knows real elapsed time)
--   S4 (Low):           24h / (none) / (none) -- "best efforts", response only
INSERT INTO sla_duration_policy (severity, clock_type, duration) VALUES
    ('S0', 'response',   '15 minutes'),
    ('S0', 'workaround', '4 hours'),
    ('S0', 'resolution', '48 hours'),
    ('S1', 'response',   '1 hour'),
    ('S1', 'workaround', '24 hours'),
    ('S1', 'resolution', '48 hours'),
    ('S2', 'response',   '4 hours'),
    ('S2', 'workaround', '48 hours'),
    ('S2', 'resolution', '72 hours'),
    ('S3', 'response',   '6 hours'),
    ('S3', 'workaround', '72 hours'),
    ('S3', 'resolution', '7 days'),
    ('S4', 'response',   '24 hours')
ON CONFLICT (severity, clock_type) DO NOTHING;
