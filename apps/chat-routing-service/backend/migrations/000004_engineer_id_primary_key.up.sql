-- Switches engineers' primary key from email to engineer_id, the IdP's
-- stable per-account userid claim, not a locally-generated ID. email stays
-- required and unique since most routes still identify an engineer by
-- email; only POST /route/presence (the one place a new row is created)
-- also carries engineerId, since this service has no way to mint that ID
-- itself.
--
-- Presence rows are ephemeral pre-launch state, so instead of backfilling
-- engineer_id for existing rows, this just truncates. CASCADE also clears
-- customer_engineer_assignments, whose FK points at email, so those rows
-- don't dangle once the engineers they reference are gone.
TRUNCATE chat_routing.engineers CASCADE;

-- The FK depends on the PK's backing index specifically, so it has to be
-- dropped before the PK swap and re-attached to the new UNIQUE(email)
-- constraint below -- otherwise dropping the old PK would silently
-- cascade-drop the FK too.
ALTER TABLE chat_routing.customer_engineer_assignments
  DROP CONSTRAINT customer_engineer_assignments_engineer_email_fkey;

ALTER TABLE chat_routing.engineers
  DROP CONSTRAINT engineers_pkey,
  ADD COLUMN engineer_id TEXT NOT NULL,
  ADD CONSTRAINT engineers_pkey PRIMARY KEY (engineer_id),
  ADD CONSTRAINT engineers_email_key UNIQUE (email);

ALTER TABLE chat_routing.customer_engineer_assignments
  ADD CONSTRAINT customer_engineer_assignments_engineer_email_fkey
  FOREIGN KEY (engineer_email) REFERENCES chat_routing.engineers (email);
