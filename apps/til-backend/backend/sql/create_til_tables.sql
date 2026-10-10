-- Prerequisite for til-backend. Run this once against the target database
-- before starting the app -- db.py only ever verifies this table exists
-- (SHOW TABLES LIKE) and fails loudly if it doesn't; it never creates
-- anything itself, so an operator always knows exactly what ran against
-- a real database. Same posture as the PAR Legacy Migration Tool's own
-- sql/create_migration_tables.sql.
--
-- Usage:
--   mysql -h <host> -P <port> -u <user> -p <database> < sql/create_til_tables.sql

CREATE TABLE IF NOT EXISTS til_submissions (
    id                 VARCHAR(36)   NOT NULL PRIMARY KEY,
    -- Added after the table already had rows in some deployments -- NOT
    -- NULL DEFAULT '' rather than nullable, so an existing row reads as "no
    -- title" (empty, falsy) consistently with how a title-less entry is
    -- meant to display, without needing a separate NULL-check anywhere.
    -- Every NEW submission is required to provide one (validation.py);
    -- this default only ever applies to rows that predate this column.
    title              VARCHAR(150)  NOT NULL DEFAULT '',
    who                VARCHAR(200)  NOT NULL,
    where_             VARCHAR(20)   NOT NULL,
    where_detail       VARCHAR(200)  NULL,
    what               TEXT          NOT NULL,
    submitted_by_email VARCHAR(255)  NOT NULL,
    -- ISO 8601 UTC string (e.g. "2026-10-04T09:28:36.606775+00:00"), not a
    -- native DATETIME -- keeps this column a drop-in match for the values
    -- db.py already produces/compares as plain strings (keyset pagination
    -- does `WHERE created_at < %s` on this same string form).
    created_at         VARCHAR(40)   NOT NULL,
    INDEX idx_til_submissions_created_at (created_at DESC),
    -- Backs the "Submitted by (email)" search scope and the "My entries"
    -- tab filter -- both now run as a real WHERE clause in list_submissions,
    -- not a client-side scan of whatever page happened to be fetched.
    INDEX idx_til_submissions_submitted_by_email (submitted_by_email),
    -- Backs filtering by Customer/Partner/Internal/Other, same reasoning.
    INDEX idx_til_submissions_where (where_),
    -- Backs the default "What was learned" search. FULLTEXT rather than a
    -- regular index -- a plain B-tree index can't accelerate a `LIKE
    -- '%word%'` scan at all (the leading wildcard makes it unusable), and
    -- `what` is the one column actually worth searching at real scale.
    -- MySQL's own tokenizer splits on non-alphanumeric characters, so HTML
    -- tags in the stored markup naturally act as word boundaries rather
    -- than polluting the index.
    FULLTEXT INDEX idx_til_submissions_what_fulltext (what)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Migrations for existing deployments (no-op on a fresh install, where
-- every column/index above is already in the CREATE TABLE). CREATE TABLE
-- IF NOT EXISTS is a no-op against a database that already has this table
-- from before `title` and these three indexes existed -- re-running this
-- script against such a database silently applied NOTHING beyond this
-- point without the guarded ALTERs below. Same "MySQL has no ADD COLUMN/
-- INDEX IF NOT EXISTS, so build it conditionally via information_schema"
-- pattern as the Novera po-agent's own db/schema.sql.
SET @add_title := (
    SELECT IF(
        COUNT(*) = 0,
        "ALTER TABLE til_submissions ADD COLUMN title VARCHAR(150) NOT NULL DEFAULT '' AFTER id",
        'SELECT 1'
    )
    FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME   = 'til_submissions'
      AND COLUMN_NAME  = 'title'
);
PREPARE stmt FROM @add_title;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

-- The ADD COLUMN above fills every pre-existing row's title with '' (its
-- DEFAULT) -- the app enforces a non-empty title for every NEW submission
-- going forward, but a historical row from before this column existed
-- would otherwise come back from the API, and render on the feed/entry
-- page, with a silently blank title rather than one that reads as
-- deliberately pre-dating this field. Safe to re-run: a row only ever
-- matches title = '' here because it's an untouched pre-existing row (no
-- path in the app can create a new row with an empty title), so this
-- never overwrites anything the column's own DEFAULT didn't just set.
UPDATE til_submissions SET title = 'Untitled entry' WHERE title = '';

SET @add_submitted_by_email_idx := (
    SELECT IF(
        COUNT(*) = 0,
        'ALTER TABLE til_submissions ADD INDEX idx_til_submissions_submitted_by_email (submitted_by_email)',
        'SELECT 1'
    )
    FROM information_schema.STATISTICS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME   = 'til_submissions'
      AND INDEX_NAME   = 'idx_til_submissions_submitted_by_email'
);
PREPARE stmt FROM @add_submitted_by_email_idx;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @add_where_idx := (
    SELECT IF(
        COUNT(*) = 0,
        'ALTER TABLE til_submissions ADD INDEX idx_til_submissions_where (where_)',
        'SELECT 1'
    )
    FROM information_schema.STATISTICS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME   = 'til_submissions'
      AND INDEX_NAME   = 'idx_til_submissions_where'
);
PREPARE stmt FROM @add_where_idx;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @add_what_fulltext_idx := (
    SELECT IF(
        COUNT(*) = 0,
        'ALTER TABLE til_submissions ADD FULLTEXT INDEX idx_til_submissions_what_fulltext (what)',
        'SELECT 1'
    )
    FROM information_schema.STATISTICS
    WHERE TABLE_SCHEMA = DATABASE()
      AND TABLE_NAME   = 'til_submissions'
      AND INDEX_NAME   = 'idx_til_submissions_what_fulltext'
);
PREPARE stmt FROM @add_what_fulltext_idx;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
