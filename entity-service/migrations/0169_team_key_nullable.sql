-- team.key was made NOT NULL in 0165, backfilled from lower(name) for every
-- existing row. Relaxed back to nullable here -- a new team row can now be
-- created without a key up front, the same way it could before 0165 existed.
ALTER TABLE team ALTER COLUMN key DROP NOT NULL;
