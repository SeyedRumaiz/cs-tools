-- Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com). All Rights Reserved.
--
-- This software is the property of WSO2 LLC. and its suppliers, if any.
-- Dissemination of any information or reproduction of any material contained
-- herein in any form is strictly forbidden, unless permitted by WSO2 expressly.
-- You may not alter or remove any copyright or other notice from copies of this content.

-- first_reported_by_task (the task a problem was first reported from) was
-- incorrectly feeding work_item.parent_id, a column meant only for
-- ServiceNow's generic "parent" reference (set uniformly across every
-- work_item type). Gets its own column instead.
ALTER TABLE problem ADD COLUMN IF NOT EXISTS origin_work_item_id UUID REFERENCES work_item(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_problem_origin_work_item_id ON problem (origin_work_item_id);
