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

-- team.key: a stable, lower-case handle for a team, which the Team Schedule
-- (0153-0155) refers to teams by. The rota's catalogue is read from this table
-- -- a team whose type starts with CRE or SRE is a rota team -- and every
-- assignment and absence names its team by key, under a foreign key.
--
-- This is the one Team Schedule migration that touches a shared table. team
-- is written by the ServiceNow sync (0033), which knows nothing about key, so
-- the column must never make one of the sync's writes fail:
--
--   * A BEFORE INSERT trigger fills key from the name when a writer leaves it
--     out. Without it, NOT NULL rejects every such insert -- including an
--     INSERT ... ON CONFLICT DO UPDATE of a team that already exists, because
--     the proposed row is checked before the conflict is resolved.
--   * An existing row's key is never rewritten, so a rename in ServiceNow
--     does not move a team's rota history out from under it.
--   * A filled key always fits and never collides. team.name allows 255
--     characters and key 64, and a new team can share a name, ignoring
--     case, with one that already has a key; either would otherwise fail
--     the sync's insert on the length or on team_key_unique. The name is
--     cut to fit, and on a clash the team's own id is appended.
--
-- Safe to re-run: every step is guarded, and the backfill only fills blanks.

-- One transaction, and a short lock timeout. team is written by the ServiceNow
-- sync while this runs, and the column, the NOT NULL, the two constraints and
-- the trigger each need an exclusive lock on it: waiting indefinitely would
-- queue every sync write behind this migration, so it gives up after 5s
-- instead. Nothing is left half-done either way; re-run it when the table is
-- quiet.
BEGIN;
SET LOCAL lock_timeout = '5s';

ALTER TABLE team ADD COLUMN IF NOT EXISTS key VARCHAR(64);

CREATE OR REPLACE FUNCTION team_fill_key() RETURNS TRIGGER AS $$
DECLARE
    base      TEXT := lower(NEW.name);
    candidate TEXT := left(lower(NEW.name), 64);
    n         INT  := 0;
BEGIN
    IF NEW.key IS NOT NULL THEN
        RETURN NEW;
    END IF;
    -- Another team's key only: an upsert of this very team (same id) finds
    -- its own row, and that is no clash -- the conflict resolves to an
    -- UPDATE, which leaves key alone.
    WHILE EXISTS (SELECT 1 FROM team WHERE key = candidate AND id IS DISTINCT FROM NEW.id) LOOP
        n := n + 1;
        -- 36 for the id, 1 for its dash; a counter only from the second try.
        candidate := CASE WHEN n = 1
            THEN left(base, 64 - 37) || '-' || NEW.id::text
            ELSE left(base, 64 - 38 - length(n::text)) || '-' || NEW.id::text || '-' || n
        END;
    END LOOP;
    NEW.key := candidate;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS team_fill_key_trigger ON team;
CREATE TRIGGER team_fill_key_trigger
    BEFORE INSERT ON team
    FOR EACH ROW EXECUTE FUNCTION team_fill_key();

-- Two teams whose names differ only by case would get the same key. Refuse
-- with the names rather than let the UNIQUE below fail with a bare
-- constraint error: the fix is to give one of them a key by hand
-- (UPDATE team SET key = '...' WHERE id = '...') and re-run this file.
DO $$
DECLARE clash TEXT;
BEGIN
    SELECT string_agg(k, ', ') INTO clash FROM (
        SELECT COALESCE(key, left(lower(name), 64)) AS k
          FROM team
         GROUP BY 1
        HAVING count(*) > 1
    ) d;
    IF clash IS NOT NULL THEN
        RAISE EXCEPTION
            'team.key would not be unique for: %. Set a distinct key on one of each pair, then re-run this migration.',
            clash;
    END IF;
END $$;

UPDATE team SET key = left(lower(name), 64) WHERE key IS NULL;

ALTER TABLE team ALTER COLUMN key SET NOT NULL;

DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'team_key_unique') THEN
        ALTER TABLE team ADD CONSTRAINT team_key_unique UNIQUE (key);
    END IF;
    -- The pair an assignment's (team_id, team_key) is constrained to agree
    -- with. key alone is already unique; this second constraint exists only
    -- because a composite foreign key needs a matching unique target.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'team_id_key_unique') THEN
        ALTER TABLE team ADD CONSTRAINT team_id_key_unique UNIQUE (id, key);
    END IF;
END $$;

COMMENT ON COLUMN team.key IS
    'Stable lower-case handle the Team Schedule refers to this team by. Filled from the name on insert when a writer leaves it out; never rewritten after that.';

COMMIT;
