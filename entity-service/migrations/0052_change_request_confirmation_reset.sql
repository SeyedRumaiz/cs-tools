-- Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com). All Rights Reserved.
--
-- This software is the property of WSO2 LLC. and its suppliers, if any.
-- Dissemination of any information or reproduction of any material contained
-- herein in any form is strictly forbidden, unless permitted by WSO2 expressly.
-- You may not alter or remove any copyright or other notice from copies of this content.

-- Clears a change request's plan-start-date answer whenever the proposed date
-- itself moves.
--
-- This is the port of the ServiceNow flow "CR change start plan date
-- notifications", whose name is misleading: it sends nothing. Its only action
-- is Update Record clearing u_confirm_customer_updated_date, so that WSO2's
-- previous Agree or Disagree does not stand against a date it never answered.
--
-- WHY A TRIGGER. The original is record-triggered on this very table, and a
-- trigger is its exact native equivalent: it fires for every writer -- the
-- portal, csm-sync-service's loader, a manual fix -- where application code in
-- any one of them would miss the others.
--
-- WHY IT MATTERS, beyond tidiness. csm-flow-service notifies the customer when
-- this column CHANGES. Without the reset, a customer proposing a second date
-- after WSO2 already agreed leaves AGREE standing; WSO2 agreeing again writes
-- the same value, the diff is empty, no outbox row is produced, and the
-- customer is never told. The notice does not fail -- it silently never exists.
--
-- BEFORE, not AFTER: this edits NEW in place, so the row is written once and
-- the outbox trigger (AFTER) sees the final state. Both columns therefore
-- appear in a single outbox diff, which is why cr_plan_date_notice reads a
-- date change as a proposal before it reads a cleared confirmation as an
-- answer -- see that flow's Match.
-- Wrapped in a transaction deliberately, so a psql -f run applying this file
-- alone can't leave the table with no trigger at all mid-way through.
BEGIN;

CREATE OR REPLACE FUNCTION trg_change_request_reset_confirmation() RETURNS trigger AS $$
BEGIN
    IF NEW.customer_updated_on IS DISTINCT FROM OLD.customer_updated_on
       AND NEW.customer_updated_date_confirmation IS NOT DISTINCT FROM OLD.customer_updated_date_confirmation
    THEN
        -- The date moved and nobody answered in the same write, so whatever
        -- answer stood was about the old date.
        NEW.customer_updated_date_confirmation := NULL;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS change_request_reset_confirmation ON change_request;
CREATE TRIGGER change_request_reset_confirmation
    BEFORE UPDATE ON change_request
    FOR EACH ROW EXECUTE FUNCTION trg_change_request_reset_confirmation();

COMMIT;
