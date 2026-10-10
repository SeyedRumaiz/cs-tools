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

-- Two more SME (Subject Matter Specialist) rotations: U2 and B-Central.
--
-- Data only: no table, column, type or constraint is created or changed. Each
-- team gets what migration 0200 gave the other seven SME rotations -- a rota,
-- a Day and a Night zone, and a Day and a Night escalation window -- plus its
-- team row, so every environment gets the team from this script rather than by
-- hand. A team is on a rota through its type (team_schedule_rota.team_type),
-- and any type starting with "sme" is the SME family, so nothing in the code
-- names these teams.
--
--   U2          Day 10:00-22:00, Night 22:00-10:00 (LK), weekly, 5 min
--   B-Central   Day 10:00-22:00, Night 22:00-10:00 (LK), weekly, 5 min
--
-- The windows are the ones the teams' rota sheet gives (as Moesif's in 0200).
--
-- Times are LK time (the windows' authoring time zone, Asia/Colombo) in
-- minutes from midnight; an end past 1440 runs into the next day. Every SME
-- rotation has L1, L2 and L3, held on the assignment (tier), and the weekend
-- is the same as a weekday.
--
-- Re-runnable: a team is added only when no team has its key, and every other
-- row only when its code is new. One transaction with a short lock timeout, as
-- the other schedule migrations.
BEGIN;
SET LOCAL lock_timeout = '5s';

-- ── The teams ─────────────────────────────────────────────────────────────
INSERT INTO team (id, created_on, updated_on, created_by, updated_by, name, key, type)
SELECT gen_random_uuid(), NOW(), NOW(), 'migration', 'migration', v.name, v.key, v.type
FROM (VALUES
    ('U2',        'u2',        'sme-u2'),
    ('B-Central', 'b-central', 'sme-b-central')
) AS v(name, key, type)
WHERE NOT EXISTS (SELECT 1 FROM team t WHERE lower(t.key) = v.key);

-- ── Their rotas ───────────────────────────────────────────────────────────
INSERT INTO team_schedule_rota
    (code, label, family, team_type, rotates, escalation_minutes, source_sheet, sort_order, created_by, updated_by)
VALUES
    ('SME_U2',        'U2',        'SME', 'sme-u2',        'WEEKLY', 5, NULL, 80, 'migration', 'migration'),
    ('SME_B_CENTRAL', 'B-Central', 'SME', 'sme-b-central', 'WEEKLY', 5, NULL, 90, 'migration', 'migration')
ON CONFLICT (code) DO NOTHING;

-- ── Their zones: a Day and a Night one each, each its own weekend zone ────
INSERT INTO team_schedule_zone (code, label, sort_order, rota_id, created_by, updated_by)
SELECT v.code, v.label, v.sort_order, r.id, 'migration', 'migration'
FROM (VALUES
    ('U2_D',  'U2 · Day',          101, 'SME_U2'),
    ('U2_N',  'U2 · Night',        102, 'SME_U2'),
    ('BCN_D', 'B-Central · Day',   111, 'SME_B_CENTRAL'),
    ('BCN_N', 'B-Central · Night', 112, 'SME_B_CENTRAL')
) AS v(code, label, sort_order, rota_code)
JOIN team_schedule_rota r ON r.code = v.rota_code
ON CONFLICT (code) DO NOTHING;

UPDATE team_schedule_zone SET weekend_zone_id = id, updated_on = NOW()
 WHERE code IN ('U2_D', 'U2_N', 'BCN_D', 'BCN_N')
   AND weekend_zone_id IS NULL;

-- ── Their windows: one escalation window per zone, every day ──────────────
-- As 0200's SME windows: tier left to the assignment, on the rotation, and
-- active at once (only IaaS waited for the rota-aware portal).
INSERT INTO team_schedule_shift
    (code, label, family, zone_id, tier, day_scope, start_minute, end_minute,
     is_on_call, is_escalation, is_rotation, required_headcount,
     short_code, colour_token, sort_order, is_active, created_by, updated_by)
SELECT v.code, v.label, 'SME'::team_schedule_shift_family_enum, z.id,
       NULL, 'ANY'::team_schedule_day_scope_enum, v.start_minute, v.end_minute,
       FALSE, TRUE, TRUE, NULL,
       v.short_code, v.colour_token, v.sort_order, TRUE,
       'migration', 'migration'
FROM (VALUES
    ('SME_U2_DAY',    'U2 day escalation',          'U2_D',   600, 1320, 'Day',   'TZ1', 1010),
    ('SME_U2_NIGHT',  'U2 night escalation',        'U2_N',  1320, 2040, 'Night', 'TZ3', 1020),
    ('SME_BCN_DAY',   'B-Central day escalation',   'BCN_D',  600, 1320, 'Day',   'TZ1', 1110),
    ('SME_BCN_NIGHT', 'B-Central night escalation', 'BCN_N', 1320, 2040, 'Night', 'TZ3', 1120)
) AS v(code, label, zone_code, start_minute, end_minute, short_code, colour_token, sort_order)
JOIN team_schedule_zone z ON z.code = v.zone_code
ON CONFLICT (code) DO NOTHING;

COMMIT;
