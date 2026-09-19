DROP TABLE IF EXISTS chat_routing.customer_engineer_assignments;

ALTER TABLE chat_routing.engineers
  DROP COLUMN IF EXISTS chats_today_date,
  DROP COLUMN IF EXISTS chats_today;
