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

//
// Exporting a case list to CSV and to PDF.
//
// Read-only: exporting creates nothing on the backend, so unlike most of the
// support suite these tests leave no permanent records and are safe to re-run.
//
// The route is the one a user takes — Support in the side nav, the Outstanding
// Cases card, "View all cases" — rather than a direct URL, because the card's
// footer button is what sets the list's `returnTo` state and the export acts on
// whatever the list is currently showing.
//
// Both formats build the file in the browser (utils/csv.ts, utils/pdf.ts) and
// hand it over through an anchor with a `download` attribute. There is no
// request to wait on: the assertion is the download event itself, which is also
// the only proof the file was actually produced rather than the menu merely
// closing.
//
// The search narrows to a single known case first, so the exported file is
// small and its contents are predictable — an export of the whole list would be
// slow and would assert nothing in particular.
//

import {
  test,
  expect,
  withSession,
  type Download,
  type Page,
  type TestInfo,
} from "../../fixtures/test";
import fs from "node:fs/promises";
import path from "node:path";
import { CasesListPage } from "../../pages/CasesListPage";
import { SupportCenterPage } from "../../pages/SupportCenterPage";
import { CASE_EXPORT_INPUT, PROJECTS } from "../../config/testData";
import { CASES_LIST, SUPPORT_CENTER } from "../../utils/selectors";

withSession(test);

const project = PROJECTS[CASE_EXPORT_INPUT.projectType];
const exportControls = CASES_LIST.export;

test.describe("Export Case", () => {
  // A shell load, a card navigation, a search round trip and a file build.
  test.describe.configure({ timeout: 180_000 });


  /** Where exported files are kept after a run. Beside the other artefacts, so
   * `test-results/` remains the one place to look. */
  const EXPORT_DIR = path.join("test-results", "exports");

  /**
   * Saves a download and attaches it to the HTML report.
   *
   * Playwright deletes downloads when the browser context closes, so a file
   * that is only read during the test leaves nothing behind. Saving it makes
   * the artefact inspectable afterwards, and attaching it puts it in the report
   * — which is also what gets uploaded by CI and emailed by the container.
   *
   * @param testInfo - The running test, for the attachment.
   * @param download - The download to keep.
   * @returns Absolute path to the saved file.
   */
  async function keepExport(
    testInfo: TestInfo,
    download: Download,
  ): Promise<string> {
    const target = path.resolve(EXPORT_DIR, download.suggestedFilename());
    await fs.mkdir(path.dirname(target), { recursive: true });
    await download.saveAs(target);

    await testInfo.attach(download.suggestedFilename(), { path: target });
    return target;
  }

  /**
   * Walks Support → Outstanding Cases → View all cases, then narrows the list
   * to the one case under test.
   *
   * @param page - Test page.
   * @returns The cases list page object, showing the search result.
   */
  async function openFilteredCaseList(
    page: Page,
  ): Promise<CasesListPage> {
    const supportCenter = new SupportCenterPage(page);
    await supportCenter.openViaSideNav(project.id);

    await supportCenter
      .outstandingCasesFooterButton(
        SUPPORT_CENTER.outstandingCases.allCasesButton,
      )
      .click();
    await supportCenter.waitForList();

    const cases = new CasesListPage(page);
    await expect(cases.searchInput()).toBeVisible({ timeout: 60_000 });

    // Search by case number, and wait for the response produced by this very
    // term — reading the list mid-flight would export the unfiltered set.
    const matched = await cases.search(CASE_EXPORT_INPUT.caseNumber);
    expect(
      matched,
      `case ${CASE_EXPORT_INPUT.caseNumber} should exist on ` +
        `${CASE_EXPORT_INPUT.projectType}; update CASE_EXPORT_INPUT if it does not`,
    ).toBeGreaterThan(0);

    // The row must be the one searched for, not merely a row. Without this a
    // stale result left over from a previous render satisfies the check, and
    // the export then runs against whatever the list happens to show — which
    // is exactly the thing the CSV assertion later claims to prove.
    await expect(
      cases.rows().first(),
      `the first result should be ${CASE_EXPORT_INPUT.caseNumber}`,
    ).toContainText(CASE_EXPORT_INPUT.caseNumber, { timeout: 60_000 });

    return cases;
  }

  test("Export to CSV", async ({ page }, testInfo) => {
    test.skip(!project.id, `${CASE_EXPORT_INPUT.projectType} needs a project id.`);

    const cases = await openFilteredCaseList(page);

    await cases.openExportMenu();
    const download = await cases.exportAs(exportControls.csvItem);

    // The suggested filename is built by the app (downloadCaseListCsv), so it
    // proves the CSV path ran rather than the PDF one.
    expect(download.suggestedFilename()).toMatch(
      exportControls.filenamePattern("csv"),
    );

    // And the file has content: an export that hands over an empty file is a
    // failure the filename alone would not catch.
    const saved = await keepExport(testInfo, download);
    const csv = await fs.readFile(saved, "utf8");
    expect(csv.length, "the CSV should not be empty").toBeGreaterThan(0);

    // The searched case must be in it — this is what makes the export "of the
    // current result set" rather than of something else.
    expect(csv).toContain(CASE_EXPORT_INPUT.caseNumber);

    console.log(
      `Exported CSV: ${download.suggestedFilename()} (${csv.length} bytes) ` +
        `→ ${path.relative(process.cwd(), saved)}`,
    );
  });

  test("Export to PDF", async ({ page }, testInfo) => {
    test.skip(!project.id, `${CASE_EXPORT_INPUT.projectType} needs a project id.`);

    const cases = await openFilteredCaseList(page);

    await cases.openExportMenu();
    const download = await cases.exportAs(exportControls.pdfItem);

    expect(download.suggestedFilename()).toMatch(
      exportControls.filenamePattern("pdf"),
    );

    const saved = await keepExport(testInfo, download);

    // A PDF's text is compressed, so its contents are not asserted the way the
    // CSV's are. The magic number is checked instead: it distinguishes a real
    // PDF from an error page or a zero-byte file, which is the failure this can
    // meaningfully catch.
    const bytes = await fs.readFile(saved);
    expect(bytes.length, "the PDF should not be empty").toBeGreaterThan(0);
    expect(
      bytes.subarray(0, 5).toString("latin1"),
      "the file should be a PDF",
    ).toBe("%PDF-");

    console.log(
      `Exported PDF: ${download.suggestedFilename()} (${bytes.length} bytes) ` +
        `→ ${path.relative(process.cwd(), saved)}`,
    );
  });
});
