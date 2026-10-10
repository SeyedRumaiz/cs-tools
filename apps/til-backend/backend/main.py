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

"""FastAPI backend for "Today I Learned" -- a company-wide feed of learnings
from customers, partners, and internal sources.

The One WSO2 /me/til page is the only entry point allowed to create an
entry -- POST /submissions checks the caller's own verified token identity
(aud/client_id/azp) against ONE_WSO2_WEBAPP_CLIENT_ID and rejects anything
else outright. Reading and deleting entries stays open to every signed-in
employee (see list_submissions / delete_submission below).
"""
from __future__ import annotations

import os
from datetime import date

from dotenv import load_dotenv

load_dotenv()

from typing import Optional

from fastapi import Depends, FastAPI, File, Request, Response, UploadFile
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse
from fastapi.staticfiles import StaticFiles

import db
import entity_client
import uploads
from auth import ONE_WSO2_WEBAPP_CLIENT_ID, require_auth
from novera_notify import notify_novera
from sanitize import sanitize_what_html
from validation import TIL_WHERE_OPTIONS, WHAT_MAX_LENGTH, validate_submission_payload

# Comma-separated so a second origin (e.g. a temporary tunnel URL used only
# so a Novera broadcast card's link actually resolves) can be allowed
# alongside the webapp's normal origin, without replacing it.
CORS_ALLOWED_ORIGINS = [
    origin.strip()
    for origin in os.environ.get("CORS_ALLOWED_ORIGIN", "http://localhost:3000").split(",")
    if origin.strip()
]
# Base URL of the One WSO2 webapp itself (NOT this backend) -- used only to
# build each entry's shareable link (.../knowledge-base/{id}) for the Novera
# broadcast card. Optional: absent just means that link is omitted, same
# "unset key = quietly off" posture as the rest of this service.
ONE_WSO2_BASE_URL = os.environ.get("ONE_WSO2_BASE_URL", "").rstrip("/")


def require_webapp_caller(user: dict) -> Optional[JSONResponse]:
    """Shared by POST /submissions and POST /uploads -- both are ways to add
    content to an entry, so both are restricted to the same single entry
    point. Returns a 403 response if the caller isn't the real One WSO2
    webapp, else None. See POST /submissions for the full reasoning on why
    this checks the verified token identity, never a request header."""
    if not ONE_WSO2_WEBAPP_CLIENT_ID or ONE_WSO2_WEBAPP_CLIENT_ID not in user.get("token_identities", set()):
        return JSONResponse(
            status_code=403,
            content={"error": "This action is only available from the One WSO2 webapp."},
        )
    return None


async def lifespan(app: FastAPI):
    db.init_db()
    yield


app = FastAPI(lifespan=lifespan, title="Today I Learned Backend API", version="0.1.0")

# Rejects an oversized request by its own declared Content-Length BEFORE
# FastAPI/Starlette starts parsing the body at all -- uploads.read_bounded()
# only runs once inside the route handler, by which point File(...) has
# already driven Starlette's multipart parser over the whole request.
# A known DoS exists in python-multipart's part-header parsing for exactly
# this "oversized/malicious body reaches the parser" case (see
# requirements.txt's own note on the version pin); checking here closes
# that regardless of which library version is installed. Content-Length
# can be absent or a client can lie via chunked transfer encoding, so this
# is defense in depth, not a substitute for read_bounded()'s own file-level
# check -- it narrows the worst case rather than guaranteeing it away.
_MAX_REQUEST_BYTES = uploads.MAX_UPLOAD_BYTES + 64 * 1024  # headroom for multipart boundaries/field overhead


@app.middleware("http")
async def reject_oversized_requests(request: Request, call_next):
    content_length = request.headers.get("content-length")
    if content_length is not None:
        try:
            if int(content_length) > _MAX_REQUEST_BYTES:
                return JSONResponse(status_code=413, content={"error": "Request body too large."})
        except ValueError:
            pass
    return await call_next(request)


app.add_middleware(
    CORSMiddleware,
    allow_origins=CORS_ALLOWED_ORIGINS,
    allow_methods=["*"],
    allow_headers=["*"],
)

# A raw ASGI middleware that counts actual received bytes (rather than
# trusting Content-Length) was attempted here, to close the gap where
# Content-Length is absent or a lie. Abandoned: interacting with
# BaseHTTPMiddleware's own body-streaming internals (reject_oversized_
# requests above is BaseHTTPMiddleware-based) produced a confirmed
# infinite receive() loop under exactly the oversized-body scenario this
# was meant to protect against -- a worse failure mode than the gap it
# closes. The correct place for a hard body-size cap that can't be
# bypassed by a missing/lying Content-Length is the reverse proxy or
# gateway in front of this service (e.g. Choreo's own request-size
# limit), not application code fighting the ASGI body-streaming layer.
# reject_oversized_requests above remains as defense in depth for the
# honest-Content-Length case; read_bounded() in uploads.py remains the
# file-level backstop.

# Serves whatever POST /uploads has saved -- StaticFiles needs the directory
# to already exist at mount time, so this runs before the mount, not lazily
# inside the upload handler.
uploads.ensure_upload_dir()
app.mount("/uploads", StaticFiles(directory=uploads.UPLOAD_DIR), name="uploads")


@app.get("/user-info")
async def user_info(user: dict = Depends(require_auth)):
    return {"email": user["email"], "displayName": user["name"], "canModerate": user["is_moderator"]}


@app.get("/customers/search")
async def search_customers(
    q: str = "",
    user: dict = Depends(require_auth),  # noqa: ARG001 -- every signed-in employee may search; nothing here is sensitive beyond what the form already shows
):
    """Backs the "Customer name" autocomplete when where == "Customer" --
    proxies entity_client.search_customers so the real customer list is
    typed correctly rather than free-text-guessed. Returns [] (not an
    error) when ENTITY_SERVICE_* isn't configured at all, so the frontend
    can fall back to plain free-text entry exactly as it did before this
    feature existed."""
    return await entity_client.search_customers(q.strip())


@app.get("/submissions")
async def list_submissions(
    limit: int = 100,
    cursor: Optional[str] = None,
    q: Optional[str] = None,
    scope: str = "what",
    dateFrom: Optional[str] = None,
    dateTo: Optional[str] = None,
    mine: bool = False,
    user: dict = Depends(require_auth),
):
    # Raised from 200: real filters now run as a WHERE clause server-side
    # (see db.list_submissions), so a search result set is no longer capped
    # by how many rows happened to fit in one earlier unfiltered page.
    capped_limit = min(max(limit, 1), 1000)
    if scope not in {"what", *db.SEARCH_SCOPE_COLUMNS}:
        return JSONResponse(status_code=400, content={"error": f"Invalid scope: {scope!r}"})
    # Parsed (not just pattern-matched) and rejected outright on failure --
    # db.list_submissions compares these lexicographically against
    # LEFT(created_at, 10), which only sorts correctly for a real,
    # zero-padded ISO date. An unparseable or non-zero-padded value (e.g.
    # "2026-1-5") would silently sort into the wrong position and return
    # wrong results with no error at all (caught in review).
    for label, value in (("dateFrom", dateFrom), ("dateTo", dateTo)):
        if value is not None:
            try:
                date.fromisoformat(value)
            except ValueError:
                return JSONResponse(status_code=400, content={"error": f"Invalid {label}: {value!r}"})
    # "mine" is a boolean flag from the caller, not a caller-supplied email --
    # it always resolves against the VERIFIED token's own email, never
    # anything the request could set directly, same "never trust what the
    # caller merely claims" posture as submittedByEmail on create.
    mine_email = user["email"] if mine else None
    return db.list_submissions(
        limit=capped_limit, cursor=cursor, q=q, scope=scope, date_from=dateFrom, date_to=dateTo, mine_email=mine_email
    )


@app.post("/submissions")
async def create_submission(request: Request, user: dict = Depends(require_auth)):
    rejection = require_webapp_caller(user)
    if rejection:
        return rejection

    try:
        body = await request.json()
    except Exception:
        return JSONResponse(status_code=400, content={"error": "Request body must be valid JSON."})
    error = validate_submission_payload(body)
    if error:
        return JSONResponse(status_code=400, content={"error": error})

    title = body["title"].strip()
    who = body["who"].strip()
    where = body["where"]
    where_detail = body.get("whereDetail")
    where_detail = where_detail.strip() if isinstance(where_detail, str) else None
    # Re-sanitized here even though the frontend editor already does --
    # never trust that a direct API call went through it. See sanitize.py.
    # request.url.netloc (not a configured constant) is this request's OWN
    # host -- this backend serves from a different host per environment, so
    # there's no single fixed value to validate an <img src> against ahead
    # of time; the only host that's always correct is whichever one the
    # caller actually used to reach this same backend's POST /uploads a
    # moment earlier.
    what = sanitize_what_html(body["what"], request_host=request.url.netloc)

    # submittedByEmail always comes from the verified token -- this is the
    # one guarantee that makes "no anonymous entries" actually true
    # regardless of what the free-text "who" field says.
    submitted_by_email = user["email"]

    submission = db.create_submission(
        title=title, who=who, where=where, what=what, submitted_by_email=submitted_by_email, where_detail=where_detail
    )

    # The gate above already guarantees this request came from the real
    # webapp, so Novera's DM broadcast fires unconditionally here -- no
    # further identity check needed at this point.
    entry_url = f"{ONE_WSO2_BASE_URL}/knowledge-base/{submission['id']}" if ONE_WSO2_BASE_URL else None
    await notify_novera(title=title, who=who, where=where, what=what, where_detail=where_detail, entry_url=entry_url)

    return submission


@app.post("/uploads")
async def create_upload(request: Request, file: UploadFile = File(...), user: dict = Depends(require_auth)):
    """Backs the image button and paste-an-image support in the "What did
    you learn?" editor. Stores the file on disk (see uploads.py) and
    returns an absolute URL the editor inserts as an <img src>. Built from
    `request.base_url` rather than a configured constant -- this backend is
    reachable at different hostnames across local dev / Staging / prod, and
    the one guaranteed-correct base is whatever the caller actually used.
    Same webapp-only gate as POST /submissions -- adding an image to an
    entry is just another way of adding content to it."""
    rejection = require_webapp_caller(user)
    if rejection:
        return rejection

    data = await uploads.read_bounded(file)
    if data is None:
        return JSONResponse(status_code=400, content={"error": "Image must be 5 MB or smaller."})

    # Sniffed from the actual bytes, not file.content_type -- a client can
    # set that header to anything regardless of what was actually uploaded.
    detected = uploads.detect_image_type(data[:12])
    if detected is None:
        return JSONResponse(status_code=400, content={"error": "File must be a PNG, JPEG, GIF, or WEBP image."})
    ext, _content_type = detected

    filename = uploads.save_upload(data, ext)
    url = f"{str(request.base_url).rstrip('/')}/uploads/{filename}"
    return {"url": url}


@app.get("/submissions/{submission_id}")
async def get_submission(
    submission_id: str,
    user: dict = Depends(require_auth),  # noqa: ARG001 -- same "every entry, every employee" rule as the list
):
    submission = db.get_submission(submission_id)
    if submission is None:
        return JSONResponse(status_code=404, content={"error": "Entry not found."})
    return submission


@app.delete("/submissions/{submission_id}")
async def delete_submission(submission_id: str, user: dict = Depends(require_auth)):
    existing = db.get_submission(submission_id)
    if existing is None:
        return JSONResponse(status_code=404, content={"error": "Entry not found."})

    # Delete is allowed for a moderator (TIL_MODERATOR_GROUP) OR the entry's
    # own submitter -- checked against the verified submittedByEmail, never
    # the free-text `who` field, for the same reason that field was never
    # trusted for identity in the first place.
    is_owner = user["email"] == existing["submittedByEmail"]
    if not (user["is_moderator"] or is_owner):
        return JSONResponse(status_code=403, content={"error": "Not authorized to delete entries."})

    db.delete_submission(submission_id)
    return Response(status_code=204)


@app.get("/health")
async def health():
    return {"status": "ok"}
