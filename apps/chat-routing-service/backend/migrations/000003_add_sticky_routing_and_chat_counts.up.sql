-- Sticky customer routing + daily per-engineer chat counts, layered on top
-- of the plain FIFO assignment 000001/000002 shipped with: Router.Escalate
-- now prefers routing a customer back to whichever engineer last handled
-- them (see customer_engineer_assignments below), and only when that
-- engineer isn't AVAILABLE right now (or there's no prior assignment)
-- falls back to whichever AVAILABLE engineer has taken the fewest chats
-- today -- ties still broken by available_since, exactly as before.
ALTER TABLE chat_routing.engineers
  ADD COLUMN chats_today       INTEGER NOT NULL DEFAULT 0,

  -- The calendar day chats_today was last incremented on. Compared against
  -- CURRENT_DATE (the database's configured timezone) at read and write
  -- time instead of a scheduled job resetting every row at midnight -- an
  -- engineer with a stale count from a previous day is simply treated as 0
  -- the next time anyone looks (see Router.popAvailableEngineer /
  -- Router.assignCaseToEngineer).
  ADD COLUMN chats_today_date  DATE;

-- One row per customer: which engineer they were most recently assigned
-- to. Written every time Router.assignCaseToEngineer hands a case to
-- someone -- sticky match, load-balanced fallback, queue drain, or a
-- Decline reassignment all count, since any of them is "who this customer
-- actually ended up talking to." Read (and, if that engineer isn't
-- currently AVAILABLE and idle, ignored in favor of the fallback ranking)
-- at the top of every Router.Escalate call.
CREATE TABLE chat_routing.customer_engineer_assignments (
  customer_email  TEXT PRIMARY KEY,
  engineer_email  TEXT NOT NULL REFERENCES chat_routing.engineers (email),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_customer_engineer_assignments_engineer
  ON chat_routing.customer_engineer_assignments (engineer_email);
