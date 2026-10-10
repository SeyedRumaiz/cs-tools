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
import { INCIDENT_CREATE } from "../utils/selectors";

/**
 * Page object for `/operations/incidents/new`. Unlike change requests
 * (Subject-only), the form requires Short description, Category, Channel,
 * Impact, Urgency, Caller, and Service (the backend's
 * `validateCreateIncidentBody` in incidents.go hard-requires all but
 * Channel) — Caller auto-fills to the signed-in user, everything else
 * needs an explicit pick. Subcategory is optional.
 */
export class IncidentCreatePage {
  constructor(private readonly page: Page) {}

  async goto(): Promise<void> {
    await this.page.goto(INCIDENT_CREATE.path);
    await expect(
      this.page.getByRole("heading", { name: INCIDENT_CREATE.heading }),
    ).toBeVisible();
  }

  shortDescriptionField(): Locator {
    return this.page.getByRole("textbox", { name: /^Short description\s*\*?$/ });
  }

  createButton(): Locator {
    return this.page.getByRole("button", { name: "Create incident" });
  }

  /** Opens a MUI Select by its field label and clicks the named option —
   * Category, Subcategory, Channel, Impact, Urgency all use this.
   * Anchored regex, not `exact: true` — all but Subcategory are required
   * fields, and MUI's FormControl appends a required-field marker to the
   * label. Confirmed live it's not a plain " *": the actual separator is
   * U+2009 (thin space), not U+0020, so `\s*` (which covers U+2009) is used
   * rather than a literal space. A loose (non-exact) match isn't safe
   * either — "Category" is a substring of "Subcategory", so it'd match
   * both; the `^...$` anchors rule that out. Options are scoped to the
   * just-opened MUI listbox (`getByRole("listbox")`), never queried
   * page-wide — see `CaseCreatePage.selectOption` for why an unscoped
   * `getByRole("option")` is unsafe on a page that also embeds the rich-text
   * description editor (this page currently has none, but the scoping is
   * kept consistent with every other picker in this suite). */
  async selectOption(label: string, optionLabel: string): Promise<void> {
    await this.requiredSelectLocator(label).click();
    await this.page
      .getByRole("listbox")
      .getByRole("option", { name: optionLabel, exact: true })
      .click();
  }

  /** The same required-field-marker-tolerant locator `selectOption` clicks
   * through — exposed separately for specs that need to open a select and
   * inspect its options without picking one (e.g. asserting Subcategory's
   * options change with Category, rather than driving a full pick). */
  requiredSelectLocator(label: string): Locator {
    const escaped = label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    return this.page.getByRole("combobox", { name: new RegExp(`^${escaped}\\s*\\*?$`) });
  }

  /** Types into the Service async type-ahead and picks the first real
   * result. There's no seeded/known service name in staging to search for,
   * so callers pass a short, broad query (e.g. a single common letter) —
   * mirrors how a user would actually search when they don't know the
   * exact catalog entry. Scoped to the open listbox — see `selectOption`
   * above. */
  async pickService(query: string): Promise<void> {
    const input = this.page.getByRole("combobox", { name: /^Service\s*\*?$/ });
    await input.click();
    await input.fill(query);
    const option = this.page.getByRole("listbox").getByRole("option").first();
    await option.waitFor({ state: "visible", timeout: 10_000 });
    await option.click();
  }

  /** Fills every required field (short description, the classification
   * selects — Subcategory only when given — and a Service pick) and submits. Caller is left
   * alone — it's already auto-filled to the signed-in user. Returns once
   * the app has navigated to the new incident's detail page
   * (`/operations/incidents/:id`). The id segment must not match the literal
   * "new" of this very create route — a bare `[^/]+$` is satisfied by
   * `/operations/incidents/new` itself, which would let this assertion pass
   * instantly on a still-pending (or failed) submit, before the app ever
   * navigates to the created record's real id. */
  async fillRequiredFieldsAndSubmit(opts: {
    shortDescription: string;
    category: string;
    /** Optional — Subcategory isn't required; omitted, it's left unset. */
    subcategory?: string;
    channel: string;
    impact: string;
    urgency: string;
    serviceQuery: string;
  }): Promise<void> {
    await this.shortDescriptionField().fill(opts.shortDescription);
    await this.selectOption("Category", opts.category);
    if (opts.subcategory) await this.selectOption("Subcategory", opts.subcategory);
    await this.selectOption("Channel", opts.channel);
    await this.selectOption("Impact", opts.impact);
    await this.selectOption("Urgency", opts.urgency);
    await this.pickService(opts.serviceQuery);

    await expect(this.createButton()).toBeEnabled();
    await this.createButton().click();
    await expect(this.page).toHaveURL(/\/operations\/incidents\/(?!new(?:[/?#]|$))[^/]+$/, {
      timeout: 15_000,
    });
  }
}
