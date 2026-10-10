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

import { describe, expect, it } from "vitest";
import {
  renderIncidentNoteHtml,
  withRenderedIncidentNotes,
} from "@features/csm-operations/utils/incidentNoteHtml";
import type { CsmCaseComment } from "@features/csm-cases/types/csmCases";

const creationNote =
  "<p>Incident auto-created from Alert: ALT1</p>" +
  "<table style='border:1px solid #dcdcdc;'><tr><td>description</td><td>a &lt;b&gt; c &amp; d</td></tr></table>";

function encode(html: string): string {
  return html.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

describe("renderIncidentNoteHtml", () => {
  it("decodes an entity-encoded HTML note back to its HTML", () => {
    expect(renderIncidentNoteHtml(encode(creationNote))).toBe(creationNote);
  });

  it("decodes an encoded note inside ServiceNow's [code] wrapper", () => {
    expect(renderIncidentNoteHtml(`[code]${encode(creationNote)}[/code]`)).toBe(`[code]${creationNote}[/code]`);
  });

  it("leaves a note that already is HTML unchanged", () => {
    expect(renderIncidentNoteHtml(creationNote)).toBe(creationNote);
  });

  it("leaves plain text with an encoded angle bracket unchanged", () => {
    const text = "Use &lt;token&gt; in the command";
    expect(renderIncidentNoteHtml(text)).toBe(text);
  });

  it("leaves plain text and empty bodies unchanged", () => {
    expect(renderIncidentNoteHtml("Duplicate alert received.")).toBe("Duplicate alert received.");
    expect(renderIncidentNoteHtml("")).toBe("");
  });

  it("does not create elements from the encoded markup while decoding", () => {
    const script = encode("<p>x</p><img src=x onerror=\"window.__ran=true\">");
    renderIncidentNoteHtml(script);
    expect((window as unknown as { __ran?: boolean }).__ran).toBeUndefined();
  });
});

describe("withRenderedIncidentNotes", () => {
  it("rewrites only the comments whose body needs decoding", () => {
    const plain = { id: "1", bodyHtml: "hello" } as CsmCaseComment;
    const encoded = { id: "2", bodyHtml: encode(creationNote), internal: true } as CsmCaseComment;
    const [first, second] = withRenderedIncidentNotes([plain, encoded]);
    expect(first).toBe(plain);
    expect(second).toEqual({ ...encoded, bodyHtml: creationNote });
  });
});
