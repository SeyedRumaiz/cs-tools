// Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

// Ported from the webapp's identical utility (features/csm-cases/utils/inlineImages.ts) — both
// apps talk to the same csm-portal backend, which embeds inline comment/description images the
// same way regardless of client.

// Shared "img tag with quoted/bare src" grammar for both extraction and
// replacement, so the two stay in sync as the pattern evolves. Capture groups:
// 1=before-src attrs, 2=double-quoted src, 3=single-quoted src, 4=bare src, 5=after-src attrs.
const IMG_TAG_SRC = /<img([^>]*?)\s+src\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))([^>]*)>/gi;

/**
 * A bare attachment-id `src`: an optional single leading slash, then either a
 * canonical hyphenated UUID (`8-4-4-4-12`) or 32 hex characters, optionally
 * followed by `.iix`, and nothing else (no query string, no extra path
 * segments, no scheme, no `//` prefix). Content migrated from the legacy data
 * source carries inline images in this shape (`<img src="/<uuid>">`, with the
 * `.iix` suffix dropped), so it is treated as an attachment reference exactly
 * like the `.iix` form. Kept deliberately exact so ordinary image URLs are
 * never mistaken for attachment references.
 */
const BARE_ATTACHMENT_SRC =
  /^\/?([a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}|[a-f0-9]{32})(?:\.iix)?$/i;

/**
 * Whether an inline `<img>` `src` is an attachment reference that must be
 * resolved through the backend: either a `.iix`-suffixed reference or a bare
 * attachment id as found in migrated content (see {@link BARE_ATTACHMENT_SRC}).
 */
export function isInlineImageRefSrc(src: string): boolean {
  return src.includes(".iix") || BARE_ATTACHMENT_SRC.test(src.trim());
}

/**
 * Extracts the backing attachment id from an inline `<img>` `src` value. The
 * backing data source embeds inline images as `.iix`-suffixed references
 * (e.g. `.../<attachmentId>.iix`); this pulls the id out regardless of
 * whether it appears as a full path or a bare token. Migrated content may
 * instead carry a bare hyphenated UUID (`/<uuid>`); that is normalized to the
 * same 32-character lowercase form so it dedupes with (and shares a query key
 * with) the `.iix` form of the same attachment.
 */
export function extractInlineImageRefId(src: string): string {
  const s = src.trim();
  const fromPath = s.match(/\/([a-f0-9]{32})\.iix(?:\?|#|$)/i);
  if (fromPath) return fromPath[1];
  const bare = s.match(BARE_ATTACHMENT_SRC);
  if (bare && bare[1].includes("-")) {
    return bare[1].replace(/-/g, "").toLowerCase();
  }
  const tail =
    s
      .replace(/\.iix$/i, "")
      .split("/")
      .pop()
      ?.trim() ?? "";
  if (/^[a-f0-9]{32}$/i.test(tail)) return tail;
  return s
    .replace(/^\//, "")
    .replace(/\.iix$/i, "")
    .trim();
}

/**
 * Formats a 32-character ServiceNow sysid (no hyphens) as a canonical UUID
 * (`8-4-4-4-12`) — the shape the backend's `/attachments/{id}/content`
 * endpoint requires. Ids already in another shape are returned unchanged.
 */
export function sysidToUuid(id: string): string {
  if (!/^[a-f0-9]{32}$/i.test(id)) return id;
  return `${id.slice(0, 8)}-${id.slice(8, 12)}-${id.slice(12, 16)}-${id.slice(16, 20)}-${id.slice(20, 32)}`;
}

// A comment/description with an unreasonable number of distinct inline images would otherwise
// fire one parallel authenticated Blob fetch per image (see useResolvedInlineImageHtml) — this
// caps how many unique ids are ever collected, so that stays bounded regardless of how much HTML
// is thrown at it. Ids past the cap are simply never extracted, so replaceInlineImageSrcs treats
// them the same as any other unresolved reference (stripped, not left pointing at an auth-gated
// URL the WebView can't load).
const MAX_INLINE_IMAGES = 20;

/**
 * Extracts every attachment id referenced by a `.iix` or bare-id `<img>` src
 * (see {@link isInlineImageRefSrc}) within an HTML string.
 */
export function extractIixAttachmentIds(html: string): string[] {
  const ids = new Set<string>();
  let match;
  IMG_TAG_SRC.lastIndex = 0;
  while (ids.size < MAX_INLINE_IMAGES && (match = IMG_TAG_SRC.exec(html)) !== null) {
    const src = match[2] ?? match[3] ?? match[4] ?? "";
    if (isInlineImageRefSrc(src)) {
      const id = extractInlineImageRefId(src);
      if (id) ids.add(id);
    }
  }
  return Array.from(ids);
}

/**
 * Replaces every `.iix` or bare-id (migrated content) `<img>` src in `html` with its resolved
 * data URL from `dataUrls`. A reference with no matching entry is stripped (rather
 * than left pointing at an auth-gated URL the browser cannot fetch).
 */
export function replaceInlineImageSrcs(html: string, dataUrls: Map<string, string>): string {
  return html.replace(IMG_TAG_SRC, (fullMatch, before, doubleSrc, singleSrc, bareSrc, after) => {
    const src = (doubleSrc ?? singleSrc ?? bareSrc ?? "") as string;
    if (!isInlineImageRefSrc(src)) return fullMatch;
    const refId = extractInlineImageRefId(src);
    const dataUrl = dataUrls.get(refId);
    const quote = doubleSrc !== undefined ? '"' : singleSrc !== undefined ? "'" : '"';
    if (!dataUrl) {
      return `<img${before} src=${quote}${quote} data-unresolved="true"${after}>`;
    }
    return `<img${before} src=${quote}${dataUrl}${quote}${after}>`;
  });
}
