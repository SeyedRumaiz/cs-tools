# Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
#
# WSO2 LLC. licenses this file to you under the Apache License,
# Version 2.0 (the "License"); you may not use this file except
# in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing,
# software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
# KIND, either express or implied.  See the License for the
# specific language governing permissions and limitations
# under the License.

"""MySQL access for til-backend.

Mirrors the PAR Legacy Migration Tool's own db.py (operations/peoplehr-
data-migration/par/backend/db.py in digiops-hr) -- same connection-pool
shape, same fail-loud posture: init_db() only ever VERIFIES the
til_submissions table already exists (SHOW TABLES LIKE), it never creates
anything itself. An operator runs sql/create_til_tables.sql once against
the target database before this app is ever started there -- see that
file for the exact command.
"""
from __future__ import annotations

import os
import threading
import uuid
from contextlib import contextmanager
from datetime import datetime, timezone

import pymysql
import pymysql.cursors

DB_HOST = os.environ.get("DB_HOST", "localhost")
DB_PORT = int(os.environ.get("DB_PORT", "3306"))
DB_USER = os.environ.get("DB_USER", "root")
DB_PASSWORD = os.environ.get("DB_PASSWORD")
if DB_PASSWORD is None:
    # No default -- this connects to a real database in any real
    # deployment, so a missing value must fail loudly rather than silently
    # connecting as root with an empty/guessable password.
    raise RuntimeError("DB_PASSWORD is required and has no default. Set it in the environment.")
DB_NAME = os.environ.get("DB_NAME", "til")

POOL_SIZE = 10


class ConnectionPool:
    """Simple thread-safe connection pool for one database. Checks a
    connection out of a list, pings it (reconnecting if the server dropped
    it), and returns it to the pool when done."""

    def __init__(self, host, port, user, password, database):
        self.host = host
        self.port = port
        self.user = user
        self.password = password
        self.database = database
        self._lock = threading.Lock()
        self._connections = []

    def _new_connection(self):
        return pymysql.connect(
            host=self.host,
            port=self.port,
            user=self.user,
            password=self.password,
            database=self.database,
            charset="utf8mb4",
            cursorclass=pymysql.cursors.DictCursor,
            autocommit=True,
            connect_timeout=10,
            read_timeout=30,
            write_timeout=30,
        )

    @contextmanager
    def get_conn(self):
        with self._lock:
            conn = self._connections.pop() if self._connections else None
        if conn is None:
            conn = self._new_connection()
        else:
            try:
                conn.ping(reconnect=True)
            except Exception:
                conn = self._new_connection()
        try:
            yield conn
        finally:
            with self._lock:
                if len(self._connections) < POOL_SIZE:
                    self._connections.append(conn)
                else:
                    conn.close()


pool = ConnectionPool(DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME)


def init_db():
    """Verifies til_submissions already exists -- does NOT create it. See
    sql/create_til_tables.sql and the module docstring above."""
    with pool.get_conn() as conn:
        with conn.cursor() as cur:
            cur.execute("SHOW TABLES LIKE %s", ("til_submissions",))
            if cur.fetchone() is None:
                raise RuntimeError(
                    "Required table `til_submissions` does not exist in database "
                    f"`{DB_NAME}`. Run sql/create_til_tables.sql against this "
                    "database before starting this app."
                )


def create_submission(
    title: str, who: str, where: str, what: str, submitted_by_email: str, where_detail: str | None = None
) -> dict:
    submission_id = str(uuid.uuid4())
    created_at = datetime.now(timezone.utc).isoformat()
    with pool.get_conn() as conn:
        with conn.cursor() as cur:
            cur.execute(
                "INSERT INTO til_submissions "
                "(id, title, who, where_, where_detail, what, submitted_by_email, created_at) "
                "VALUES (%s, %s, %s, %s, %s, %s, %s, %s)",
                (submission_id, title, who, where, where_detail, what, submitted_by_email, created_at),
            )
    return {
        "id": submission_id,
        "title": title,
        "who": who,
        "where": where,
        "whereDetail": where_detail,
        "what": what,
        "submittedByEmail": submitted_by_email,
        "createdAt": created_at,
    }


def _row_to_dict(row: dict) -> dict:
    return {
        "id": row["id"],
        "title": row["title"],
        "who": row["who"],
        "where": row["where_"],
        "whereDetail": row["where_detail"],
        "what": row["what"],
        "submittedByEmail": row["submitted_by_email"],
        "createdAt": row["created_at"],
    }


#  "what" is searched via the FULLTEXT index (idx_til_submissions_what_
# fulltext) in NATURAL LANGUAGE MODE, which can't be accelerated for a query
# shorter than innodb_ft_min_token_size (3 by default -- confirmed against
# this server) -- MySQL's own indexer never tokenizes anything shorter than
# that, so a FULLTEXT MATCH against a 1-2 character query matches nothing
# even when the text is right there. Short queries fall back to a LIKE scan
# instead, which is correct for them regardless of index support.
_FULLTEXT_MIN_QUERY_LEN = 3


def _has_fulltext_eligible_token(query: str) -> bool:
    # The length check is per WORD, not per whole query -- checking
    # len(query) alone let a query like "Go is" (length 5) take the MATCH
    # path even though both of its tokens ("Go", "is") are themselves under
    # innodb_ft_min_token_size and would be ignored by the indexer, so the
    # MATCH silently returned nothing despite "Go" appearing verbatim in the
    # text (caught in review). Any single long-enough token is enough for
    # MATCH to find something; LIKE is used only when every token is too
    # short to ever match via FULLTEXT regardless.
    return any(len(token) >= _FULLTEXT_MIN_QUERY_LEN for token in query.split())


def _escape_like(value: str) -> str:
    # LIKE's own wildcard characters (and its escape character itself) need
    # escaping before a caller-supplied value is wrapped in %...% -- without
    # this, a literal "_" in a search query matches ANY single character
    # (so a bare "_" matches every row) and "%" matches any run of
    # characters, neither of which a user typing those characters expects
    # (caught in review). MySQL's default LIKE escape character is a
    # backslash, so backslash itself is escaped first.
    return value.replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_")


SEARCH_SCOPE_COLUMNS = {
    "title": "title",
    "who": "who",
    "email": "submitted_by_email",
    "whereDetail": "where_detail",
}


def list_submissions(
    limit: int = 100,
    cursor: str | None = None,
    q: str | None = None,
    scope: str = "what",
    date_from: str | None = None,
    date_to: str | None = None,
    mine_email: str | None = None,
) -> dict:
    # Cursor = "<created_at>|<id>" of the last row the caller already has;
    # keyset pagination, newest first. created_at ALONE used to be the whole
    # cursor, but created_at is microsecond-precision text, not a guaranteed-
    # unique key -- two rows landing in the same microsecond would tie, and
    # a strict `created_at < %s` silently drops whichever of them falls on
    # the far side of that boundary. id (the primary key) breaks the tie.
    #
    # Every filter here used to be a client-side JS scan over whatever page
    # happened to already be in the browser -- correct up to exactly
    # `limit` rows, silently wrong past it (a match sitting on page 2 simply
    # never got fetched to search in the first place). Real WHERE clauses
    # here mean a search is always correct regardless of how many rows exist
    # on either side of it.
    conditions: list[str] = []
    params: list[object] = []

    if mine_email:
        conditions.append("submitted_by_email = %s")
        params.append(mine_email)

    # Compared against just the YYYY-MM-DD prefix of the stored ISO string
    # (LEFT(created_at, 10)), not the full timestamp -- created_at is
    # microsecond-precision ("...T14:32:10.123456+00:00"), so a direct
    # string compare against a plain "2026-10-04" would exclude every row
    # from that day except one landing at exactly midnight. Mirrors the
    # frontend's own localDateString-based comparison this replaces.
    if date_from:
        conditions.append("LEFT(created_at, 10) >= %s")
        params.append(date_from)
    if date_to:
        conditions.append("LEFT(created_at, 10) <= %s")
        params.append(date_to)

    query = (q or "").strip()
    if query:
        if scope == "what":
            # Two known, accepted gaps in the MATCH path, neither one worth
            # the cost of a LIKE fallback query on every empty MATCH result:
            # (1) `what` is stored HTML, so MySQL's tokenizer indexes words
            # that appear only inside markup (e.g. "uploads"/"png" from an
            # <img src="/uploads/...png">), so a query for one of those
            # words matches every entry that happens to contain an image --
            # not a security issue, just an occasional surprising match.
            # (2) a query made entirely of InnoDB's default stopwords (e.g.
            # "the") is long enough to pass _has_fulltext_eligible_token but
            # is invisible to the index regardless, so it returns zero rows
            # even when the word appears verbatim -- LIKE would have found
            # it. Flagged in review as worth noting, not fixing now.
            if _has_fulltext_eligible_token(query):
                conditions.append("MATCH(what) AGAINST (%s IN NATURAL LANGUAGE MODE)")
                params.append(query)
            else:
                conditions.append("what LIKE %s")
                params.append(f"%{_escape_like(query)}%")
        else:
            column = SEARCH_SCOPE_COLUMNS.get(scope)
            if column is None:
                raise ValueError(f"Unknown search scope: {scope!r}")
            conditions.append(f"{column} LIKE %s")
            params.append(f"%{_escape_like(query)}%")

    with pool.get_conn() as conn:
        with conn.cursor() as cur:
            if cursor:
                cursor_created_at, _, cursor_id = cursor.rpartition("|")
                conditions.append("(created_at, id) < (%s, %s)")
                params.extend([cursor_created_at, cursor_id])

            where_clause = f"WHERE {' AND '.join(conditions)}" if conditions else ""
            cur.execute(
                f"SELECT * FROM til_submissions {where_clause} "
                "ORDER BY created_at DESC, id DESC LIMIT %s",
                (*params, limit + 1),
            )
            rows = cur.fetchall()

    has_more = len(rows) > limit
    rows = rows[:limit]
    items = [_row_to_dict(r) for r in rows]
    next_cursor = f'{items[-1]["createdAt"]}|{items[-1]["id"]}' if has_more and items else None
    return {"items": items, "nextCursor": next_cursor}


def get_submission(submission_id: str) -> dict | None:
    with pool.get_conn() as conn:
        with conn.cursor() as cur:
            cur.execute("SELECT * FROM til_submissions WHERE id = %s", (submission_id,))
            row = cur.fetchone()
    if row is None:
        return None
    return _row_to_dict(row)


def delete_submission(submission_id: str) -> bool:
    with pool.get_conn() as conn:
        with conn.cursor() as cur:
            cur.execute("DELETE FROM til_submissions WHERE id = %s", (submission_id,))
            return cur.rowcount > 0
