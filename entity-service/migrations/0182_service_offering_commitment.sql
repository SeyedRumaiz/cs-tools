-- Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com). All Rights Reserved.
--
-- This software is the property of WSO2 LLC. and its suppliers, if any.
-- Dissemination of any information or reproduction of any material contained
-- herein in any form is strictly forbidden, unless permitted by WSO2 expressly.
-- You may not alter or remove any copyright or other notice from copies of this content.

-- Same DDL as csm-sync-service's migration 0124 in digiops-cs, carried here so
-- this schema is complete for the availability sweep. Every statement is
-- IF NOT EXISTS, so a database where 0124 already ran is left unchanged.

-- Which service offering (or CI) is held to which commitment.
--
-- THE LAST MISSING PIECE OF THE AVAILABILITY DATA SET. Migration 0084 already
-- mirrors service_availability (212,904 rows of computed uptime, daily since
-- 2022-11-01) and service_commitment carries the targets, but nothing records
-- WHICH OFFERING IS HELD TO WHICH COMMITMENT. Without this join table the
-- mirrored data cannot answer the three questions it exists for:
--
--   * which offerings are in scope at all -- the ServiceNow calculator builds
--     its subject list by querying exactly this table for
--     service_commitment.type = 'availability'
--   * what the target percentage is for a given offering
--   * whether a maintenance window applies -- a SECOND row here, of type
--     maintenance_window, is the only thing that ever shrinks agreed service
--     time
--
-- 146 rows on dev against 152 service offerings.
--
-- A row carries an offering OR a cmdb_ci, never both: ServiceNow enforces that
-- with the "Only Offering or CI" business rule, and the constraint below says
-- the same thing so a bad row cannot be mirrored in silently.
--
-- *** BOTH CONSTRAINTS BELOW ARE VERIFIED AGAINST THE LIVE DATA, NOT ASSUMED.
-- *** Discovery script 48 PASS 1, run 2026-10-02 against wso2sndev:
--   146 rows, every one type=availability
--   rows with BOTH offering and cmdb_ci ...... 0
--   rows with NEITHER ........................ 0
--   duplicate (offering, commitment) pairs ... none
-- A constraint that rejects real production rows would turn this migration
-- into an outage of its own, so it was checked before being written rather
-- than discovered at sync time.
--
-- cmdb_ci is empty on all 146 rows today; the column exists because the v2
-- calculator supports CI subjects and a future commitment could use one.
--
-- sys_domain, sys_domain_path and sys_mod_count are present on the source and
-- populated, and are deliberately NOT carried: zero of the 180+ existing
-- mappings in this repo carry any of them.

CREATE TABLE IF NOT EXISTS service_offering_commitment (
    id UUID PRIMARY KEY,
    created_on TIMESTAMPTZ NOT NULL,
    updated_on TIMESTAMPTZ NOT NULL,
    created_by VARCHAR(255) NOT NULL,
    updated_by VARCHAR(255) NOT NULL,

    service_offering_id UUID REFERENCES service_offering(id) ON DELETE CASCADE,

    -- Deliberately NOT a foreign key, and deliberately nullable, for the same
    -- reason as outage_affected_ci.ci_id: the source column references
    -- cmdb_ci, the base class every CI type extends, and Postgres mirrors
    -- those classes into separate tables so there is no single one to point
    -- at. On dev every row uses service_offering and this stays null, but the
    -- calculator supports CI subjects and the column has to exist for that.
    cmdb_ci_id UUID,

    service_commitment_id UUID REFERENCES service_commitment(id) ON DELETE CASCADE,

    -- Populated on all 146 source rows. Caught by discovery script 48 PASS 1,
    -- which is the only reason it is here: the draft of this migration was
    -- written from the columns the calculator reads and missed it entirely.
    -- It has no role in the arithmetic, but a one-time migration does not get
    -- a second run, so a populated column that is dropped is gone.
    "order" INTEGER,

    -- Exactly one subject. ServiceNow's "Only Offering or CI" rule enforces
    -- this upstream; stating it here means a violation fails the sync loudly
    -- instead of producing a row the availability port would silently skip.
    CONSTRAINT service_offering_commitment_one_subject
        CHECK (num_nonnulls(service_offering_id, cmdb_ci_id) = 1)
);

-- The lookup the calculator does on every run: all commitments for one
-- subject. Also the lookup that finds a subject's maintenance windows.
CREATE INDEX IF NOT EXISTS idx_service_offering_commitment_offering
    ON service_offering_commitment (service_offering_id);
CREATE INDEX IF NOT EXISTS idx_service_offering_commitment_cmdb_ci
    ON service_offering_commitment (cmdb_ci_id);
CREATE INDEX IF NOT EXISTS idx_service_offering_commitment_commitment
    ON service_offering_commitment (service_commitment_id);

-- ServiceNow's "Check For Duplicate Record" business rule rejects a second
-- row for the same pair. Mirrored as a real constraint so a duplicate cannot
-- arrive through the sync either -- two rows would double-count a subject.
CREATE UNIQUE INDEX IF NOT EXISTS uq_service_offering_commitment_offering_pair
    ON service_offering_commitment (service_offering_id, service_commitment_id)
    WHERE service_offering_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_service_offering_commitment_ci_pair
    ON service_offering_commitment (cmdb_ci_id, service_commitment_id)
    WHERE cmdb_ci_id IS NOT NULL;

-- service_availability has carried 212,904 rows since migration 0084 with NO
-- index at all. Every read the Cloud Status Dashboard makes is by subject,
-- period type and period start -- three sequential scans over a six-figure
-- table. Added here because this migration is what makes that table
-- queryable in earnest.
CREATE INDEX IF NOT EXISTS idx_service_availability_offering_type_start
    ON service_availability (service_offering_id, type, start_on DESC);
CREATE INDEX IF NOT EXISTS idx_service_availability_type_start
    ON service_availability (type, start_on DESC);
