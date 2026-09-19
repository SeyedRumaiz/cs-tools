DROP TABLE IF EXISTS chat_routing.engineers;
DROP TYPE IF EXISTS chat_routing.engineer_status;
-- Safe to drop the schema here since down migrations run in reverse order,
-- so every other table in it is already gone by this point.
DROP SCHEMA IF EXISTS chat_routing;
