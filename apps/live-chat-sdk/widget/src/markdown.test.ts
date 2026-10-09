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
import { renderMarkdown } from "./markdown.js";

function html(text: string): string {
  const div = document.createElement("div");
  div.append(renderMarkdown(text));
  return div.innerHTML;
}

describe("renderMarkdown", () => {
  it("renders paragraphs, lists, headings and code", () => {
    expect(html("# Steps\n\n1. Open **Settings**\n2. Run `deploy`\n\n- one\n- two\n\nDone.\nNext line.")).toBe(
      "<p><strong>Steps</strong></p><ol><li>Open <strong>Settings</strong></li><li>Run <code>deploy</code></li></ol>" +
        "<ul><li>one</li><li>two</li></ul><p>Done.<br>Next line.</p>",
    );
    expect(html("```\n<b>raw</b>\n```")).toBe("<pre><code>&lt;b&gt;raw&lt;/b&gt;</code></pre>");
  });

  it("links only http(s) addresses, opening them safely", () => {
    expect(html("[Docs](https://wso2.com/docs) and https://example.com/a.")).toBe(
      '<p><a href="https://wso2.com/docs" target="_blank" rel="noopener noreferrer">Docs</a> and ' +
        '<a href="https://example.com/a" target="_blank" rel="noopener noreferrer">https://example.com/a</a>.</p>',
    );
    expect(html("[click](javascript:alert(1))")).not.toContain("<a");
  });

  it("never turns text into markup", () => {
    expect(html('<img src=x onerror="alert(1)"> **<script>x</script>**')).toBe(
      '<p>&lt;img src=x onerror="alert(1)"&gt; <strong>&lt;script&gt;x&lt;/script&gt;</strong></p>',
    );
  });
});
