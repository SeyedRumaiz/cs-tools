-- Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com). All Rights Reserved.
--
-- This software is the property of WSO2 LLC. and its suppliers, if any.
-- Dissemination of any information or reproduction of any material contained
-- herein in any form is strictly forbidden, unless permitted by WSO2 expressly.
-- You may not alter or remove any copyright or other notice from copies of this content.

-- work_item.subject is the single shared column every work-item-type mapping
-- (incident, problem, change_request, change_task, incident_task,
-- incident_alert_task, problem_task, case, conversation) writes its
-- short_description/u_initial_message into - there's no separate subject
-- column per type to widen.
ALTER TABLE work_item ALTER COLUMN subject TYPE VARCHAR(512);

-- customer_engagement_status_update.subject is unrelated to work_item but
-- shares the same column name and needs the same widening.
ALTER TABLE customer_engagement_status_update ALTER COLUMN subject TYPE VARCHAR(512);
