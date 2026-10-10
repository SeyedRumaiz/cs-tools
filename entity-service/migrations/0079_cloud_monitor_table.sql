-- Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com). All Rights Reserved.
--
-- This software is the property of WSO2 LLC. and its suppliers, if any.
-- Dissemination of any information or reproduction of any material contained
-- herein in any form is strictly forbidden, unless permitted by WSO2 expressly.
-- You may not alter or remove any copyright or other notice from copies of this content.

DO $$ BEGIN
    CREATE TYPE cloud_monitor_cloud_offering_enum AS ENUM (
        'ASGARDEO', 'BIJIRA', 'AGENT_MANAGER', 'CHOREO_EU', 'MOESIF', 'DEVANT', 'CHOREO'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE cloud_monitor_status_enum AS ENUM (
        'PARTIAL_OUTAGE', 'MAINTENANCE', 'DEGRADED', 'OPERATIONAL', 'MAJOR_OUTAGE'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

CREATE TABLE IF NOT EXISTS cloud_monitor (
    id UUID PRIMARY KEY,
    created_on TIMESTAMPTZ NOT NULL,
    updated_on TIMESTAMPTZ NOT NULL,
    created_by VARCHAR(255) NOT NULL,
    updated_by VARCHAR(255) NOT NULL,
    name VARCHAR(255),
    "group" VARCHAR(255),
    group_priority INTEGER,
    description TEXT,
    service_offering_id UUID REFERENCES service_offering(id) ON DELETE SET NULL,
    service_id UUID REFERENCES service(id) ON DELETE SET NULL,
    cloud_offering cloud_monitor_cloud_offering_enum,
    region VARCHAR(100),
    status cloud_monitor_status_enum,
    is_active BOOLEAN
);
