-- Runs last in a full down (golang-migrate applies downs in reverse order),
-- once every later migration's own down has dropped its tables and types,
-- so the schema is empty by the time this fires.
DROP SCHEMA IF EXISTS chat_routing;
