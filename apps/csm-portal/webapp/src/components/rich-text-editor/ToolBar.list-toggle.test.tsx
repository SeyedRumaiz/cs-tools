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
import { ListPlugin } from "@lexical/react/LexicalListPlugin";
import { OnChangePlugin } from "@lexical/react/LexicalOnChangePlugin";
import { useLexicalComposerContext } from "@lexical/react/LexicalComposerContext";
import { $generateHtmlFromNodes } from "@lexical/html";
import { $getRoot, $createParagraphNode, $createTextNode } from "lexical";
import { ListItemNode, ListNode } from "@lexical/list";
import { ThemeProvider, createTheme } from "@wso2/oxygen-ui";
import { ErrorBannerProvider } from "@context/error-banner/ErrorBannerContext";
import Toolbar from "./ToolBar";

const initialConfig = {
  namespace: "ToolBarListToggleTest",
  nodes: [ListNode, ListItemNode],
  onError: (error: Error) => {
    throw error;
  },
  editable: true,
};

/** Seeds a paragraph with text and places the selection inside it, so the
 * list commands (which act on the current selection) have something to
 * operate on. */
const SeedContentPlugin = () => {
  const [editor] = useLexicalComposerContext();
  useEffect(() => {
    editor.update(() => {
      const root = $getRoot();
      root.clear();
      const paragraph = $createParagraphNode();
      paragraph.append($createTextNode("Line one"));
      root.append(paragraph);
      paragraph.selectEnd();
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
          <ListPlugin />
          <SeedContentPlugin />
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

describe("ToolBar bullet/numbered list toggle", () => {
  it("clicking Bullet List again removes the list instead of leaving it applied", async () => {
    const onChange = vi.fn();
    renderToolbarHarness(onChange);

    const bulletButton = screen.getByRole("button", { name: "Bullet List" });
    fireEvent.click(bulletButton);
    await waitFor(() =>
      expect(onChange.mock.calls.at(-1)?.[0]).toContain("<ul"),
    );
    expect(bulletButton).toHaveAttribute("aria-pressed", "true");

    fireEvent.click(bulletButton);
    await waitFor(() =>
      expect(onChange.mock.calls.at(-1)?.[0]).not.toContain("<ul"),
    );
    expect(bulletButton).toHaveAttribute("aria-pressed", "false");
  });

  it("clicking Numbered List again removes the list instead of leaving it applied", async () => {
    const onChange = vi.fn();
    renderToolbarHarness(onChange);

    const numberedButton = screen.getByRole("button", {
      name: "Numbered List",
    });
    fireEvent.click(numberedButton);
    await waitFor(() =>
      expect(onChange.mock.calls.at(-1)?.[0]).toContain("<ol"),
    );
    expect(numberedButton).toHaveAttribute("aria-pressed", "true");

    fireEvent.click(numberedButton);
    await waitFor(() =>
      expect(onChange.mock.calls.at(-1)?.[0]).not.toContain("<ol"),
    );
    expect(numberedButton).toHaveAttribute("aria-pressed", "false");
  });

  it("switching from Bullet List to Numbered List replaces the list type in one click", async () => {
    const onChange = vi.fn();
    renderToolbarHarness(onChange);

    fireEvent.click(screen.getByRole("button", { name: "Bullet List" }));
    await waitFor(() =>
      expect(onChange.mock.calls.at(-1)?.[0]).toContain("<ul"),
    );

    fireEvent.click(screen.getByRole("button", { name: "Numbered List" }));
    await waitFor(() => {
      const lastHtml = onChange.mock.calls.at(-1)?.[0] as string;
      expect(lastHtml).toContain("<ol");
      expect(lastHtml).not.toContain("<ul");
    });
  });
});
