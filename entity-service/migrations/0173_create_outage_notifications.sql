-- Go's own record of which internal-stakeholder outage emails it has sent.
--
-- Deliberately NOT a column on `outage`. That table is mirrored from
-- ServiceNow by csm-sync-service (digiops-cs migration 0083), so anything Go
-- wrote there would be overwritten by the next sync run -- the same reasoning
-- that keeps project_query_hours separate from the sync-owned customer_project
-- columns.
--
-- `outage.internal_notification_phase` (added by digiops-cs 0089) is the
-- ServiceNow flow's own copy of this state. It is READ ONCE to seed a row
-- here and never written. Seeding matters: starting empty would make every
-- already-announced outage look un-announced and re-declare all of them on the
-- first sweep.

DO $$ BEGIN
    -- Mirrors ServiceNow's u_internal_notification_phase choice list. NONE is
    -- a real state -- "no email has gone out yet" -- not an absence.
    CREATE TYPE outage_notification_phase_enum AS ENUM (
        'NONE', 'DECLARED', 'RESOLVED'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

CREATE TABLE IF NOT EXISTS outage_notifications (
    -- One row per outage. Not a foreign key: `outage` is sync output and may
    -- be recreated by a migration there, which would cascade away our send
    -- history and re-announce everything. Same choice sla_clocks makes.
    outage_id       UUID PRIMARY KEY,

    -- What we have actually sent, which is the only thing that decides the
    -- next email.
    phase           outage_notification_phase_enum NOT NULL DEFAULT 'NONE',

    -- When each email went out. Nullable because an outage may never reach
    -- that stage.
    declared_on     TIMESTAMPTZ,
    resolved_on     TIMESTAMPTZ,

    -- The "update" arm can fire repeatedly and writes no phase, so it needs
    -- its own trace. update_count is the honest measure of how noisy this
    -- notifier is for one outage.
    last_update_on  TIMESTAMPTZ,
    update_count    INTEGER NOT NULL DEFAULT 0,

    -- True when `phase` was seeded from ServiceNow's mirrored value rather
    -- than earned by an email this service sent. Keeps a cutover-seeded row
    -- distinguishable from one we genuinely drove, which matters the first
    -- time somebody asks why no declaration email exists for an outage that
    -- is already marked declared.
    seeded_from_sync BOOLEAN NOT NULL DEFAULT FALSE,

    created_on      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_on      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The sweep's only query: outages we have not finished notifying about,
-- oldest first.
CREATE INDEX IF NOT EXISTS idx_outage_notifications_unresolved
    ON outage_notifications (updated_on)
    WHERE phase <> 'RESOLVED';
