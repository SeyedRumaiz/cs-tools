DROP TABLE IF EXISTS chat_routing.engineers;
DROP TYPE IF EXISTS chat_routing.engineer_status;
-- Only safe to drop here because down migrations run in reverse order
-- (000003 down, then 000002 down, then this one) -- by this point
-- escalation_queue and customer_engineer_assignments are already gone, so
-- the schema is empty.
DROP SCHEMA IF EXISTS chat_routing;
