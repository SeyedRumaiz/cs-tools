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

"""JWT verification against Asgardeo's own JWKS.

Pattern reused from the PAR Legacy Migration Tool's auth.py (operations/
peoplehr-data-migration/par/backend/auth.py in digiops-hr) -- same rigor
(real signature check, issuer check, rejects expired tokens, no unverified
fallback path), just pointed at a different group.

One WSO2's own gateway in front of its backends forwards a gateway-decoded
x-jwt-assertion header for most feature backends (see one-wso2's
docs/conventions.md: "Every request carries the signed-in user's access
token ... The API gateway turns it into x-jwt-assertion for the backend").
This backend still independently re-verifies the signature itself rather
than trusting that header blindly -- same posture the PAR migration tool
took, and explicitly endorsed by one-wso2's own conventions doc:
"Identity claims that backends check (email, groups) are id_token claims."

Config (env):
    ASGARDEO_JWKS_URL     JWKS endpoint whose keys signed the token (REQUIRED).
    ASGARDEO_ISSUER       expected `iss` claim (REQUIRED).
    TIL_MODERATOR_GROUP   REQUIRED. Only users whose groups claim includes this
                          exact value may delete another person's entry.
                          Unset fails closed (503), never "everyone is a
                          moderator" -- same reasoning as the PAR migration
                          tool's ADMIN_LDAP_GROUP.
    GROUPS_CLAIM          claim key carrying the user's groups (default: "groups").
"""
import os
import time

import httpx
from fastapi import HTTPException, Request

ASGARDEO_JWKS_URL = os.environ.get("ASGARDEO_JWKS_URL", "").rstrip("/")
ASGARDEO_ISSUER = os.environ.get("ASGARDEO_ISSUER", "")
TIL_MODERATOR_GROUP = os.environ.get("TIL_MODERATOR_GROUP", "")
GROUPS_CLAIM = os.environ.get("GROUPS_CLAIM", "groups")
# Optional. Comma-separated allowlist of OAuth client ids permitted to call
# this API (checked against the token's `aud` and `client_id`/`azp` claims).
# Unset (default) = any token from the configured issuer is accepted
# regardless of which application it was issued to, same as before this
# check existed -- deliberately opt-in rather than enabled by default, since
# this API is meant to be called by more than one application (the One
# WSO2 webapp's gateway-forwarded token today; a future caller with its own
# separate Asgardeo application tomorrow), and those legitimately carry
# different client ids from the same org.
ASGARDEO_ALLOWED_CLIENT_IDS = {
    c.strip() for c in os.environ.get("ASGARDEO_ALLOWED_CLIENT_IDS", "").split(",") if c.strip()
}
# Optional. The One WSO2 webapp's own Asgardeo client id (a public SPA
# client id, not a secret -- see public/config.js's ONE_WSO2_AUTH_CLIENT_ID
# in that repo). Used by main.py to decide whether to trigger the Novera
# broadcast -- checked against the VERIFIED token's own aud/client_id/azp
# claim, never a request header, since a header is caller-controlled and
# proves nothing about which application actually issued the token. Absent
# = the broadcast never fires (fail closed, not "trust everyone").
ONE_WSO2_WEBAPP_CLIENT_ID = os.environ.get("ONE_WSO2_WEBAPP_CLIENT_ID", "")

_jwks_cache: dict = {}
_jwks_fetched_at: float = 0.0
_JWKS_TTL = 3600  # seconds, matches one-wso2's own "JWKS cache, 1h" note


async def _get_jwks() -> dict:
    global _jwks_cache, _jwks_fetched_at
    now = time.time()
    if _jwks_cache and (now - _jwks_fetched_at) < _JWKS_TTL:
        return _jwks_cache
    async with httpx.AsyncClient() as client:
        resp = await client.get(ASGARDEO_JWKS_URL, timeout=10)
        resp.raise_for_status()
        _jwks_cache = resp.json()
        _jwks_fetched_at = now
        return _jwks_cache


def _is_jwt(token: str) -> bool:
    return len(token.split(".")) == 3


async def _validate_token(token: str) -> dict:
    import json

    from joserfc import jws, jwt
    from joserfc.jwk import KeySet

    key_set = KeySet.import_key_set(await _get_jwks())
    # Asgardeo issues RFC 9068 JWT access tokens with header `"typ": "at+jwt"`,
    # which joserfc's jwt.decode() rejects outright -- it only accepts "JWT"
    # (see joserfc/jwt.py: `if typ and typ != "JWT": raise InvalidTypeError()`).
    # jws.deserialize_compact does the same real signature verification
    # (BadSignatureError on mismatch) without that unrelated typ restriction,
    # so verify at that layer and apply claims validation ourselves.
    verified = jws.deserialize_compact(token, key_set, algorithms=["RS256"])
    claims = json.loads(verified.payload)
    claims_registry = jwt.JWTClaimsRegistry(
        iss={"essential": True, "value": ASGARDEO_ISSUER},
        exp={"essential": True},
    )
    claims_registry.validate(claims)

    # Computed unconditionally (not just when ASGARDEO_ALLOWED_CLIENT_IDS is
    # set) -- require_auth also exposes this so callers can verify WHICH
    # application a token was issued to, not just that it's valid. This is
    # the one thing a request header can never prove: the client_id claim is
    # part of the signed JWT payload itself, so forging a different value
    # would require forging the whole signature.
    aud = claims.get("aud")
    aud_values = aud if isinstance(aud, list) else ([aud] if aud else [])
    client_id = claims.get("client_id") or claims.get("azp")
    token_identities = {str(v) for v in aud_values if v}
    if client_id:
        token_identities.add(str(client_id))
    claims["_token_identities"] = token_identities

    if ASGARDEO_ALLOWED_CLIENT_IDS and not (token_identities & ASGARDEO_ALLOWED_CLIENT_IDS):
        raise ValueError("Token not issued to an allowed client")

    return claims


def _extract_groups(claims: dict) -> list:
    raw = claims.get(GROUPS_CLAIM)
    if not raw:
        return []
    if isinstance(raw, (list, tuple)):
        return [str(g).strip() for g in raw if str(g).strip()]
    if isinstance(raw, str):
        parts = raw.split(",") if "," in raw else [raw]
        return [p.strip() for p in parts if p.strip()]
    return [str(raw).strip()]


def extract_token(request: Request) -> str:
    # x-jwt-assertion (one-wso2's gateway) takes precedence; falls back to a
    # raw Authorization header for local dev, where there is no gateway.
    token = request.headers.get("x-jwt-assertion")
    if token:
        return token
    auth_header = request.headers.get("authorization", "")
    if auth_header.startswith("Bearer "):
        return auth_header[len("Bearer ") :]
    raise HTTPException(status_code=401, detail="Not authenticated")


async def require_auth(request: Request) -> dict:
    """Verifies the caller's token and returns {email, name, groups, is_moderator}."""
    if not ASGARDEO_JWKS_URL or not ASGARDEO_ISSUER:
        raise HTTPException(
            status_code=503,
            detail="Authentication is not configured. Set ASGARDEO_JWKS_URL and ASGARDEO_ISSUER.",
        )
    if not TIL_MODERATOR_GROUP:
        # Fail closed: an unset moderator group must never silently mean
        # "nobody can moderate" turns into "everybody can" by accident.
        raise HTTPException(
            status_code=503,
            detail="Moderation is not configured. Set TIL_MODERATOR_GROUP.",
        )

    token = extract_token(request)
    if not _is_jwt(token):
        raise HTTPException(status_code=401, detail="Invalid token format")

    try:
        claims = await _validate_token(token)
    except (httpx.RequestError, httpx.HTTPStatusError) as exc:
        raise HTTPException(
            status_code=503,
            detail="Authentication service temporarily unavailable. Please try again.",
        ) from exc
    except Exception as exc:
        raise HTTPException(status_code=401, detail="Invalid or expired token") from exc

    email = claims.get("email")
    if not email:
        raise HTTPException(status_code=401, detail="Token missing email claim")

    groups = _extract_groups(claims)
    is_moderator = TIL_MODERATOR_GROUP in groups

    # The verified Asgardeo client (aud/client_id/azp) this token was issued
    # to -- see main.py's is_one_wso2_webapp_request for why this, not a
    # request header, is what decides whether to trigger the Novera broadcast.
    token_identities = claims.get("_token_identities", set())

    # Asgardeo's own access tokens (at least this org's) don't carry a
    # combined "name" claim -- only given_name/family_name separately -- so
    # prefer joining those and fall back to "name" (id_token-style) then email.
    given_name = claims.get("given_name")
    family_name = claims.get("family_name")
    full_name = " ".join(p for p in (given_name, family_name) if p)
    display_name = full_name or claims.get("name") or email

    return {
        "email": email,
        "name": display_name,
        "groups": groups,
        "is_moderator": is_moderator,
        "token_identities": token_identities,
    }
