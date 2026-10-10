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

-- A database-level history of every Team Schedule table that holds state.
-- The two *_activity tables (0153) are what the portal shows a lead; this is
-- the record underneath them, written by triggers, so a change made outside
-- the application -- a console fix, an import -- is recorded too.
--
-- Numbered after the catalogue (0154) on purpose: the rows that already exist
-- when this runs get a BASELINE entry rather than an INSERT one.

-- One transaction, so a failure part-way leaves nothing behind, and a short
-- lock timeout, so a table another writer holds makes this fail fast instead
-- of queuing every later writer behind it. Re-running after either is safe.
BEGIN;
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS team_schedule_audit (
    id              BIGSERIAL PRIMARY KEY,
    changed_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    table_name      TEXT NOT NULL,
    -- Every audited table is keyed by a uuid id, so one column addresses
    -- them all.
    row_id          UUID,
    action          TEXT NOT NULL,
    -- Who, as far as the database can tell: an actor the application set for
    -- the transaction (app.actor), else the row's own updated_by/created_by.
    -- On a DELETE the row can only offer its last writer, which is not
    -- necessarily the person deleting it -- app.actor is what closes that.
    actor           VARCHAR(255),
    old_row         JSONB,
    new_row         JSONB,
    -- Which columns actually moved. NULL for INSERT, DELETE and BASELINE.
    changed_fields  TEXT[],
    CONSTRAINT team_schedule_audit_action_check
        CHECK (action IN ('INSERT', 'UPDATE', 'DELETE', 'BASELINE'))
);

CREATE INDEX IF NOT EXISTS idx_team_schedule_audit_row
    ON team_schedule_audit (table_name, row_id, changed_at DESC);
CREATE INDEX IF NOT EXISTS idx_team_schedule_audit_when
    ON team_schedule_audit (changed_at DESC);
CREATE INDEX IF NOT EXISTS idx_team_schedule_audit_actor
    ON team_schedule_audit (actor, changed_at DESC);

CREATE OR REPLACE FUNCTION team_schedule_audit_row() RETURNS TRIGGER AS $$
DECLARE
    v_old JSONB;
    v_new JSONB;
    v_actor TEXT;
    v_fields TEXT[];
BEGIN
    IF TG_OP = 'DELETE' THEN
        v_old := to_jsonb(OLD);
    ELSIF TG_OP = 'INSERT' THEN
        v_new := to_jsonb(NEW);
    ELSE
        v_old := to_jsonb(OLD);
        v_new := to_jsonb(NEW);
        -- A row touched without anything of substance moving is not a
        -- change. updated_on/updated_by move on every write by construction.
        SELECT array_agg(e.key ORDER BY e.key) INTO v_fields
          FROM jsonb_each(v_new) e
         WHERE e.key NOT IN ('updated_on', 'updated_by')
           AND v_new -> e.key IS DISTINCT FROM v_old -> e.key;
        IF v_fields IS NULL THEN
            RETURN NULL;
        END IF;
    END IF;

    v_actor := COALESCE(
        NULLIF(current_setting('app.actor', true), ''),
        v_new ->> 'updated_by', v_new ->> 'created_by',
        v_old ->> 'updated_by', v_old ->> 'created_by');

    INSERT INTO team_schedule_audit
        (table_name, row_id, action, actor, old_row, new_row, changed_fields)
    VALUES (
        TG_TABLE_NAME,
        COALESCE((v_new ->> 'id')::uuid, (v_old ->> 'id')::uuid),
        TG_OP, v_actor, v_old, v_new, v_fields);

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

-- The five tables that hold state. The activity tables are append-only
-- history already; auditing them would record each change twice.
DROP TRIGGER IF EXISTS team_schedule_assignment_audit ON team_schedule_assignment;
CREATE TRIGGER team_schedule_assignment_audit
    AFTER INSERT OR UPDATE OR DELETE ON team_schedule_assignment
    FOR EACH ROW EXECUTE FUNCTION team_schedule_audit_row();

DROP TRIGGER IF EXISTS team_schedule_absence_audit ON team_schedule_absence;
CREATE TRIGGER team_schedule_absence_audit
    AFTER INSERT OR UPDATE OR DELETE ON team_schedule_absence
    FOR EACH ROW EXECUTE FUNCTION team_schedule_audit_row();

DROP TRIGGER IF EXISTS team_schedule_absence_kind_audit ON team_schedule_absence_kind;
CREATE TRIGGER team_schedule_absence_kind_audit
    AFTER INSERT OR UPDATE OR DELETE ON team_schedule_absence_kind
    FOR EACH ROW EXECUTE FUNCTION team_schedule_audit_row();

DROP TRIGGER IF EXISTS team_schedule_shift_audit ON team_schedule_shift;
CREATE TRIGGER team_schedule_shift_audit
    AFTER INSERT OR UPDATE OR DELETE ON team_schedule_shift
    FOR EACH ROW EXECUTE FUNCTION team_schedule_audit_row();

DROP TRIGGER IF EXISTS team_schedule_zone_audit ON team_schedule_zone;
CREATE TRIGGER team_schedule_zone_audit
    AFTER INSERT OR UPDATE OR DELETE ON team_schedule_zone
    FOR EACH ROW EXECUTE FUNCTION team_schedule_audit_row();

-- A baseline for what is already here, so "this row has no history" is not
-- ambiguous between "never touched" and "predates the audit". Only for rows
-- with no audit entry yet, so a re-run adds nothing.
INSERT INTO team_schedule_audit (changed_at, table_name, row_id, action, actor, new_row)
SELECT now(), t.table_name, t.row_id, 'BASELINE', t.actor, t.new_row
  FROM (
        SELECT 'team_schedule_assignment' AS table_name, a.id AS row_id, a.created_by AS actor, to_jsonb(a) AS new_row FROM team_schedule_assignment a
        UNION ALL
        SELECT 'team_schedule_absence', a.id, a.created_by, to_jsonb(a) FROM team_schedule_absence a
        UNION ALL
        SELECT 'team_schedule_absence_kind', a.id, a.created_by, to_jsonb(a) FROM team_schedule_absence_kind a
        UNION ALL
        SELECT 'team_schedule_shift', a.id, a.created_by, to_jsonb(a) FROM team_schedule_shift a
        UNION ALL
        SELECT 'team_schedule_zone', a.id, a.created_by, to_jsonb(a) FROM team_schedule_zone a
       ) t
 WHERE NOT EXISTS (
        SELECT 1 FROM team_schedule_audit x
         WHERE x.table_name = t.table_name AND x.row_id = t.row_id);

COMMIT;
