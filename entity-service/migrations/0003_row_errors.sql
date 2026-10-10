-- Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com). All Rights Reserved.
--
-- This software is the property of WSO2 LLC. and its suppliers, if any.
-- Dissemination of any information or reproduction of any material contained
-- herein in any form is strictly forbidden, unless permitted by WSO2 expressly.
-- You may not alter or remove any copyright or other notice from copies of this content.

CREATE TABLE IF NOT EXISTS migration_row_error (
    id BIGSERIAL PRIMARY KEY,
    job_id BIGINT REFERENCES migration_job(id),
    table_name TEXT NOT NULL,
    source_sys_id TEXT,
    stage TEXT NOT NULL,        -- 'transform' | 'load'
    error_message TEXT NOT NULL,
    raw_record JSONB,
    severity TEXT NOT NULL DEFAULT 'error',  -- 'error' (row rejected) | 'warning' (row loaded, degraded)
    failed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_row_errors_job ON migration_row_error (job_id);
