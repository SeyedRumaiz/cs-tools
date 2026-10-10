-- Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com). All Rights Reserved.
--
-- This software is the property of WSO2 LLC. and its suppliers, if any.
-- Dissemination of any information or reproduction of any material contained
-- herein in any form is strictly forbidden, unless permitted by WSO2 expressly.
-- You may not alter or remove any copyright or other notice from copies of this content.

-- Value sets verified against ServiceNow's Choices table for u_support_timezone/
-- u_support_tier on Account; see CLAUDE.md section 8's enum-column convention.
DO $$ BEGIN
    CREATE TYPE support_timezone_enum AS ENUM ('M_F_ET', 'M_F_GMT', 'M_F_IST', 'T_S_GMT');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE support_tier_enum AS ENUM ('BASIC', 'ENTERPRISE');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

ALTER TABLE account ADD COLUMN IF NOT EXISTS support_timezone support_timezone_enum;
ALTER TABLE account ADD COLUMN IF NOT EXISTS support_tier support_tier_enum;
