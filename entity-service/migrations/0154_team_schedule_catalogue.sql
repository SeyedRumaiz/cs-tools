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

-- The windows CRE and SRE actually work, and the kinds of time away from the
-- rota. This is reference data, not sample data: the day view, the week view
-- and the roster all read their shape from here, so an empty catalogue renders
-- an empty page. Seeded as a migration so every environment starts from the
-- same catalogue, and a window that changes is a reviewable diff.
--
-- Minutes count from midnight IST (authoring_time_zone Asia/Colombo);
-- anything past 1440 runs into the next day.
--
-- ON CONFLICT DO NOTHING throughout: re-running this never overwrites a row a
-- lead has since edited.

-- ── SRE time zones ────────────────────────────────────────────────────────
-- One transaction, so a failure part-way leaves nothing behind, and a short
-- lock timeout, so a table another writer holds makes this fail fast instead
-- of queuing every later writer behind it. Re-running after either is safe.
BEGIN;
SET LOCAL lock_timeout = '5s';

INSERT INTO team_schedule_zone (code, label, sort_order, created_by, updated_by)
VALUES
    ('TZ1', 'Time zone 1', 1, 'migration', 'migration'),
    ('TZ2', 'Time zone 2', 2, 'migration', 'migration'),
    ('TZ3', 'Time zone 3', 3, 'migration', 'migration')
ON CONFLICT (code) DO NOTHING;

-- At the weekend TZ1 and TZ2 combine into one crew, 06:00-21:00, carried
-- under TZ1; TZ3 stays as it is, 21:00-06:00. Only fills a zone that has no
-- mapping yet (an older database is corrected at the end of this file).
UPDATE team_schedule_zone z SET weekend_zone_id = w.id, updated_on = NOW()
  FROM team_schedule_zone w
 WHERE w.code = CASE z.code WHEN 'TZ1' THEN 'TZ1' WHEN 'TZ2' THEN 'TZ1' WHEN 'TZ3' THEN 'TZ3' END
   AND z.weekend_zone_id IS NULL;

-- ── CRE windows ───────────────────────────────────────────────────────────
-- is_rotation FALSE marks when a team simply works (regular hours, and the
-- Americas team's standing night) as opposed to a turn somebody takes.
-- required_headcount NULL means no target has been set, not zero.
INSERT INTO team_schedule_shift
    (code, label, family, zone_id, tier, day_scope, start_minute, end_minute,
     is_on_call, is_escalation, is_rotation, required_headcount,
     short_code, colour_token, sort_order, created_by, updated_by)
VALUES
    ('CRE_MORNING',          'Morning 6-9am',            'CRE', NULL, NULL, 'WEEKDAY',  360,  540, FALSE, FALSE, TRUE,  1,    '6-9am',     'AM',    10, 'migration', 'migration'),
    ('CRE_MORNING_OC',       'Morning 6-9am on-call',    'CRE', NULL, NULL, 'WEEKDAY',  360,  540, TRUE,  FALSE, TRUE,  1,    '6-9am',     'AM',    20, 'migration', 'migration'),
    ('CRE_REGULAR',          'Regular hours',            'CRE', NULL, NULL, 'WEEKDAY',  540, 1080, FALSE, FALSE, FALSE, NULL, 'LK',        'LK',    30, 'migration', 'migration'),
    ('CRE_REGULAR_IND',      'India region shift',       'CRE', NULL, NULL, 'WEEKDAY',  540, 1080, FALSE, FALSE, FALSE, NULL, 'IND',       'IND',   35, 'migration', 'migration'),
    ('CRE_EVENING',          'Evening 6-9pm',            'CRE', NULL, NULL, 'WEEKDAY', 1080, 1260, FALSE, FALSE, TRUE,  7,    '6-9pm',     'PM',    40, 'migration', 'migration'),
    ('CRE_AMERICAS',         'Americas cover',           'CRE', NULL, NULL, 'ANY',     1260, 1800, FALSE, FALSE, FALSE, NULL, 'NLK',       'NLK',   50, 'migration', 'migration'),
    ('CRE_WEEKEND',          'Weekend rotation',         'CRE', NULL, NULL, 'WEEKEND',  360, 1260, FALSE, FALSE, TRUE,  3,    'WE',        'WE',    60, 'migration', 'migration'),
    ('CRE_WEEKEND_NIGHT',    'Americas weekend',         'CRE', NULL, NULL, 'WEEKEND', 1260, 1800, FALSE, FALSE, TRUE,  NULL, 'NLK-WE',    'NLKWE', 70, 'migration', 'migration'),
    ('CRE_WEEKEND_NIGHT_OC', 'Americas weekend on-call', 'CRE', NULL, NULL, 'WEEKEND', 1260, 1800, TRUE,  FALSE, TRUE,  NULL, 'NLK-WE-OC', 'OC',    80, 'migration', 'migration')
ON CONFLICT (code) DO NOTHING;

-- ── SRE windows ───────────────────────────────────────────────────────────
-- The SRE day, as the SRE leads run it:
--
--             L1 and L2        regular hours
--   TZ1       06:00-13:30      06:00-15:00
--   TZ2       13:30-21:00      12:00-21:00
--   TZ3       21:00-06:00      21:00-06:00
--
-- At the weekend TZ1 and TZ2 are one crew, 06:00-21:00 (SRE_WE_TZ1), and TZ3
-- is unchanged: its escalation window runs every day, weekends included.
-- There are no regular hours at the weekend.
--
-- Each zone has an escalation window (SRE_TZn, tier left open: L1, L2 or L3
-- is a fact about the person that week), an L1 window for TZ1/TZ2, and its
-- own regular hours, drawn SUP (support in normal hours). TZ3's regular
-- window matches its escalation one on purpose: the window is not what
-- separates them, the escalation rota is.
INSERT INTO team_schedule_shift
    (code, label, family, zone_id, tier, day_scope, start_minute, end_minute,
     is_on_call, is_escalation, is_rotation, required_headcount,
     short_code, colour_token, sort_order, created_by, updated_by)
SELECT v.code, v.label, 'SRE'::team_schedule_shift_family_enum, z.id,
       v.tier::team_schedule_tier_enum, v.day_scope::team_schedule_day_scope_enum,
       v.start_minute, v.end_minute, FALSE, v.is_escalation, v.is_rotation, NULL,
       v.short_code, v.colour_token, v.sort_order, 'migration', 'migration'
FROM (VALUES
    ('SRE_TZ1_REGULAR', 'TZ1 regular hours', 'TZ1', NULL, 'WEEKDAY',  360,  900, FALSE, FALSE, 'SUP', 'LK',  101),
    ('SRE_TZ2_REGULAR', 'TZ2 regular hours', 'TZ2', NULL, 'WEEKDAY',  720, 1260, FALSE, FALSE, 'SUP', 'LK',  102),
    ('SRE_TZ3_REGULAR', 'TZ3 regular hours', 'TZ3', NULL, 'WEEKDAY', 1260, 1800, FALSE, FALSE, 'SUP', 'LK',  103),
    ('SRE_TZ1_L1',      'TZ1 L1 support',    'TZ1', 'L1', 'WEEKDAY',  360,  810, TRUE,  TRUE,  'L1',  'L1',  110),
    ('SRE_TZ1',         'TZ1 escalation',    'TZ1', NULL, 'WEEKDAY',  360,  810, TRUE,  TRUE,  'TZ1', 'TZ1', 120),
    ('SRE_TZ2_L1',      'TZ2 L1 support',    'TZ2', 'L1', 'WEEKDAY',  810, 1260, TRUE,  TRUE,  'L1',  'L1',  130),
    ('SRE_TZ2',         'TZ2 escalation',    'TZ2', NULL, 'WEEKDAY',  810, 1260, TRUE,  TRUE,  'TZ2', 'TZ2', 140),
    ('SRE_TZ3',         'TZ3 escalation',    'TZ3', NULL, 'ANY',     1260, 1800, TRUE,  TRUE,  'TZ3', 'TZ3', 150),
    ('SRE_WE_TZ1',      'Weekend TZ1 + TZ2', 'TZ1', NULL, 'WEEKEND',  360, 1260, TRUE,  TRUE,  'TZ1+2', 'TZ1', 160)
) AS v(code, label, zone_code, tier, day_scope, start_minute, end_minute,
       is_escalation, is_rotation, short_code, colour_token, sort_order)
JOIN team_schedule_zone z ON z.code = v.zone_code
ON CONFLICT (code) DO NOTHING;

-- ── Leave and allocation kinds ────────────────────────────────────────────
-- Allocations are a few kinds plus allocated_to (who the time is for) --
-- a new customer is a value, not a new tag. The Brazil rotation keeps a kind
-- of its own: it is neither customer nor product work.
--
-- family is the rota a kind is offered on, NULL for both. The two rotas
-- allocate time to different things: CRE to internal and external work, the
-- Brazil rotation and Migration; SRE to internal and external work, the
-- Brazil rotation and RnD. Leave is the same for everyone.
--
-- Rows marked retired (is_active FALSE) are kinds an earlier version of the
-- rota used, kept so absences recorded against them stay readable. They
-- never appear in a picker or legend.
INSERT INTO team_schedule_absence_kind
    (code, short_code, label, bucket, colour_token, sort_order, is_active, family, created_by, updated_by)
VALUES
    ('ANNUAL_LEAVE',     'AL',       'Annual leave',                      'LEAVE',      'AL',   10, TRUE,  NULL,  'migration', 'migration'),
    ('LIEU_LEAVE',       'LL',       'Lieu leave',                        'LEAVE',      'LL',   20, TRUE,  NULL,  'migration', 'migration'),
    ('MATERNITY_LEAVE',  'ML',       'Maternity leave',                   'LEAVE',      'MAT',  21, TRUE,  NULL,  'migration', 'migration'),
    ('PATERNITY_LEAVE',  'PL',       'Paternity leave',                   'LEAVE',      'PAT',  22, TRUE,  NULL,  'migration', 'migration'),
    ('SICK_LEAVE',       'SL',       'Sick leave',                        'LEAVE',      'AL',   25, TRUE,  NULL,  'migration', 'migration'),
    ('ALLO_INT',         'Allo-INT', 'Allocated — internal',              'ALLOCATION', 'INT',  30, TRUE,  NULL,  'migration', 'migration'),
    ('ALLO_EXT',         'Allo-EXT', 'Allocated — external',              'ALLOCATION', 'EXT',  40, TRUE,  NULL,  'migration', 'migration'),
    ('RND',              'RnD',      'RnD',                               'ALLOCATION', 'RND',  50, TRUE,  'SRE', 'migration', 'migration'),
    ('ALLO_BR',          'BR',       'Brazil rotation',                   'ALLOCATION', 'BR',   70, TRUE,  NULL,  'migration', 'migration'),
    ('MIGRATION',        'Mig',      'Migration',                         'ALLOCATION', 'MIG',  90, TRUE,  'CRE', 'migration', 'migration'),
    ('EXCLUDED',         'EXC',      'Excluded from rota',                'EXCLUDED',   'EXC', 110, TRUE,  NULL,  'migration', 'migration'),
    ('CUSTOMER_ONSITE',  'CUS-ON',   'Customer allocation — on site',     'ALLOCATION', 'EXT',  41, FALSE, NULL,  'migration', 'migration'),
    ('CUSTOMER_OFFSITE', 'CUS-OFF',  'Customer allocation — off site',    'ALLOCATION', 'EXT',  42, FALSE, NULL,  'migration', 'migration'),
    ('CUSTOMER',         'CUS',      'Customer allocation — unspecified', 'ALLOCATION', 'EXT',  43, FALSE, NULL,  'migration', 'migration'),
    ('ONBOARDING',       'ONB',      'Onboarding',                        'ALLOCATION', 'ONB', 100, FALSE, NULL,  'migration', 'migration'),
    ('ALLO_CRIS',        'CRIS',     'Allocated — CRIS',                  'ALLOCATION', 'BR',   80, FALSE, NULL,  'migration', 'migration')
ON CONFLICT (code) DO NOTHING;

-- ── An older database's kinds ─────────────────────────────────────────────
-- The chain this file replaces offered customer allocations (on and off
-- site) and Onboarding to both rotas, and kept Allo-INT / Allo-EXT retired.
-- Bring such a database to the list above. Only the catalogue's own rows are
-- touched: a tag a lead added is theirs, and stays offered on both rotas.
UPDATE team_schedule_absence_kind k
   SET short_code = v.short_code, sort_order = v.sort_order, is_active = v.is_active,
       family = v.family::team_schedule_shift_family_enum,
       updated_on = NOW(), updated_by = 'migration'
  FROM (VALUES
    ('ALLO_INT',         'Allo-INT', 30, TRUE,  NULL),
    ('ALLO_EXT',         'Allo-EXT', 40, TRUE,  NULL),
    ('RND',              'RnD',      50, TRUE,  'SRE'),
    ('MIGRATION',        'Mig',      90, TRUE,  'CRE'),
    ('CUSTOMER_ONSITE',  'CUS-ON',   41, FALSE, NULL),
    ('CUSTOMER_OFFSITE', 'CUS-OFF',  42, FALSE, NULL),
    ('CUSTOMER',         'CUS',      43, FALSE, NULL),
    ('ONBOARDING',       'ONB',     100, FALSE, NULL)
  ) AS v(code, short_code, sort_order, is_active, family)
 WHERE k.code = v.code AND k.created_by = 'migration'
   AND (k.short_code, k.sort_order, k.is_active, k.family::text)
       IS DISTINCT FROM (v.short_code, v.sort_order::smallint, v.is_active, v.family);

-- A customer allocation is external work, so what was marked as one is
-- Allo-EXT now, still naming the customer it was for.
UPDATE team_schedule_absence a
   SET kind_id = ext.id, updated_on = NOW(), updated_by = 'migration'
  FROM team_schedule_absence_kind old, team_schedule_absence_kind ext
 WHERE a.kind_id = old.id
   AND old.code IN ('CUSTOMER_ONSITE', 'CUSTOMER_OFFSITE', 'CUSTOMER')
   AND ext.code = 'ALLO_EXT';

-- ── An older database's weekend ───────────────────────────────────────────
-- The chain this file replaces seeded a different weekend: weekend TZ1
-- 06:00-18:00 and a weekend TZ2 window (18:00-06:00) standing in for TZ3.
-- Bring such a database to the weekend above. Each step only acts on the old
-- shape, so on a database this file built it changes nothing.
--
-- Weekend TZ1's hours change below while assignments still use it, which the
-- used-shift guard in 0153 refuses. It is allowed here, for this transaction
-- only, because the same block recomputes every one of those assignments
-- from the new hours before it commits -- the guard exists to stop exactly
-- the edit that does NOT do that.
SET LOCAL team_schedule.allow_shift_rewrite = 'on';

-- TZ3's escalation window runs every day.
UPDATE team_schedule_shift SET day_scope = 'ANY', updated_on = NOW(), updated_by = 'migration'
 WHERE code = 'SRE_TZ3' AND day_scope <> 'ANY';

-- Turns on the old weekend TZ2 window are TZ3's, with their tier kept. Moved
-- first, onto TZ3's own hours, so the wider weekend TZ1 below cannot overlap
-- a turn still sitting on 18:00-21:00. A day that already holds SRE_TZ3 for
-- the same person keeps that one.
UPDATE team_schedule_assignment a
   SET shift_id   = t3.id,
       zone_id    = t3.zone_id,
       starts_at  = (a.rota_date::timestamp + make_interval(mins => t3.start_minute)) AT TIME ZONE t3.authoring_time_zone,
       ends_at    = (a.rota_date::timestamp + make_interval(mins => t3.end_minute))   AT TIME ZONE t3.authoring_time_zone,
       updated_on = NOW(), updated_by = 'migration'
  FROM team_schedule_shift we2, team_schedule_shift t3
 WHERE a.shift_id = we2.id AND we2.code = 'SRE_WE_TZ2' AND t3.code = 'SRE_TZ3'
   AND NOT EXISTS (SELECT 1 FROM team_schedule_assignment b
                    WHERE b.user_id = a.user_id AND b.rota_date = a.rota_date AND b.shift_id = t3.id);
DELETE FROM team_schedule_assignment a USING team_schedule_shift we2
 WHERE a.shift_id = we2.id AND we2.code = 'SRE_WE_TZ2';
DELETE FROM team_schedule_shift WHERE code = 'SRE_WE_TZ2';

-- Weekend TZ1 is TZ1 and TZ2 together, 06:00-21:00, and the turns on it cover
-- those hours.
UPDATE team_schedule_shift
   SET label = 'Weekend TZ1 + TZ2', short_code = 'TZ1+2', start_minute = 360, end_minute = 1260,
       updated_on = NOW(), updated_by = 'migration'
 WHERE code = 'SRE_WE_TZ1' AND (start_minute, end_minute) IS DISTINCT FROM (360, 1260);
UPDATE team_schedule_assignment a
   SET starts_at  = (a.rota_date::timestamp + make_interval(mins => s.start_minute)) AT TIME ZONE s.authoring_time_zone,
       ends_at    = (a.rota_date::timestamp + make_interval(mins => s.end_minute))   AT TIME ZONE s.authoring_time_zone,
       updated_on = NOW(), updated_by = 'migration'
  FROM team_schedule_shift s
 WHERE s.id = a.shift_id AND s.code = 'SRE_WE_TZ1'
   AND a.ends_at <> (a.rota_date::timestamp + make_interval(mins => s.end_minute)) AT TIME ZONE s.authoring_time_zone;

-- TZ3 is its own zone at the weekend.
UPDATE team_schedule_zone z SET weekend_zone_id = z.id, updated_on = NOW(), updated_by = 'migration'
 WHERE z.code = 'TZ3' AND z.weekend_zone_id IS DISTINCT FROM z.id;

COMMIT;
