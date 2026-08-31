DROP TABLE IF EXISTS customer_engineer_assignments;

ALTER TABLE engineers
  DROP COLUMN IF EXISTS chats_today_date,
  DROP COLUMN IF EXISTS chats_today;
