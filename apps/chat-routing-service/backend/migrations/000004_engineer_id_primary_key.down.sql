TRUNCATE chat_routing.engineers CASCADE;

-- FK depends on the engineers_email_key index being dropped below, so it
-- has to be dropped and re-attached to the restored PK(email).
ALTER TABLE chat_routing.customer_engineer_assignments
  DROP CONSTRAINT customer_engineer_assignments_engineer_email_fkey;

ALTER TABLE chat_routing.engineers
  DROP CONSTRAINT engineers_pkey,
  DROP CONSTRAINT engineers_email_key,
  DROP COLUMN engineer_id,
  ADD CONSTRAINT engineers_pkey PRIMARY KEY (email);

ALTER TABLE chat_routing.customer_engineer_assignments
  ADD CONSTRAINT customer_engineer_assignments_engineer_email_fkey
  FOREIGN KEY (engineer_email) REFERENCES chat_routing.engineers (email);
