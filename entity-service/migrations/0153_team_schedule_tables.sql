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

-- Team Schedule: who from CRE and SRE is working, when, and in which
-- escalation tier. There is no ServiceNow equivalent -- this is portal-native
-- data, like announcement_requests -- so these tables are the system of
-- record, not a mirror of one. Not to be confused with schedule /
-- user_schedule / schedule_span (0050), which mirror ServiceNow's own
-- cmn_schedule tables; every table here carries the team_schedule_ prefix so
-- the two can never be mistaken for each other.
--
-- The rota is AUTHORED in one clock (IST, the clock the shift windows are
-- written in) and READ in whatever clock the viewer is in. That is why
-- team_schedule_shift stores minutes-of-day against an authoring_time_zone,
-- while team_schedule_assignment stores the resolved absolute instants: the
-- catalogue stays human-editable, and "who is on duty at 03:14 UTC" stays a
-- plain indexed range query that no client has to re-derive.
--
-- This file is the schema only. The catalogue rows it needs to render
-- anything are in 0154, and the change history in 0155. Requires team.key
-- (0152).
--
-- It replaces the pre-restructure 000088-000107 chain with the state that
-- chain ended in. Safe to re-run, and a no-op on a database that already
-- applied that chain under its old file names.

-- ── A partial run of the old chain ────────────────────────────────────────
-- Before 000088-000104 were renumbered, `make migrate` could apply the first
-- two of them -- which created schedule_zone/_shift/_assignment/_absence under
-- their pre-rename names -- and then stop at the third. Those tables hold
-- only catalogue rows, since no code has ever written to them under those
-- names, so they are dropped and rebuilt below rather than carried forward.
-- Anything that is NOT catalogue -- a single assignment or absence -- means
-- real data, and stops the migration instead of dropping it.
-- One transaction, so a failure part-way leaves nothing behind, and a short
-- lock timeout, so a table another writer holds makes this fail fast instead
-- of queuing every later writer behind it. Re-running after either is safe.
BEGIN;
SET LOCAL lock_timeout = '5s';

DO $$
BEGIN
    IF to_regclass('team_schedule_shift') IS NULL AND to_regclass('schedule_shift') IS NOT NULL THEN
        IF (to_regclass('schedule_assignment') IS NOT NULL AND EXISTS (SELECT 1 FROM schedule_assignment))
        OR (to_regclass('schedule_absence') IS NOT NULL AND EXISTS (SELECT 1 FROM schedule_absence)) THEN
            RAISE EXCEPTION
                'schedule_assignment / schedule_absence hold rows from a partial Team Schedule install. Nothing has been changed; move or remove those rows before applying this migration.';
        END IF;
        DROP TABLE IF EXISTS schedule_assignment, schedule_absence, schedule_shift, schedule_zone;
        DROP TYPE IF EXISTS schedule_shift_family_enum, schedule_tier_enum, schedule_day_scope_enum,
                            schedule_source_enum, schedule_absence_kind_enum;
    END IF;
END $$;

-- The two EXCLUDE constraints below pair an equality test on a uuid with an
-- overlap test on a range, and a plain GiST opclass has no equality operator
-- for uuid. btree_gist ships with Postgres (contrib); on a managed server it
-- may need allow-listing first (Azure: the azure.extensions parameter).
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- ── Vocabulary ────────────────────────────────────────────────────────────

DO $$ BEGIN
    CREATE TYPE team_schedule_shift_family_enum AS ENUM ('CRE', 'SRE');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- L1/L2/L3 are escalation tiers, not seniority. A shift with tier NULL is
-- ordinary working hours, or a window whose tier is a fact about the person
-- that week rather than about the window.
DO $$ BEGIN
    CREATE TYPE team_schedule_tier_enum AS ENUM ('L1', 'L2', 'L3');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- Weekday and weekend are different shapes of day, not different windows of
-- the same shape: SRE runs three zones on a weekday and two at the weekend.
DO $$ BEGIN
    CREATE TYPE team_schedule_day_scope_enum AS ENUM ('WEEKDAY', 'WEEKEND', 'ANY');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- How a row got there. GENERATED rows came from the round-robin allocator and
-- may be regenerated in bulk; MANUAL and SWAP rows were put there by a lead
-- and must survive any regeneration; IMPORTED came from a rota sheet.
DO $$ BEGIN
    CREATE TYPE team_schedule_source_enum AS ENUM ('GENERATED', 'MANUAL', 'SWAP', 'IMPORTED');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- LEAVE is time off and is never counted against a weekend; ALLOCATION is
-- work done elsewhere; EXCLUDED is off the rota entirely.
DO $$ BEGIN
    CREATE TYPE team_schedule_absence_bucket_enum AS ENUM ('LEAVE', 'ALLOCATION', 'EXCLUDED');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- ── The catalogue ─────────────────────────────────────────────────────────

-- An SRE time zone. TZ1/TZ2/TZ3 on a weekday; at the weekend the day
-- collapses to two, so each weekday zone names the weekend zone that absorbs
-- it (TZ1->TZ1, TZ2->TZ1, TZ3->TZ2). Without that mapping a Saturday's
-- 00:00-06:00 -- Friday's TZ3 crew -- belongs to no weekend zone at all.
CREATE TABLE IF NOT EXISTS team_schedule_zone (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    created_on          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_on          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by          VARCHAR(255),
    updated_by          VARCHAR(255),
    code                VARCHAR(8) NOT NULL UNIQUE,
    label               VARCHAR(50) NOT NULL,
    weekend_zone_id     UUID REFERENCES team_schedule_zone(id) ON DELETE SET NULL,
    sort_order          SMALLINT NOT NULL DEFAULT 0,
    is_active           BOOLEAN NOT NULL DEFAULT TRUE
);

-- A named window of the working day. Minutes count from midnight in
-- authoring_time_zone; an end past 1440 runs into the next day, so a night
-- block 21:00-06:00 is one row, 1260 -> 1800, not two a reader must stitch.
CREATE TABLE IF NOT EXISTS team_schedule_shift (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    created_on          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_on          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by          VARCHAR(255),
    updated_by          VARCHAR(255),
    code                VARCHAR(32) NOT NULL UNIQUE,
    label               VARCHAR(100) NOT NULL,
    family              team_schedule_shift_family_enum NOT NULL,
    zone_id             UUID REFERENCES team_schedule_zone(id) ON DELETE RESTRICT,
    tier                team_schedule_tier_enum,
    day_scope           team_schedule_day_scope_enum NOT NULL DEFAULT 'WEEKDAY',
    start_minute        SMALLINT NOT NULL,
    end_minute          SMALLINT NOT NULL,
    authoring_time_zone VARCHAR(64) NOT NULL DEFAULT 'Asia/Colombo',
    is_on_call          BOOLEAN NOT NULL DEFAULT FALSE,
    is_escalation       BOOLEAN NOT NULL DEFAULT FALSE,
    sort_order          SMALLINT NOT NULL DEFAULT 0,
    is_active           BOOLEAN NOT NULL DEFAULT TRUE,
    crosses_midnight    BOOLEAN GENERATED ALWAYS AS (end_minute > 1440) STORED,
    -- The chip a roster cell draws: a short code and a colour, both distinct
    -- from code and label.
    short_code          VARCHAR(12) NOT NULL,
    colour_token        VARCHAR(24) NOT NULL,
    is_rotation         BOOLEAN NOT NULL DEFAULT TRUE,
    required_headcount  SMALLINT,
    CONSTRAINT team_schedule_shift_start_minute_check
        CHECK (start_minute >= 0 AND start_minute < 1440),
    -- At most one midnight: > 1440 is the next day, and crosses_midnight is
    -- generated from exactly that. A typo like 2000 is refused.
    CONSTRAINT team_schedule_shift_end_minute_check
        CHECK (end_minute > start_minute AND end_minute <= start_minute + 1440),
    -- Only SRE works in time zones, and a window that hosts an escalation
    -- tier must say which zone it covers, or "who is L1 right now" has no
    -- answer.
    CONSTRAINT team_schedule_shift_zone_family_check
        CHECK (zone_id IS NULL OR family = 'SRE'),
    CONSTRAINT team_schedule_shift_escalation_zone_check
        CHECK (NOT is_escalation OR zone_id IS NOT NULL),
    CONSTRAINT team_schedule_shift_required_headcount_check
        CHECK (required_headcount IS NULL OR required_headcount > 0)
);

COMMENT ON COLUMN team_schedule_shift.is_rotation IS
  'TRUE when being on this window is a turn somebody takes; FALSE when it is simply when a team works.';
COMMENT ON COLUMN team_schedule_shift.required_headcount IS
  'Target number of engineers for this window; NULL means no target is defined, which is not the same as zero.';

CREATE INDEX IF NOT EXISTS idx_team_schedule_shift_family  ON team_schedule_shift (family);
CREATE INDEX IF NOT EXISTS idx_team_schedule_shift_zone_id ON team_schedule_shift (zone_id);

-- What takes an engineer out of the rota for whole days. Rows, not an enum:
-- a new kind is an insert a lead can ask for, not an ALTER TYPE and a deploy.
CREATE TABLE IF NOT EXISTS team_schedule_absence_kind (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    created_on    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_on    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by    VARCHAR(255),
    updated_by    VARCHAR(255),
    code          VARCHAR(32) NOT NULL UNIQUE,
    short_code    VARCHAR(12) NOT NULL,
    label         VARCHAR(100) NOT NULL,
    bucket        team_schedule_absence_bucket_enum NOT NULL,
    colour_token  VARCHAR(24) NOT NULL,
    sort_order    SMALLINT NOT NULL DEFAULT 0,
    -- A retired kind is deactivated, never deleted: absences point at it
    -- ON DELETE RESTRICT. The catalogue still serves it, marked retired, so
    -- the days already marked with it keep their label; nothing offers it.
    is_active     BOOLEAN NOT NULL DEFAULT TRUE,
    -- The rota a kind is offered on: CRE and SRE allocate time to different
    -- things (only SRE does RnD; only CRE does Migration). NULL is both,
    -- which is every kind of leave.
    family        team_schedule_shift_family_enum
);

-- An older database's table, from before a kind had a rota of its own.
ALTER TABLE team_schedule_absence_kind
    ADD COLUMN IF NOT EXISTS family team_schedule_shift_family_enum;

-- ── The facts ─────────────────────────────────────────────────────────────

-- One engineer, one rota day, one window. Every assignment is stored, not
-- derived: the roster is evidence of who was responsible at a given moment,
-- so a change to the allocator must never rewrite what already happened.
--
-- rota_date is the day the CREW is rostered for, not the calendar date of
-- every hour worked: a Monday 21:00-06:00 block is Monday's even though six
-- of its hours fall on Tuesday.
--
-- starts_at/ends_at are resolved from shift + rota_date + authoring_time_zone
-- at write time. They are all a point-in-time lookup touches, and they stay
-- correct across DST because they are absolute instants.
--
-- zone_id and tier are stored rather than read through shift_id because the
-- catalogue leaves them open in places (which of L1/L2 someone is that week
-- is a fact about the person). Where the shift does fix them, the trigger
-- below holds the assignment to it.
CREATE TABLE IF NOT EXISTS team_schedule_assignment (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    created_on          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_on          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by          VARCHAR(255),
    updated_by          VARCHAR(255),
    -- RESTRICT, not CASCADE: the roster is a record of who was responsible,
    -- and deleting a user must not erase it. Users are deactivated, not
    -- deleted; this makes that assumption fail loudly if it is ever broken.
    user_id             UUID NOT NULL REFERENCES "user"(id) ON DELETE RESTRICT,
    team_id             UUID REFERENCES team(id) ON DELETE SET NULL,
    team_key            VARCHAR(64) NOT NULL REFERENCES team(key) ON UPDATE CASCADE,
    shift_id            UUID NOT NULL REFERENCES team_schedule_shift(id) ON DELETE RESTRICT,
    zone_id             UUID REFERENCES team_schedule_zone(id) ON DELETE RESTRICT,
    tier                team_schedule_tier_enum,
    rota_date           DATE NOT NULL,
    starts_at           TIMESTAMPTZ NOT NULL,
    ends_at             TIMESTAMPTZ NOT NULL,
    is_on_call          BOOLEAN NOT NULL DEFAULT FALSE,
    source              team_schedule_source_enum NOT NULL DEFAULT 'MANUAL',
    note                TEXT,
    -- The window's own is_rotation, copied by the trigger below so the
    -- no-overlap rule can apply to turns only. Never written by hand.
    is_rotation         BOOLEAN NOT NULL DEFAULT TRUE,
    CONSTRAINT team_schedule_assignment_window_check CHECK (ends_at > starts_at),
    CONSTRAINT team_schedule_assignment_unique_slot UNIQUE (user_id, rota_date, shift_id),
    -- team_id is kept beside team_key so an engineer who moves team does not
    -- retroactively change who covered a past shift; the pair must agree.
    CONSTRAINT team_schedule_assignment_team_agrees
        FOREIGN KEY (team_id, team_key) REFERENCES team (id, key) ON UPDATE CASCADE,
    -- Nobody holds two turns at once. Over the resolved instants, half-open,
    -- so a window ending 18:00 and one starting 18:00 are a handover.
    --
    -- Turns only: a zone's regular hours are when somebody works, and an
    -- engineer on TZ1's regular hours who is also TZ1 L1 for the morning is
    -- doing both -- the turn is part of their working day, not a second place
    -- to be. So regular hours may sit under a turn; two turns may not overlap.
    CONSTRAINT team_schedule_assignment_no_overlap
        EXCLUDE USING gist (user_id WITH =, tstzrange(starts_at, ends_at, '[)') WITH &&)
        WHERE (is_rotation),
    -- Nobody works two sets of regular hours at once either. Regular hours
    -- are one window per day: TZ1's and TZ2's overlap (06:00-15:00 and
    -- 12:00-21:00), and CRE's regular and India-region windows are the same
    -- hours, so holding two would say an engineer is in two places for the
    -- same hours. Setting a zone's regular hours already replaces the day's
    -- other regular hours; this makes the database say so too. Turns are
    -- still allowed over regular hours -- only like overlapping like is ruled
    -- out.
    CONSTRAINT team_schedule_assignment_no_overlap_regular
        EXCLUDE USING gist (user_id WITH =, tstzrange(starts_at, ends_at, '[)') WITH &&)
        WHERE (NOT is_rotation)
);

COMMENT ON COLUMN team_schedule_assignment.team_key IS
    'The team this assignment belongs to, by team.key. team_id is kept alongside it, and the pair is constrained to agree.';

CREATE INDEX IF NOT EXISTS idx_team_schedule_assignment_rota_date
    ON team_schedule_assignment (rota_date);
CREATE INDEX IF NOT EXISTS idx_team_schedule_assignment_user_date
    ON team_schedule_assignment (user_id, rota_date);
CREATE INDEX IF NOT EXISTS idx_team_schedule_assignment_team_date
    ON team_schedule_assignment (team_key, rota_date);
CREATE INDEX IF NOT EXISTS idx_team_schedule_assignment_zone_tier_date
    ON team_schedule_assignment (zone_id, tier, rota_date);
-- "Who is on duty at this instant" -- the question an alert escalation asks.
CREATE INDEX IF NOT EXISTS idx_team_schedule_assignment_window
    ON team_schedule_assignment USING GIST (tstzrange(starts_at, ends_at, '[)'));

-- A table built by the old chain has neither is_rotation nor the turns-only
-- no-overlap rule. Bring it up to date: add the column, fill it from each
-- row's window, and put the rule back with its WHERE. A no-op on a table
-- this file created.
ALTER TABLE team_schedule_assignment ADD COLUMN IF NOT EXISTS is_rotation BOOLEAN NOT NULL DEFAULT TRUE;
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'team_schedule_assignment_no_overlap'
           AND pg_get_constraintdef(oid) LIKE '%WHERE%is_rotation%'
    ) THEN
        ALTER TABLE team_schedule_assignment DROP CONSTRAINT IF EXISTS team_schedule_assignment_no_overlap;
        UPDATE team_schedule_assignment a
           SET is_rotation = s.is_rotation
          FROM team_schedule_shift s
         WHERE s.id = a.shift_id AND a.is_rotation IS DISTINCT FROM s.is_rotation;
        ALTER TABLE team_schedule_assignment
            ADD CONSTRAINT team_schedule_assignment_no_overlap
            EXCLUDE USING gist (user_id WITH =, tstzrange(starts_at, ends_at, '[)') WITH &&)
            WHERE (is_rotation);
    END IF;
END $$;

-- The regular-hours rule, on a table built before it existed. Refuse with a
-- count and the fix rather than a bare constraint error: the rows that
-- overlap are real rota data, and which of each pair to keep is a lead's call.
DO $$
DECLARE clashes BIGINT;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'team_schedule_assignment_no_overlap_regular') THEN
        SELECT count(*) INTO clashes
          FROM team_schedule_assignment a
          JOIN team_schedule_assignment b
            ON b.user_id = a.user_id AND b.id > a.id
           AND NOT a.is_rotation AND NOT b.is_rotation
           AND tstzrange(a.starts_at, a.ends_at, '[)') && tstzrange(b.starts_at, b.ends_at, '[)');
        IF clashes > 0 THEN
            RAISE EXCEPTION
                '% pair(s) of team_schedule_assignment regular-hours rows overlap for the same engineer. Remove one of each pair (keep the one the lead meant), then re-run this migration.',
                clashes;
        END IF;
        ALTER TABLE team_schedule_assignment
            ADD CONSTRAINT team_schedule_assignment_no_overlap_regular
            EXCLUDE USING gist (user_id WITH =, tstzrange(starts_at, ends_at, '[)') WITH &&)
            WHERE (NOT is_rotation);
    END IF;
END $$;

-- An older table's user_id cascades on delete. Move both facts tables to
-- RESTRICT: found by what the constraint does, not by its name, so a
-- differently-named constraint on some server is handled too.
DO $$
DECLARE c RECORD;
BEGIN
    FOR c IN
        SELECT con.conname, cl.relname
          FROM pg_constraint con
          JOIN pg_class cl ON cl.oid = con.conrelid
          JOIN pg_attribute att ON att.attrelid = con.conrelid AND att.attnum = ANY (con.conkey)
         WHERE con.contype = 'f'
           AND con.confrelid = '"user"'::regclass
           AND con.confdeltype = 'c'
           AND cl.relname IN ('team_schedule_assignment', 'team_schedule_absence')
           AND att.attname = 'user_id'
    LOOP
        EXECUTE format('ALTER TABLE %I DROP CONSTRAINT %I', c.relname, c.conname);
        EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I FOREIGN KEY (user_id) REFERENCES "user"(id) ON DELETE RESTRICT',
                       c.relname, c.conname);
    END LOOP;
END $$;

-- Where the shift fixes a zone or a tier, the assignment must match it;
-- where it leaves one open, the assignment may fill it in. A CHECK cannot
-- reach another table, so this is a trigger. It also copies the window's
-- is_rotation onto the row, which the no-overlap rule reads.
CREATE OR REPLACE FUNCTION team_schedule_assignment_matches_shift()
RETURNS TRIGGER AS $$
DECLARE
    shift_zone UUID;
    shift_tier team_schedule_tier_enum;
    shift_code TEXT;
    shift_rotation BOOLEAN;
BEGIN
    SELECT s.zone_id, s.tier, s.code, s.is_rotation
      INTO shift_zone, shift_tier, shift_code, shift_rotation
      FROM team_schedule_shift s WHERE s.id = NEW.shift_id;

    NEW.is_rotation := COALESCE(shift_rotation, TRUE);

    IF shift_zone IS NOT NULL AND NEW.zone_id IS DISTINCT FROM shift_zone THEN
        RAISE EXCEPTION
            'assignment zone does not match shift % (shift fixes zone %, assignment says %)',
            shift_code, shift_zone, NEW.zone_id
            USING ERRCODE = 'check_violation';
    END IF;

    IF shift_tier IS NOT NULL AND NEW.tier IS DISTINCT FROM shift_tier THEN
        RAISE EXCEPTION
            'assignment tier does not match shift % (shift fixes tier %, assignment says %)',
            shift_code, shift_tier, NEW.tier
            USING ERRCODE = 'check_violation';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS team_schedule_assignment_matches_shift_trigger ON team_schedule_assignment;
CREATE TRIGGER team_schedule_assignment_matches_shift_trigger
    BEFORE INSERT OR UPDATE OF shift_id, zone_id, tier ON team_schedule_assignment
    FOR EACH ROW EXECUTE FUNCTION team_schedule_assignment_matches_shift();

-- The reverse direction. The trigger above holds an assignment to its shift
-- when the assignment is written; nothing held the shift to the assignments
-- already written against it. Changing a used shift's hours, zone, tier,
-- family or turn/regular flag would leave every existing row saying something
-- the shift no longer does -- starts_at/ends_at resolved from the old hours,
-- is_rotation copied from the old flag -- with no error. So a shift that any
-- assignment uses is frozen in those respects: a correction is a new shift
-- code, and the old one is retired (is_active FALSE). Label, short code,
-- colour, sort order, day scope and is_active stay editable; none of them is
-- copied onto an assignment.
--
-- A migration that corrects the catalogue and recomputes the affected rows in
-- the same transaction may say so with
--   SET LOCAL team_schedule.allow_shift_rewrite = 'on';
-- 0154 does, for its own upgrade of an older database's weekend.
CREATE OR REPLACE FUNCTION team_schedule_shift_guard_used()
RETURNS TRIGGER AS $$
BEGIN
    IF (NEW.zone_id, NEW.tier, NEW.family, NEW.is_rotation,
        NEW.start_minute, NEW.end_minute, NEW.authoring_time_zone)
       IS NOT DISTINCT FROM
       (OLD.zone_id, OLD.tier, OLD.family, OLD.is_rotation,
        OLD.start_minute, OLD.end_minute, OLD.authoring_time_zone) THEN
        RETURN NEW;
    END IF;
    IF current_setting('team_schedule.allow_shift_rewrite', true) = 'on' THEN
        RETURN NEW;
    END IF;
    IF EXISTS (SELECT 1 FROM team_schedule_assignment WHERE shift_id = OLD.id) THEN
        RAISE EXCEPTION
            'shift % is used by existing assignments, so its hours, zone, tier, family and turn/regular flag cannot change. Add a new shift code and retire this one instead.',
            OLD.code
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS team_schedule_shift_guard_used_trigger ON team_schedule_shift;
CREATE TRIGGER team_schedule_shift_guard_used_trigger
    BEFORE UPDATE ON team_schedule_shift
    FOR EACH ROW EXECUTE FUNCTION team_schedule_shift_guard_used();

-- Whole days an engineer is not available to the rota: leave, or time given
-- to R&D or to a customer. A range, because that is how it is granted -- one
-- row for "12-19 March", not eight. ends_on NULL is open-ended ("allocated
-- until further notice"). A range may span a weekend; which days inside it
-- count is a service-layer rule.
CREATE TABLE IF NOT EXISTS team_schedule_absence (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    created_on          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_on          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by          VARCHAR(255),
    updated_by          VARCHAR(255),
    -- RESTRICT, for the same reason as the assignment's user_id.
    user_id             UUID NOT NULL REFERENCES "user"(id) ON DELETE RESTRICT,
    team_key            VARCHAR(64) NOT NULL REFERENCES team(key) ON UPDATE CASCADE,
    starts_on           DATE NOT NULL,
    ends_on             DATE,
    note                TEXT,
    approved_by         VARCHAR(255),
    approved_on         TIMESTAMPTZ,
    kind_id             UUID NOT NULL REFERENCES team_schedule_absence_kind(id) ON DELETE RESTRICT,
    allocated_to        VARCHAR(100),
    CONSTRAINT team_schedule_absence_range_check CHECK (ends_on IS NULL OR ends_on >= starts_on),
    -- Not away twice for two reasons. Closed at both ends: a leave day is a
    -- whole day, so 1st-3rd and 3rd-5th is a real clash.
    CONSTRAINT team_schedule_absence_no_overlap
        EXCLUDE USING gist (user_id WITH =, daterange(starts_on, ends_on, '[]') WITH &&)
);

COMMENT ON COLUMN team_schedule_absence.team_key IS
    'The team this absence belongs to, by team.key. No team_id column on purpose: nothing joins an absence to a team row, and the foreign key on this column is what integrity the id would have added.';
COMMENT ON COLUMN team_schedule_absence.allocated_to IS
  'Who an allocation is for: the customer for a customer allocation, the product team for RnD. NULL when not known or not an allocation.';

CREATE INDEX IF NOT EXISTS idx_team_schedule_absence_user
    ON team_schedule_absence (user_id, starts_on, ends_on);
CREATE INDEX IF NOT EXISTS idx_team_schedule_absence_team
    ON team_schedule_absence (team_key, starts_on);
CREATE INDEX IF NOT EXISTS idx_team_schedule_absence_kind_id
    ON team_schedule_absence (kind_id);
CREATE INDEX IF NOT EXISTS idx_team_schedule_absence_span
    ON team_schedule_absence USING GIST (daterange(starts_on, ends_on, '[]'));

-- ── What leads changed ────────────────────────────────────────────────────
-- Append-only, one row per change, read by the portal's "Recent changes"
-- panel. Deliberately no foreign keys: a delete is the change most worth
-- keeping, and a cascade would erase exactly that record. Everything a reader
-- needs is denormalised onto the row so it survives the row it describes.

CREATE TABLE IF NOT EXISTS team_schedule_assignment_activity (
    id              UUID PRIMARY KEY,
    created_on      TIMESTAMPTZ NOT NULL,
    created_by      VARCHAR(255) NOT NULL,
    assignment_id   UUID NOT NULL,
    user_id         UUID NOT NULL,
    team_key        VARCHAR(100) NOT NULL,
    rota_date       DATE NOT NULL,
    shift_code      VARCHAR(100) NOT NULL,
    action          VARCHAR(20) NOT NULL,
    field_name      VARCHAR(255),
    old_value       VARCHAR(255),
    new_value       VARCHAR(255),
    -- The person who made the change, not the person the row is about.
    actor_email     VARCHAR(255) NOT NULL,
    note            TEXT,
    CONSTRAINT team_schedule_assignment_activity_action_check
        CHECK (action IN ('CREATED', 'UPDATED', 'DELETED'))
);

CREATE INDEX IF NOT EXISTS idx_team_schedule_assignment_activity_assignment
    ON team_schedule_assignment_activity (assignment_id, created_on DESC);
CREATE INDEX IF NOT EXISTS idx_team_schedule_assignment_activity_user_date
    ON team_schedule_assignment_activity (user_id, rota_date);
CREATE INDEX IF NOT EXISTS idx_team_schedule_assignment_activity_team_date
    ON team_schedule_assignment_activity (team_key, rota_date);
CREATE INDEX IF NOT EXISTS idx_team_schedule_assignment_activity_date
    ON team_schedule_assignment_activity (rota_date, created_on DESC);

CREATE TABLE IF NOT EXISTS team_schedule_absence_activity (
    id              UUID PRIMARY KEY,
    created_on      TIMESTAMPTZ NOT NULL,
    created_by      VARCHAR(255) NOT NULL,
    absence_id      UUID NOT NULL,
    user_id         UUID NOT NULL,
    team_key        VARCHAR(100) NOT NULL,
    kind_code       VARCHAR(64) NOT NULL,
    starts_on       DATE NOT NULL,
    ends_on         DATE,
    -- TRIMMED: part of a span was cleared, so the absence was shortened
    -- rather than removed. Recording it as a delete would say the leave was
    -- cancelled when most of it still stands.
    action          VARCHAR(20) NOT NULL,
    field_name      VARCHAR(255),
    old_value       VARCHAR(255),
    new_value       VARCHAR(255),
    actor_email     VARCHAR(255) NOT NULL,
    note            TEXT,
    CONSTRAINT team_schedule_absence_activity_action_check
        CHECK (action IN ('CREATED', 'UPDATED', 'DELETED', 'TRIMMED'))
);

CREATE INDEX IF NOT EXISTS idx_team_schedule_absence_activity_absence
    ON team_schedule_absence_activity (absence_id, created_on DESC);
CREATE INDEX IF NOT EXISTS idx_team_schedule_absence_activity_user
    ON team_schedule_absence_activity (user_id, starts_on);
CREATE INDEX IF NOT EXISTS idx_team_schedule_absence_activity_team
    ON team_schedule_absence_activity (team_key, starts_on);

COMMIT;
