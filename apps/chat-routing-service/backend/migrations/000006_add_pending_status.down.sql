-- Postgres can't drop an enum value, so we recreate the type without it.
-- Rows currently PENDING map back to BUSY first so nothing is left
-- pointing at a value that's about to disappear.
UPDATE chat_routing.engineers SET status = 'BUSY' WHERE status = 'PENDING';

ALTER TABLE chat_routing.engineers ALTER COLUMN status DROP DEFAULT;

CREATE TYPE chat_routing.engineer_status_old AS ENUM ('AVAILABLE', 'BUSY', 'OFFLINE');

ALTER TABLE chat_routing.engineers
  ALTER COLUMN status TYPE chat_routing.engineer_status_old
  USING status::text::chat_routing.engineer_status_old;

DROP TYPE chat_routing.engineer_status;
ALTER TYPE chat_routing.engineer_status_old RENAME TO engineer_status;

ALTER TABLE chat_routing.engineers ALTER COLUMN status SET DEFAULT 'OFFLINE';
