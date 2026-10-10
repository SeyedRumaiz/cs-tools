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

"""Tests against the real local til_test MySQL database (conftest.py)."""
import os

import pytest

from conftest import TEST_DB_NAME, bootstrap_test_database


@pytest.fixture
def db_module():
    os.environ["DB_NAME"] = TEST_DB_NAME
    bootstrap_test_database()
    import importlib

    import db as db_mod

    importlib.reload(db_mod)
    db_mod.init_db()
    with db_mod.pool.get_conn() as conn:
        with conn.cursor() as cur:
            cur.execute("TRUNCATE TABLE til_submissions")
    yield db_mod


def test_create_and_get_submission(db_module):
    created = db_module.create_submission("My title", "Jane", "Internal", "Learned X", "jane@example.com")
    fetched = db_module.get_submission(created["id"])
    assert fetched == created
    assert fetched["title"] == "My title"


def test_where_detail_round_trips(db_module):
    created = db_module.create_submission(
        "T", "Jane", "Customer", "Learned X", "jane@example.com", where_detail="Acme Corp"
    )
    assert created["whereDetail"] == "Acme Corp"
    fetched = db_module.get_submission(created["id"])
    assert fetched["whereDetail"] == "Acme Corp"
    page = db_module.list_submissions(limit=10)
    assert page["items"][0]["whereDetail"] == "Acme Corp"


def test_list_submissions_newest_first(db_module):
    db_module.create_submission("T1", "A", "Customer", "first", "a@example.com")
    db_module.create_submission("T2", "B", "Internal", "second", "b@example.com")
    page = db_module.list_submissions(limit=10)
    assert [item["who"] for item in page["items"]] == ["B", "A"]
    assert page["nextCursor"] is None


def test_list_submissions_respects_limit_and_sets_cursor(db_module):
    for i in range(3):
        db_module.create_submission(f"T{i}", f"Person {i}", "Internal", "x", f"p{i}@example.com")
    page = db_module.list_submissions(limit=2)
    assert len(page["items"]) == 2
    assert page["nextCursor"] is not None


def test_delete_submission_removes_it(db_module):
    created = db_module.create_submission("T", "Jane", "Customer", "x", "jane@example.com")
    assert db_module.delete_submission(created["id"]) is True
    assert db_module.get_submission(created["id"]) is None


def test_delete_nonexistent_submission_returns_false(db_module):
    assert db_module.delete_submission("does-not-exist") is False


def test_mine_email_filters_to_only_that_submitter(db_module):
    db_module.create_submission("T", "Jane", "Internal", "x", "jane@example.com")
    db_module.create_submission("T", "Sam", "Internal", "x", "sam@example.com")
    page = db_module.list_submissions(limit=10, mine_email="jane@example.com")
    assert [item["submittedByEmail"] for item in page["items"]] == ["jane@example.com"]


def test_date_range_filters_by_created_at_prefix(db_module):
    db_module.create_submission("T", "Jane", "Internal", "x", "jane@example.com")
    # Entries created "now" always fall within today's own date range.
    import datetime

    today = datetime.datetime.now(datetime.timezone.utc).date().isoformat()
    page_included = db_module.list_submissions(limit=10, date_from=today, date_to=today)
    assert len(page_included["items"]) == 1
    page_excluded = db_module.list_submissions(limit=10, date_from="2000-01-01", date_to="2000-01-02")
    assert len(page_excluded["items"]) == 0


def test_fulltext_search_matches_a_real_word_in_what(db_module):
    db_module.create_submission("T", "Jane", "Internal", "Connection pooling cut our API latency significantly", "jane@example.com")
    db_module.create_submission("T", "Sam", "Internal", "Something unrelated entirely", "sam@example.com")
    page = db_module.list_submissions(limit=10, q="latency", scope="what")
    assert len(page["items"]) == 1
    assert page["items"][0]["who"] == "Jane"


def test_short_query_falls_back_to_like_instead_of_fulltext(db_module):
    # Below innodb_ft_min_token_size (3) -- FULLTEXT would silently match
    # nothing for this, so the LIKE fallback is what actually finds it.
    db_module.create_submission("T", "Jane", "Internal", "We use Go for this service", "jane@example.com")
    page = db_module.list_submissions(limit=10, q="Go", scope="what")
    assert len(page["items"]) == 1


def test_multi_token_short_query_falls_back_to_like(db_module):
    # Regression test: the FULLTEXT-eligibility check used to look at the
    # WHOLE query's length, not each token's -- "Go is" (length 5) took the
    # MATCH path even though both "Go" and "is" are themselves under
    # innodb_ft_min_token_size, so MATCH silently found nothing despite "Go"
    # appearing verbatim. Every token here is short, so this must still use
    # LIKE and actually find the row.
    db_module.create_submission("T", "Jane", "Internal", "Go is a great language for this service", "jane@example.com")
    page = db_module.list_submissions(limit=10, q="Go is", scope="what")
    assert len(page["items"]) == 1


def test_like_search_escapes_wildcard_characters(db_module):
    # Regression test: "_" and "%" are LIKE wildcards ("_" matches any
    # single character, "%" matches any run) -- a literal "_" in a search
    # query used to act as a wildcard and match every row instead of only
    # rows actually containing a literal underscore.
    db_module.create_submission("T", "Jane", "Internal", "normal text, no special chars", "jane@example.com")
    db_module.create_submission("T", "Sam", "Internal", "has a literal _ underscore", "sam@example.com")
    page = db_module.list_submissions(limit=10, q="_", scope="what")
    assert [item["who"] for item in page["items"]] == ["Sam"]


def test_who_scope_search_uses_like_on_who_column(db_module):
    db_module.create_submission("T", "Jane Doe", "Internal", "x", "jane@example.com")
    db_module.create_submission("T", "Sam Smith", "Internal", "x", "sam@example.com")
    page = db_module.list_submissions(limit=10, q="jane", scope="who")
    assert [item["who"] for item in page["items"]] == ["Jane Doe"]


def test_title_scope_search_uses_like_on_title_column(db_module):
    db_module.create_submission("A great discovery", "Jane", "Internal", "x", "jane@example.com")
    db_module.create_submission("Something else", "Sam", "Internal", "x", "sam@example.com")
    page = db_module.list_submissions(limit=10, q="discovery", scope="title")
    assert len(page["items"]) == 1
    assert page["items"][0]["title"] == "A great discovery"


def test_unknown_scope_raises():
    import db as db_mod

    with pytest.raises(ValueError):
        db_mod.list_submissions(limit=10, q="x", scope="not-a-real-scope")
