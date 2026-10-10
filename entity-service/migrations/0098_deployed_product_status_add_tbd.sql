-- Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com). All Rights Reserved.
--
-- This software is the property of WSO2 LLC. and its suppliers, if any.
-- Dissemination of any information or reproduction of any material contained
-- herein in any form is strictly forbidden, unless permitted by WSO2 expressly.
-- You may not alter or remove any copyright or other notice from copies of this content.

-- Kept in its own transaction: ALTER TYPE ... ADD VALUE can't run in the same
-- transaction as a statement that uses the new value. TO_BE_DETERMINED
-- already exists on the sibling deployed_product_lifecycle_stage_enum -
-- ServiceNow uses it for life_cycle_stage_status too.
ALTER TYPE deployed_product_lifecycle_stage_status_enum ADD VALUE IF NOT EXISTS 'TO_BE_DETERMINED';
