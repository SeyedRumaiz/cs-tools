-- Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com). All Rights Reserved.
--
-- This software is the property of WSO2 LLC. and its suppliers, if any.
-- Dissemination of any information or reproduction of any material contained
-- herein in any form is strictly forbidden, unless permitted by WSO2 expressly.
-- You may not alter or remove any copyright or other notice from copies of this content.

-- Kept as the only statement in this file: ALTER TYPE ... ADD VALUE can't run
-- in the same transaction as a statement that uses the new value. PLANNING
-- (priority "5") already exists on the sibling problem_priority_enum/
-- incident_task_priority_enum - incident's own enum was just missed.
ALTER TYPE incident_priority_enum ADD VALUE IF NOT EXISTS 'PLANNING';
