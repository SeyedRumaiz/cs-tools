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

"""Client for entity-service's POST /accounts/search -- backs the "Customer
name" autocomplete on the submission form (where == "Customer").

Config (env):
    ENTITY_SERVICE_BASE_URL       entity-service's own base URL (REQUIRED to
                                   enable this feature; absent = the search
                                   endpoint returns an empty list rather than
                                   failing the whole backend, same "unset key
                                   = quietly off" posture main.py already uses
                                   for TIL_CHAT_WEBHOOK_URL).
    ENTITY_SERVICE_TOKEN_URL      Asgardeo token endpoint for the
                                   client_credentials grant below.
    ENTITY_SERVICE_CLIENT_ID
    ENTITY_SERVICE_CLIENT_SECRET  Must be one of entity-service's own
                                   AUTH_INTERNAL_CLIENT_IDS in whichever
                                   environment ENTITY_SERVICE_BASE_URL points
                                   at, or every search call gets a 401.
    ENTITY_SERVICE_DEV_STATIC_TOKEN
                                   LOCAL DEV ONLY. When set, used verbatim as
                                   the x-jwt-assertion value instead of
                                   fetching a real token -- entity-service's
                                   own auth middleware only decodes this
                                   header, never signature-verifies it (see
                                   its internal/auth/middleware.go), so any
                                   JWT-shaped token carrying a client_id in
                                   its AUTH_INTERNAL_CLIENT_IDS allowlist
                                   works. Never set this against a real
                                   deployment -- it exists purely so a local
                                   entity-service (no real Asgardeo secret
                                   available) can still be exercised
                                   end-to-end. Takes priority over the four
                                   vars above when set.
"""
from __future__ import annotations

import os
import time

import httpx

ENTITY_SERVICE_BASE_URL = os.environ.get("ENTITY_SERVICE_BASE_URL", "").rstrip("/")
ENTITY_SERVICE_TOKEN_URL = os.environ.get("ENTITY_SERVICE_TOKEN_URL", "")
ENTITY_SERVICE_CLIENT_ID = os.environ.get("ENTITY_SERVICE_CLIENT_ID", "")
ENTITY_SERVICE_CLIENT_SECRET = os.environ.get("ENTITY_SERVICE_CLIENT_SECRET", "")
ENTITY_SERVICE_DEV_STATIC_TOKEN = os.environ.get("ENTITY_SERVICE_DEV_STATIC_TOKEN", "")

SEARCH_RESULT_LIMIT = 20

# (token, expires_at_monotonic) -- module-level, process-wide. Same shape as
# the PAR Legacy Migration Tool's get_service_account_token (operations/
# peoplehr-data-migration/par/backend/import_job.py), re-fetched a minute
# before real expiry rather than on every call.
_token_cache: dict[str, float | str] = {}


def is_configured() -> bool:
    if ENTITY_SERVICE_DEV_STATIC_TOKEN:
        return bool(ENTITY_SERVICE_BASE_URL)
    return bool(
        ENTITY_SERVICE_BASE_URL
        and ENTITY_SERVICE_TOKEN_URL
        and ENTITY_SERVICE_CLIENT_ID
        and ENTITY_SERVICE_CLIENT_SECRET
    )


async def _get_token(client: httpx.AsyncClient) -> str:
    if ENTITY_SERVICE_DEV_STATIC_TOKEN:
        return ENTITY_SERVICE_DEV_STATIC_TOKEN

    cached_token = _token_cache.get("token")
    cached_expiry = _token_cache.get("expires_at")
    if isinstance(cached_token, str) and isinstance(cached_expiry, float) and time.monotonic() < cached_expiry:
        return cached_token

    resp = await client.post(
        ENTITY_SERVICE_TOKEN_URL,
        data={"grant_type": "client_credentials"},
        auth=(ENTITY_SERVICE_CLIENT_ID, ENTITY_SERVICE_CLIENT_SECRET),
        timeout=10.0,
    )
    resp.raise_for_status()
    payload = resp.json()
    token = payload["access_token"]
    # Refresh a minute early so a request mid-call never races real expiry.
    expires_in = int(payload.get("expires_in", 3600))
    _token_cache["token"] = token
    _token_cache["expires_at"] = time.monotonic() + max(expires_in - 60, 30)
    return token


async def search_customers(query: str) -> list[dict[str, str]]:
    """Returns [{"id", "name"}, ...] for accounts matching query (case-
    insensitive, partial match -- entity-service's own SearchAccountsFilters
    semantics). Empty list, never an error, when this feature isn't
    configured at all (see is_configured) -- the frontend falls back to
    free-text entry in that case, same as before this feature existed."""
    if not is_configured():
        return []

    async with httpx.AsyncClient() as client:
        token = await _get_token(client)
        resp = await client.post(
            f"{ENTITY_SERVICE_BASE_URL}/accounts/search",
            json={
                "pagination": {"limit": SEARCH_RESULT_LIMIT, "offset": 0},
                "filters": {"searchQuery": query},
            },
            headers={"x-jwt-assertion": token},
            timeout=10.0,
        )
        resp.raise_for_status()
        body = resp.json()

    return [{"id": a["id"], "name": a["name"]} for a in body.get("accounts", [])]
