-- Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com). All Rights Reserved.
--
-- This software is the property of WSO2 LLC. and its suppliers, if any.
-- Dissemination of any information or reproduction of any material contained
-- herein in any form is strictly forbidden, unless permitted by WSO2 expressly.
-- You may not alter or remove any copyright or other notice from copies of this content.

DROP TRIGGER IF EXISTS outage_affected_ci_outbox_update ON outage_affected_ci;
DROP TRIGGER IF EXISTS outage_affected_ci_outbox ON outage_affected_ci;
DROP TRIGGER IF EXISTS outage_outbox_insert ON outage;
DROP TRIGGER IF EXISTS outage_outbox ON outage;
DROP FUNCTION IF EXISTS trg_event_outbox_insert();
