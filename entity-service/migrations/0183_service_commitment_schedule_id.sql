-- Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com). All Rights Reserved.
--
-- This software is the property of WSO2 LLC. and its suppliers, if any.
-- Dissemination of any information or reproduction of any material contained
-- herein in any form is strictly forbidden, unless permitted by WSO2 expressly.
-- You may not alter or remove any copyright or other notice from copies of this content.

-- Which schedule a service commitment is measured against.
--
-- The availability calculation (cs-tools entity-service) needs the schedule a
-- commitment answers to: an availability target applies only inside its
-- schedule's hours. service_commitment.schedule (0082) cannot provide that --
-- it is mapped from `schedule.calendar_name`, a text field that is empty on
-- every commitment on staging, and even when populated a name cannot be joined
-- to schedule rows. This adds the reference itself.
--
-- A real foreign key: cmn_schedule is mirrored in full (no source_filter, 929
-- rows), so every schedule a commitment can point at exists in `schedule`.
-- ON DELETE SET NULL rather than CASCADE: a deleted schedule must not delete
-- the commitment. The calculator treats a commitment with no schedule as
-- unrestricted (24x7), which is ServiceNow's own reading of an empty schedule.
--
-- The existing text `schedule` column is left as it is; nothing reads it.

ALTER TABLE service_commitment
    ADD COLUMN IF NOT EXISTS schedule_id UUID REFERENCES schedule(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_service_commitment_schedule_id
    ON service_commitment (schedule_id);
