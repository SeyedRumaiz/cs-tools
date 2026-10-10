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

import type { CsmCaseComment } from "@features/csm-cases/types/csmCases";
import { looksLikeHtml } from "@utils/sanitizeHtml";

// A detached textarea parses its content as text, so entities decode once and tags never become elements.
function decodeEntities(text: string): string {
  const textarea = document.createElement("textarea");
  textarea.innerHTML = text;
  return textarea.value;
}

// ServiceNow can return an HTML note entity-encoded, so a body with no markup that decodes to markup is rendered decoded.
export function renderIncidentNoteHtml(body: string): string {
  if (!body || looksLikeHtml(body) || !body.includes("&lt;")) return body;
  const decoded = decodeEntities(body);
  return looksLikeHtml(decoded) ? decoded : body;
}

// Applies renderIncidentNoteHtml to every comment of an incident, keeping unchanged comments as the same objects.
export function withRenderedIncidentNotes(comments: CsmCaseComment[]): CsmCaseComment[] {
  return comments.map((comment) => {
    const bodyHtml = renderIncidentNoteHtml(comment.bodyHtml ?? "");
    return bodyHtml === comment.bodyHtml ? comment : { ...comment, bodyHtml };
  });
}
