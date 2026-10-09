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

// Inline Markdown: `code`, **bold**, [text](url), bare URLs, *italic*.
const INLINE = /(`[^`\n]+`)|(\*\*[^*\n]+\*\*)|(\[[^\]\n]+\]\([^)\s]+\))|(https?:\/\/[^\s<>()]+[^\s<>().,;:!?'"])|(\*[^*\s][^*\n]*\*)/g;

function link(doc: Document, href: string, label: string): Node {
  if (!/^https?:\/\//i.test(href)) return doc.createTextNode(label);
  const a = doc.createElement("a");
  a.href = href;
  a.textContent = label;
  a.target = "_blank";
  a.rel = "noopener noreferrer";
  return a;
}

function inline(doc: Document, text: string, into: Node): void {
  let last = 0;
  for (const match of text.matchAll(INLINE)) {
    const index = match.index ?? 0;
    if (index > last) into.appendChild(doc.createTextNode(text.slice(last, index)));
    const [whole, code, bold, mdLink, url, italic] = match;
    if (code) {
      const el = doc.createElement("code");
      el.textContent = code.slice(1, -1);
      into.appendChild(el);
    } else if (bold) {
      const el = doc.createElement("strong");
      el.textContent = bold.slice(2, -2);
      into.appendChild(el);
    } else if (mdLink) {
      const split = mdLink.indexOf("](");
      into.appendChild(link(doc, mdLink.slice(split + 2, -1), mdLink.slice(1, split)));
    } else if (url) {
      into.appendChild(link(doc, url, url));
    } else if (italic) {
      const el = doc.createElement("em");
      el.textContent = italic.slice(1, -1);
      into.appendChild(el);
    }
    last = index + whole.length;
  }
  if (last < text.length) into.appendChild(doc.createTextNode(text.slice(last)));
}

/**
 * Renders the small Markdown subset AI answers use as DOM nodes, never by
 * parsing HTML, so a model's output cannot inject markup: paragraphs,
 * headings (as bold lines), bullet and numbered lists, fenced code blocks,
 * inline code, bold, italic and http(s) links.
 */
export function renderMarkdown(text: string, doc: Document = document): DocumentFragment {
  const out = doc.createDocumentFragment();
  const lines = text.replace(/\r\n?/g, "\n").split("\n");
  let paragraph: string[] = [];
  let list: HTMLElement | null = null;

  const flushParagraph = (): void => {
    if (paragraph.length === 0) return;
    const p = doc.createElement("p");
    paragraph.forEach((line, i) => {
      if (i > 0) p.appendChild(doc.createElement("br"));
      inline(doc, line, p);
    });
    out.appendChild(p);
    paragraph = [];
  };
  const endBlocks = (): void => {
    flushParagraph();
    list = null;
  };

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i] ?? "";
    if (/^\s*```/.test(line)) {
      endBlocks();
      const code: string[] = [];
      for (i++; i < lines.length && !/^\s*```/.test(lines[i] ?? ""); i++) code.push(lines[i] ?? "");
      const pre = doc.createElement("pre");
      const el = doc.createElement("code");
      el.textContent = code.join("\n");
      pre.appendChild(el);
      out.appendChild(pre);
      continue;
    }
    if (line.trim() === "") {
      endBlocks();
      continue;
    }
    const heading = /^\s*#{1,6}\s+(.*)$/.exec(line);
    if (heading) {
      endBlocks();
      const p = doc.createElement("p");
      const strong = doc.createElement("strong");
      inline(doc, heading[1] ?? "", strong);
      p.appendChild(strong);
      out.appendChild(p);
      continue;
    }
    const bullet = /^\s*[-*+]\s+(.*)$/.exec(line);
    const numbered = /^\s*\d+[.)]\s+(.*)$/.exec(line);
    if (bullet || numbered) {
      flushParagraph();
      const tag = bullet ? "UL" : "OL";
      if (!list || list.tagName !== tag) {
        list = doc.createElement(tag.toLowerCase());
        out.appendChild(list);
      }
      const li = doc.createElement("li");
      inline(doc, (bullet ?? numbered)?.[1] ?? "", li);
      list.appendChild(li);
      continue;
    }
    list = null;
    paragraph.push(line);
  }
  endBlocks();
  return out;
}
