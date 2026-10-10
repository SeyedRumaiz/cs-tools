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

import { type Locator, type Page, expect } from "@playwright/test";

/**
 * Page object for `/operations/incidents/:id` (`CsmIncidentDetailPage.tsx`).
 * The "Edit" button opens `EditIncidentDialog.tsx`, whose only genuinely
 * required inputs are the ones already on the incident (`canSubmit` in the
 * dialog just needs at least one field to actually change) — fields below
 * are optional per-call.
 */
export class IncidentDetailPage {
  constructor(private readonly page: Page) {}

  /**
   * A freshly-created incident isn't always retrievable the instant we
   * navigate to it — the real DEV-SN backend can lag between the create
   * write and the record becoming readable. Retry the navigation (full
   * reload) until the stable "Back to incidents" button actually renders,
   * rather than failing on the first attempt.
   */
  async goto(id: string): Promise<void> {
    await expect(async () => {
      await this.page.goto(`/operations/incidents/${id}`);
      await expect(this.backButton()).toBeVisible({ timeout: 3_000 });
    }).toPass({ timeout: 45_000, intervals: [1_000, 2_000, 3_000, 5_000] });
  }

  backButton(): Locator {
    return this.page.getByRole("button", { name: "Back to incidents" });
  }

  editButton(): Locator {
    return this.page.getByRole("button", { name: "Edit", exact: true });
  }

  async openEditDialog(): Promise<void> {
    await this.editButton().click();
    await expect(
      this.page.getByRole("dialog").getByRole("heading", { name: "Edit incident" }),
    ).toBeVisible();
  }

  editDialog(): Locator {
    return this.page.getByRole("dialog");
  }

  subjectField(): Locator {
    return this.editDialog().getByRole("textbox", { name: /^Subject\s*\*?$/ });
  }

  resolutionCodeField(): Locator {
    return this.editDialog().getByRole("textbox", { name: /^Resolution code\s*\*?$/ });
  }

  resolutionNotesField(): Locator {
    return this.editDialog().getByRole("textbox", { name: /^Resolution notes\s*\*?$/ });
  }

  /** Opens a fixed-enum Select inside the edit dialog by its field label
   * (Category, Subcategory, Channel, Impact, Urgency) and picks the
   * named option. Options are scoped to the just-opened MUI listbox
   * (`getByRole("listbox")`), never queried page-wide — the dialog may also
   * carry rich-text editor fields (e.g. Additional comments), whose "Font
   * variant" `<select>` renders native `<option>` elements that are always
   * present in the DOM and would otherwise match an unscoped
   * `getByRole("option")`; a native `<select>` has no `role="listbox"`
   * ancestor, so scoping through the open listbox excludes them entirely. */
  async selectOption(label: string, optionLabel: string): Promise<void> {
    const escaped = label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    await this.editDialog()
      .getByRole("combobox", { name: new RegExp(`^${escaped}\\s*\\*?$`) })
      .click();
    await this.page
      .getByRole("listbox")
      .getByRole("option", { name: optionLabel, exact: true })
      .click();
  }

  /** Async search-and-pick field inside the edit dialog (Service, Service
   * offering, Configuration item, Assignment group, Assigned to) — types
   * `query` and picks the first result, scoped to the open listbox — see
   * `selectOption` above. */
  async pickAsyncField(label: string, query: string): Promise<void> {
    const escaped = label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    const input = this.editDialog().getByRole("combobox", { name: new RegExp(`^${escaped}\\s*\\*?$`) });
    await input.click();
    await input.fill(query);
    const option = this.page.getByRole("listbox").getByRole("option").first();
    await option.waitFor({ state: "visible", timeout: 10_000 });
    await option.click();
  }

  additionalCommentsField(): Locator {
    return this.editDialog().getByRole("textbox", { name: /^Additional comments\s*\*?$/ });
  }

  workNoteField(): Locator {
    return this.editDialog().getByRole("textbox", { name: /^Internal work note\s*\*?$/ });
  }

  saveButton(): Locator {
    return this.editDialog().getByRole("button", { name: /^(Save|Saving…)$/ });
  }

  async saveEdit(): Promise<void> {
    await this.saveButton().click();
  }

  // ── Comments ─────────────────────────────────────────────────────────────

  async openComposer(): Promise<void> {
    const opener = this.page.getByRole("button", { name: "Add a comment…" });
    if (await opener.isVisible().catch(() => false)) await opener.click();
  }

  internalNoteSwitch(): Locator {
    return this.page.getByRole("switch", { name: "Internal note" });
  }

  commentEditor(): Locator {
    return this.page.getByTestId("case-description-editor");
  }

  commentSubmitButton(): Locator {
    return this.page.getByRole("button", { name: /Send to customer|Save work note/ });
  }

  async addComment(text: string, opts: { internal?: boolean } = {}): Promise<void> {
    await this.openComposer();
    const wantInternal = !!opts.internal;
    const isChecked = await this.internalNoteSwitch().isChecked();
    if (isChecked !== wantInternal) await this.internalNoteSwitch().click();
    await this.commentEditor().click();
    await this.commentEditor().fill(text);
    await this.commentSubmitButton().click();
  }

  // ── Attachments ──────────────────────────────────────────────────────────

  async uploadAttachment(filePath: string): Promise<void> {
    await this.page.locator('input[type="file"]').first().setInputFiles(filePath);
  }

  async downloadAttachment(filename: string): Promise<void> {
    await this.page.getByRole("button", { name: `Download ${filename}` }).click();
  }
}
