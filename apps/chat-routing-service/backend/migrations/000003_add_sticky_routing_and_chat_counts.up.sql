-- Adds sticky customer routing and daily per-engineer chat counts.
-- Escalate now prefers routing a customer back to whoever last handled
-- them (see customer_engineer_assignments below); if that engineer isn't
-- AVAILABLE, or there's no prior assignment, it falls back to whichever
-- AVAILABLE engineer has taken the fewest chats today, ties still broken
-- by available_since.
ALTER TABLE chat_routing.engineers
  ADD COLUMN chats_today       INTEGER NOT NULL DEFAULT 0,

  -- Calendar day chats_today was last incremented on. Compared against
  -- CURRENT_DATE at read/write time instead of a job resetting every row
  -- at midnight -- a stale count from a previous day just reads as 0.
  ADD COLUMN chats_today_date  DATE;

-- One row per customer: which engineer they were most recently assigned
-- to. Updated on every assignment -- sticky match, load-balanced fallback,
-- queue drain, or a decline reassignment all count. Read at the top of
-- Escalate, and ignored if that engineer isn't currently available.
CREATE TABLE chat_routing.customer_engineer_assignments (
  customer_email  TEXT PRIMARY KEY,
  engineer_email  TEXT NOT NULL REFERENCES chat_routing.engineers (email),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_customer_engineer_assignments_engineer
  ON chat_routing.customer_engineer_assignments (engineer_email);
