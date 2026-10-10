// Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License
// at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
// implied.  See the License for the specific language governing
// permissions and limitations under the License.

import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom/vitest";
import { useEffect } from "react";
import { LexicalComposer } from "@lexical/react/LexicalComposer";
import { RichTextPlugin } from "@lexical/react/LexicalRichTextPlugin";
import { ContentEditable } from "@lexical/react/LexicalContentEditable";
import { LexicalErrorBoundary } from "@lexical/react/LexicalErrorBoundary";
import { HistoryPlugin } from "@lexical/react/LexicalHistoryPlugin";
import { OnChangePlugin } from "@lexical/react/LexicalOnChangePlugin";
import { useLexicalComposerContext } from "@lexical/react/LexicalComposerContext";
import { $generateHtmlFromNodes } from "@lexical/html";
import {
  $getRoot,
  $createParagraphNode,
  $createTextNode,
  $createRangeSelection,
  $setSelection,
  $isTextNode,
} from "lexical";
import { CodeNode } from "@lexical/code";
import { ThemeProvider, createTheme } from "@wso2/oxygen-ui";
import { ErrorBannerProvider } from "@context/error-banner/ErrorBannerContext";
import Toolbar from "@components/rich-text-editor/ToolBar";

const initialConfig = {
  namespace: "ToolBarInlineCodeTest",
  nodes: [CodeNode],
  onError: (error: Error) => {
    throw error;
  },
  editable: true,
};

const LINE = "Please restart the server";
const WORD_START = LINE.indexOf("restart");
const WORD_END = WORD_START + "restart".length;

/** Seeds a single paragraph and selects only the word "restart" within it,
 * mirroring the reported repro: select one word, not the whole line. */
const SeedSelectedWordPlugin = () => {
  const [editor] = useLexicalComposerContext();
  useEffect(() => {
    editor.update(() => {
      const root = $getRoot();
      root.clear();
      const paragraph = $createParagraphNode();
      const text = $createTextNode(LINE);
      paragraph.append(text);
      root.append(paragraph);

      if ($isTextNode(text)) {
        const selection = $createRangeSelection();
        selection.anchor.set(text.getKey(), WORD_START, "text");
        selection.focus.set(text.getKey(), WORD_END, "text");
        $setSelection(selection);
      }
    });
  }, [editor]);
  return null;
};

function renderToolbarHarness(onChange: (html: string) => void) {
  return render(
    <ThemeProvider theme={createTheme()}>
      <ErrorBannerProvider>
        <LexicalComposer initialConfig={initialConfig}>
          <RichTextPlugin
            contentEditable={<ContentEditable data-testid="editable" />}
            placeholder={null}
            ErrorBoundary={LexicalErrorBoundary}
          />
          <Toolbar />
          <HistoryPlugin />
          <SeedSelectedWordPlugin />
          <OnChangePlugin
            onChange={(_editorState, editor) => {
              editor.getEditorState().read(() => {
                onChange($generateHtmlFromNodes(editor));
              });
            }}
          />
        </LexicalComposer>
      </ErrorBannerProvider>
    </ThemeProvider>,
  );
}

describe("ToolBar Inline Code", () => {
  it("formats only the selected word as inline code, not the whole line", async () => {
    const onChange = vi.fn();
    renderToolbarHarness(onChange);

    fireEvent.click(screen.getByRole("button", { name: "Inline Code" }));

    await waitFor(() => expect(onChange).toHaveBeenCalled());

    const lastHtml = onChange.mock.calls.at(-1)?.[0] as string;
    const container = document.createElement("div");
    container.innerHTML = lastHtml;
    const paragraphs = container.querySelectorAll("p");
    expect(paragraphs).toHaveLength(1);
    expect(paragraphs[0]?.textContent).toBe(LINE);
    const codeElements = paragraphs[0]?.querySelectorAll("code");
    expect(codeElements).toHaveLength(1);
    expect(codeElements?.[0]?.textContent).toBe("restart");

    // The whole line must NOT have become a <pre> code block -- only the
    // selected word gets wrapped.
    expect(lastHtml).not.toContain("<pre");
  });

  it("does not mark the Code Block button as active when only inline code is applied", async () => {
    renderToolbarHarness(() => {});

    const inlineCodeButton = screen.getByRole("button", {
      name: "Inline Code",
    });
    fireEvent.click(inlineCodeButton);

    await waitFor(() =>
      expect(inlineCodeButton).toHaveAttribute("aria-pressed", "true"),
    );
    expect(
      screen.getByRole("button", { name: "Code Block" }),
    ).toHaveAttribute("aria-pressed", "false");
  });
});
