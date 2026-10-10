-- Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com). All Rights Reserved.
--
-- This software is the property of WSO2 LLC. and its suppliers, if any.
-- Dissemination of any information or reproduction of any material contained
-- herein in any form is strictly forbidden, unless permitted by WSO2 expressly.
-- You may not alter or remove any copyright or other notice from copies of this content.

-- The configuration item an incident is raised against (ServiceNow's
-- incident.cmdb_ci).
--
-- ServiceNow's IncidentUtils.createIncident stores the portal's
-- configurationItemId on cmdb_ci. Postgres had no column for it, so the
-- native create rejected the field with a 400 -- and the portal sends it
-- whenever a CI is picked on the create form.
--
-- Deliberately NOT a foreign key, same as service_commitment.cmdb_ci_id
-- (0182) and outage_affected_ci.ci_id (0090): cmdb_ci is the base class every
-- CI type extends, and Postgres mirrors those classes into separate tables,
-- so there is no single one to point at.

ALTER TABLE incident
    ADD COLUMN IF NOT EXISTS cmdb_ci_id UUID;
