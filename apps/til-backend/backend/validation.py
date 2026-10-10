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

"""Server-side validation for a submission payload.

Deliberately re-checked here even though the One WSO2 frontend validates the
same fields client-side -- per one-wso2's own conventions.md: "Client-side
gating is presentation only. Every endpoint re-checks the caller itself" --
the same principle applies to input shape, not just auth. The Chat App entry
point in particular has no client-side validation of its own to rely on.
"""
from __future__ import annotations

from sanitize import sanitize_what_html, what_plain_text

TIL_WHERE_OPTIONS = ("Customer", "Partner", "Internal", "Other")
WHERE_OPTIONS_REQUIRING_DETAIL = ("Customer", "Partner", "Other")
TITLE_MAX_LENGTH = 100
WHO_MAX_LENGTH = 200
WHERE_DETAIL_MAX_LENGTH = 200
WHAT_MAX_LENGTH = 5000


def validate_submission_payload(body: dict) -> str | None:
    """Returns an error message, or None if the payload is valid."""
    if not isinstance(body, dict):
        return "Request body must be a JSON object."

    title = body.get("title")
    if not isinstance(title, str) or not title.strip():
        return "'title' is required."
    if len(title.strip()) > TITLE_MAX_LENGTH:
        return f"'title' must be {TITLE_MAX_LENGTH} characters or fewer."

    who = body.get("who")
    if not isinstance(who, str) or not who.strip():
        return "'who' is required."
    if len(who.strip()) > WHO_MAX_LENGTH:
        return f"'who' must be {WHO_MAX_LENGTH} characters or fewer."

    where = body.get("where")
    if where not in TIL_WHERE_OPTIONS:
        return f"'where' must be one of: {', '.join(TIL_WHERE_OPTIONS)}."

    if where in WHERE_OPTIONS_REQUIRING_DETAIL:
        where_detail = body.get("whereDetail")
        if not isinstance(where_detail, str) or not where_detail.strip():
            return f"'whereDetail' is required when 'where' is {where}."
        if len(where_detail.strip()) > WHERE_DETAIL_MAX_LENGTH:
            return f"'whereDetail' must be {WHERE_DETAIL_MAX_LENGTH} characters or fewer."

    what = body.get("what")
    if not isinstance(what, str):
        return "'what' is required."
    # Checked on the SANITIZED html, not the raw input -- main.py stores
    # sanitize_what_html(what), not `what` itself, and the two can disagree
    # on emptiness: raw "<script>x</script>" has non-empty plain text ("x",
    # what_plain_text only strips tags, not script/style CONTENT) but
    # sanitizes down to "" (sanitize_what_html drops the whole block). Validating
    # the raw form would let that through as a blank stored entry. `what` is
    # rich-text HTML (TilRichTextField on the frontend) -- an editor with
    # nothing typed still sends "<p><br></p>", not "", so an empty check
    # (and the length limit) must read the PLAIN TEXT, not the markup.
    # Mirrors the frontend's own isEmptyTilHtml/tilPlainTextLength.
    what_text = what_plain_text(sanitize_what_html(what))
    if not what_text:
        return "'what' is required."
    if len(what_text) > WHAT_MAX_LENGTH:
        return f"'what' must be {WHAT_MAX_LENGTH} characters or fewer."

    return None
