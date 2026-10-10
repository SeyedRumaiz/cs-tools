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

"""Image uploads for the "What did you learn?" rich-text field.

Real file storage, not inline base64 -- base64 embedded directly in `what`
would blow past WHAT_MAX_LENGTH instantly and bloat til_submissions well
beyond what a TEXT column is for. Files land on local disk under
UPLOAD_DIR, served back out via a static mount in main.py -- the simplest
"real storage" option that doesn't need a new cloud credential grant.

Known limitation: a local disk isn't guaranteed to survive a redeploy on
every hosting platform. Good enough for now; swapping this module's
save_upload() for a real object-storage client later doesn't require
touching main.py or the frontend at all, since both only ever see a URL.

Validation deliberately does not trust the client-supplied Content-Type
header (trivially spoofable) -- it sniffs the first few bytes against each
format's own magic number, the same "never trust a self-reported label"
posture as auth.py re-verifying a token instead of trusting a forwarded
header.
"""
from __future__ import annotations

import os
import uuid
from pathlib import Path

UPLOAD_DIR = Path(os.environ.get("TIL_UPLOAD_DIR", "uploads"))
MAX_UPLOAD_BYTES = 5 * 1024 * 1024  # 5 MB -- generous for a pasted screenshot, not for a video-as-image trick

# (magic bytes, extension, content type) -- checked in order against the
# start of the file. GIF's own magic covers both GIF87a and GIF89a since
# they share the first 4 bytes.
_SIGNATURES: list[tuple[bytes, str, str]] = [
    (b"\x89PNG\r\n\x1a\n", "png", "image/png"),
    (b"\xff\xd8\xff", "jpg", "image/jpeg"),
    (b"GIF8", "gif", "image/gif"),
]


def detect_image_type(head: bytes) -> tuple[str, str] | None:
    """Returns (extension, content_type) if `head` starts with a known image
    signature, else None. `head` only needs the first ~12 bytes."""
    for magic, ext, content_type in _SIGNATURES:
        if head.startswith(magic):
            return ext, content_type
    # WEBP needs its OWN two-part check, not a plain prefix match -- the
    # RIFF container format (bytes 0-3) is shared with other formats that
    # are not images at all (a WAV file also starts with "RIFF"; bytes 4-7
    # are a file-size field that varies per file), so "RIFF" alone
    # previously classified a non-image file as a valid WEBP upload (caught
    # in review). The actual format tag ("WEBP") only appears at bytes 8-11.
    if head.startswith(b"RIFF") and head[8:12] == b"WEBP":
        return "webp", "image/webp"
    return None


def ensure_upload_dir() -> None:
    UPLOAD_DIR.mkdir(parents=True, exist_ok=True)


_READ_CHUNK_SIZE = 64 * 1024


async def read_bounded(file) -> bytes | None:
    """Reads an UploadFile in bounded chunks, stopping as soon as
    MAX_UPLOAD_BYTES is exceeded rather than buffering the whole body first.
    A plain `await file.read()` would read (and, past Starlette's in-memory
    spool threshold, write to a temp file) the ENTIRE upload before the size
    check in main.py ever ran -- a caller presenting an arbitrarily large
    file would force that full read/spool regardless of the 5 MB limit this
    module advertises. Returns None once the running total exceeds the
    limit; the caller that ran over never gets the oversized bytes."""
    chunks: list[bytes] = []
    total = 0
    while True:
        chunk = await file.read(_READ_CHUNK_SIZE)
        if not chunk:
            break
        total += len(chunk)
        if total > MAX_UPLOAD_BYTES:
            return None
        chunks.append(chunk)
    return b"".join(chunks)


def save_upload(data: bytes, ext: str) -> str:
    """Writes `data` to a new UUID-named file under UPLOAD_DIR and returns
    the generated filename (not a full path or URL -- main.py builds the
    public URL, since it's the one that knows the request's own origin)."""
    filename = f"{uuid.uuid4().hex}.{ext}"
    (UPLOAD_DIR / filename).write_bytes(data)
    return filename
