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

"""Integration tests for the submission endpoints, with auth mocked out.

auth.require_auth is overridden via FastAPI's dependency_overrides rather
than mocking the JWKS fetch -- these tests are about main.py's own request
handling (the webapp-only submission gate in particular), not about token
verification, which is auth.py's job and not exercised here.
"""
import os

from conftest import TEST_DB_NAME, bootstrap_test_database

os.environ.setdefault("DB_NAME", TEST_DB_NAME)

from unittest.mock import AsyncMock, patch

import pytest
from fastapi.testclient import TestClient

import db
import main
import uploads
from auth import require_auth

HUMAN_USER = {"email": "jane@example.com", "name": "Jane", "groups": [], "is_moderator": False, "token_identities": {"one-wso2-webapp-real-client-id"}}
OTHER_HUMAN_USER = {"email": "sam@example.com", "name": "Sam", "groups": [], "is_moderator": False, "token_identities": {"one-wso2-webapp-real-client-id"}}
MODERATOR_USER = {"email": "mod@example.com", "name": "Mod", "groups": ["til-mods"], "is_moderator": True, "token_identities": {"one-wso2-webapp-real-client-id"}}
# A human signed in through some OTHER Asgardeo application (Novera's own,
# or anything else) -- same kind of token require_auth accepts (a real,
# valid human token), different client_id. Proves the submission gate
# checks the TOKEN's own verified identity, not just "is this caller
# authenticated at all."
HUMAN_USER_VIA_OTHER_APP = {"email": "jane@example.com", "name": "Jane", "groups": [], "is_moderator": False, "token_identities": {"some-other-apps-client-id"}}


@pytest.fixture(autouse=True)
def mock_novera_notify():
    # CRITICAL: without this, every test that successfully creates a
    # submission calls the REAL notify_novera(), which makes a REAL network
    # call to whatever NOVERA_NOTIFY_URL is set in this process's actual
    # .env -- the real Staging Novera, broadcasting test fixture data
    # ("Jane" / "Internal" / "x") into every connected employee's real Chat
    # DM, once per successful test, every single pytest run. Autouse so
    # this is true for every test in this file by default, not just the
    # ones that happen to remember to patch it themselves -- a test
    # forgetting to mock an outbound network call should never be possible
    # here again.
    with patch("main.notify_novera", new_callable=AsyncMock) as mock:
        yield mock


@pytest.fixture(autouse=True)
def fresh_db():
    # Swaps the already-imported db module's live pool, rather than
    # importlib.reload(db) -- main.py's handlers call db.create_submission
    # etc. by attribute lookup on this same module object at request time,
    # so mutating .pool in place is enough for them to pick up til_test.
    bootstrap_test_database()
    db.pool = db.ConnectionPool(db.DB_HOST, db.DB_PORT, db.DB_USER, db.DB_PASSWORD, TEST_DB_NAME)
    db.init_db()
    with db.pool.get_conn() as conn:
        with conn.cursor() as cur:
            cur.execute("TRUNCATE TABLE til_submissions")
    yield


@pytest.fixture(autouse=True)
def webapp_client_id():
    # Every test below posts as a user whose token_identities contains this
    # value, matching main.py's gate -- the gate's own behavior (what
    # happens when it DOESN'T match, or is unset) is covered explicitly by
    # test_create_submission_rejects_non_webapp_callers and
    # test_create_submission_rejects_everyone_when_client_id_unset below,
    # each of which overrides this patch for its own scope.
    with patch("main.ONE_WSO2_WEBAPP_CLIENT_ID", "one-wso2-webapp-real-client-id"):
        yield


def client_as(user: dict) -> TestClient:
    main.app.dependency_overrides[require_auth] = lambda: user
    return TestClient(main.app)


def test_create_submission_uses_tokens_own_email():
    client = client_as(HUMAN_USER)
    resp = client.post("/submissions", json={"title": "T", "who": "Jane Doe, CSM", "where": "Internal", "what": "Learned X."})
    assert resp.status_code == 200
    assert resp.json()["submittedByEmail"] == "jane@example.com"


def test_list_submissions_rejects_an_invalid_date_filter():
    # Regression test: dateFrom/dateTo used to be passed straight through to
    # a lexicographic string compare with no validation -- a value like
    # "2026-1-5" (not zero-padded) would silently sort into the wrong
    # position and return wrong results with no error at all.
    client = client_as(HUMAN_USER)
    resp = client.get("/submissions", params={"dateFrom": "2026-1-5"})
    assert resp.status_code == 400
    resp = client.get("/submissions", params={"dateTo": "not-a-date"})
    assert resp.status_code == 400
    resp = client.get("/submissions", params={"dateFrom": "2026-01-05", "dateTo": "2026-01-31"})
    assert resp.status_code == 200


def test_create_submission_notifies_novera():
    client = client_as(HUMAN_USER)
    with patch("main.notify_novera", new_callable=AsyncMock) as mock_notify:
        resp = client.post("/submissions", json={"title": "T", "who": "Jane", "where": "Internal", "what": "x"})
        assert resp.status_code == 200
        mock_notify.assert_called_once()


def test_create_submission_rejects_non_webapp_callers():
    # The One WSO2 webapp is the ONLY entry point allowed to create an
    # entry -- checked against the token's own verified client_id (main.py
    # reads user["token_identities"], set by auth.py from the signed JWT's
    # aud/client_id/azp claims), NOT a request header -- any already-
    # authenticated caller could set any header value, so a header proves
    # nothing about which application actually issued the token. This test
    # deliberately sends a spoofed header to prove it's ignored.
    client = client_as(HUMAN_USER_VIA_OTHER_APP)
    with patch("main.notify_novera", new_callable=AsyncMock) as mock_notify:
        resp = client.post(
            "/submissions",
            json={"title": "T", "who": "Jane", "where": "Internal", "what": "x"},
            headers={"X-Til-Client": "one-wso2-webapp"},
        )
        assert resp.status_code == 403
        mock_notify.assert_not_called()


def test_create_submission_rejects_everyone_when_client_id_unset():
    # ONE_WSO2_WEBAPP_CLIENT_ID unset entirely -- nobody can submit, even a
    # token that would otherwise match the webapp's real client id (fail
    # closed, not "trust everyone").
    with patch("main.ONE_WSO2_WEBAPP_CLIENT_ID", ""):
        client = client_as(HUMAN_USER)
        resp = client.post("/submissions", json={"title": "T", "who": "Jane", "where": "Internal", "what": "x"})
        assert resp.status_code == 403


def test_create_submission_sanitizes_what_even_if_client_skips_the_editor():
    client = client_as(HUMAN_USER)
    resp = client.post(
        "/submissions",
        json={"title": "T", "who": "Jane", "where": "Internal", "what": '<p>hi</p><script>alert(1)</script>'},
    )
    assert resp.status_code == 200
    assert resp.json()["what"] == "<p>hi</p>"


def test_create_submission_keeps_img_on_the_request_s_own_host():
    # TestClient's default base_url is http://testserver -- this confirms
    # main.py actually threads request.url.netloc through to
    # sanitize_what_html end-to-end, not just that the function itself
    # behaves correctly in isolation (see test_sanitize.py for that).
    client = client_as(HUMAN_USER)
    src = "http://testserver/uploads/07ec86bdee9142da838c9a3511f780e8.webp"
    resp = client.post(
        "/submissions",
        json={"title": "T", "who": "Jane", "where": "Internal", "what": f'<p>See:</p><img src="{src}">'},
    )
    assert resp.status_code == 200
    assert src in resp.json()["what"]


def test_create_submission_strips_img_on_a_different_host():
    client = client_as(HUMAN_USER)
    evil_src = "https://attacker.example/uploads/07ec86bdee9142da838c9a3511f780e8.webp"
    resp = client.post(
        "/submissions",
        json={"title": "T", "who": "Jane", "where": "Internal", "what": f'<p>See:</p><img src="{evil_src}">'},
    )
    assert resp.status_code == 200
    assert "<img" not in resp.json()["what"]


def test_create_submission_rejects_invalid_payload():
    client = client_as(HUMAN_USER)
    resp = client.post("/submissions", json={"title": "T", "who": "", "where": "Internal", "what": "x"})
    assert resp.status_code == 400


def test_create_submission_requires_where_detail_for_customer():
    client = client_as(HUMAN_USER)
    resp = client.post("/submissions", json={"title": "T", "who": "Jane", "where": "Customer", "what": "x"})
    assert resp.status_code == 400


def test_create_submission_stores_where_detail_for_customer():
    client = client_as(HUMAN_USER)
    resp = client.post(
        "/submissions",
        json={"title": "T", "who": "Jane", "where": "Customer", "whereDetail": "Acme Corp", "what": "x"},
    )
    assert resp.status_code == 200
    assert resp.json()["whereDetail"] == "Acme Corp"


def test_non_owner_non_moderator_cannot_delete():
    created = client_as(HUMAN_USER).post("/submissions", json={"title": "T", "who": "Jane", "where": "Internal", "what": "x"}).json()
    resp = client_as(OTHER_HUMAN_USER).delete(f"/submissions/{created['id']}")
    assert resp.status_code == 403


def test_submitter_can_delete_their_own_entry():
    client = client_as(HUMAN_USER)
    created = client.post("/submissions", json={"title": "T", "who": "Jane", "where": "Internal", "what": "x"}).json()
    resp = client.delete(f"/submissions/{created['id']}")
    assert resp.status_code == 204


def test_moderator_can_delete_someone_elses_entry():
    created = client_as(HUMAN_USER).post("/submissions", json={"title": "T", "who": "Jane", "where": "Internal", "what": "x"}).json()
    resp = client_as(MODERATOR_USER).delete(f"/submissions/{created['id']}")
    assert resp.status_code == 204


def test_delete_nonexistent_is_404_for_a_moderator():
    resp = client_as(MODERATOR_USER).delete("/submissions/does-not-exist")
    assert resp.status_code == 404


def test_delete_nonexistent_is_404_even_for_a_non_moderator():
    # 404 (does this exist at all) takes precedence over 403 (are you
    # allowed) -- you can't be "unauthorized" to delete nothing.
    resp = client_as(HUMAN_USER).delete("/submissions/does-not-exist")
    assert resp.status_code == 404


def test_get_single_submission_returns_it():
    created = client_as(HUMAN_USER).post("/submissions", json={"title": "T", "who": "Jane", "where": "Internal", "what": "x"}).json()
    # Any signed-in employee, not just the moderator or the submitter -- same
    # "every entry, every employee" rule as the list endpoint.
    resp = client_as(MODERATOR_USER).get(f"/submissions/{created['id']}")
    assert resp.status_code == 200
    assert resp.json()["id"] == created["id"]


def test_get_single_submission_404_for_missing():
    resp = client_as(HUMAN_USER).get("/submissions/does-not-exist")
    assert resp.status_code == 404


# Minimal valid PNG signature -- detect_image_type only sniffs the first 8
# bytes, so this is sufficient without needing a structurally complete PNG.
_PNG_HEADER = b"\x89PNG\r\n\x1a\n" + b"\x00" * 16


@pytest.fixture(autouse=True)
def isolated_upload_dir(tmp_path, monkeypatch):
    # Redirects uploads.py's module-level UPLOAD_DIR to a throwaway temp
    # directory for every test in this file -- without this, every passing
    # upload test would leave a real file behind in the project's own
    # uploads/ folder, same "never touch the real one" reasoning as
    # fresh_db's til_test database.
    monkeypatch.setattr(uploads, "UPLOAD_DIR", tmp_path)


def test_upload_accepts_a_valid_png():
    resp = client_as(HUMAN_USER).post("/uploads", files={"file": ("x.png", _PNG_HEADER, "image/png")})
    assert resp.status_code == 200
    assert resp.json()["url"].endswith(".png")


def test_upload_rejects_non_webapp_callers():
    resp = client_as(HUMAN_USER_VIA_OTHER_APP).post("/uploads", files={"file": ("x.png", _PNG_HEADER, "image/png")})
    assert resp.status_code == 403


def test_upload_rejects_file_too_large():
    oversized = b"\x89PNG\r\n\x1a\n" + b"\x00" * (uploads.MAX_UPLOAD_BYTES + 1)
    resp = client_as(HUMAN_USER).post("/uploads", files={"file": ("x.png", oversized, "image/png")})
    assert resp.status_code == 400


def test_upload_rejects_content_that_isnt_actually_an_image():
    # Content-Type header claims an image, but the bytes don't match any
    # known signature -- the backend sniffs the real bytes, never trusts
    # a client-supplied header.
    resp = client_as(HUMAN_USER).post("/uploads", files={"file": ("x.png", b"not a real image", "image/png")})
    assert resp.status_code == 400


def test_upload_accepts_a_valid_webp():
    webp = b"RIFF" + b"\x00\x00\x00\x00" + b"WEBP" + b"\x00" * 8
    resp = client_as(HUMAN_USER).post("/uploads", files={"file": ("x.webp", webp, "image/webp")})
    assert resp.status_code == 200
    assert resp.json()["url"].endswith(".webp")


def test_upload_rejects_a_non_webp_riff_file():
    # A WAV file also starts with "RIFF" (the same generic container
    # format) -- regression test for a real bug where "RIFF" alone was
    # treated as proof of a WEBP image, letting a WAV file (or anything
    # else RIFF-based) through as if it were one.
    wav = b"RIFF" + b"\x00\x00\x00\x00" + b"WAVE" + b"\x00" * 8
    resp = client_as(HUMAN_USER).post("/uploads", files={"file": ("x.webp", wav, "audio/wav")})
    assert resp.status_code == 400


