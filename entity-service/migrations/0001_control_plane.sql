-- Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com). All Rights Reserved.
--
-- This software is the property of WSO2 LLC. and its suppliers, if any.
-- Dissemination of any information or reproduction of any material contained
-- herein in any form is strictly forbidden, unless permitted by WSO2 expressly.
-- You may not alter or remove any copyright or other notice from copies of this content.

CREATE TABLE IF NOT EXISTS migration_job (
    id BIGSERIAL PRIMARY KEY,
    job_type TEXT NOT NULL,
    table_name TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'pending',
    attempts INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 5,
    run_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_at TIMESTAMPTZ,
    locked_by TEXT,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_migration_job_pending ON migration_job (run_at) WHERE status = 'pending';

CREATE TABLE IF NOT EXISTS migration_run (
    id BIGSERIAL PRIMARY KEY,
    job_id BIGINT REFERENCES migration_job(id),
    table_name TEXT NOT NULL,
    records_processed INT NOT NULL DEFAULT 0,
    records_failed INT NOT NULL DEFAULT 0,
    last_extracted_id TEXT,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    error TEXT
);

CREATE TABLE IF NOT EXISTS sync_checkpoint (
    table_name TEXT PRIMARY KEY,
    last_sync_value TIMESTAMPTZ NOT NULL,
    last_sync_sys_id TEXT,
    sync_interval_seconds INT NOT NULL DEFAULT 300,
    last_sync_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS schema_version (
    table_name TEXT PRIMARY KEY,
    mapping_hash TEXT NOT NULL,
    mapping_version INT NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
