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
// Knowledge Base recommendations on a case raised from a Novera conversation.
//
// The whole chain is the point: articles are recommended by the backend from
// the case's title and description, and a case raised through the assistant
// carries the question as its description. So the only reliable way to reach a
// case WITH recommendations is to go through the assistant — which is why this
// enables Novera, holds a conversation, and raises the case from it rather than
// opening an existing one.
//
// ⚠️ Creates a permanent case — POST /cases has no delete counterpart — and
// leaves the assistant enabled for the duration, which changes what Get Help
// does for every other spec. The restore runs from `finally`.
//
// The assistant's reply is waited for, not just its acknowledgement: the case's
// description is built from the exchange, and raising it mid-reply produces a
// thin case that the backend has little to recommend against.
//

import { test, expect, withSession } from "../../fixtures/test";
import { NoveraChatPage } from "../../pages/NoveraChatPage";
import { CaseCreatePage } from "../../pages/CaseCreatePage";
import { CaseDetailPage } from "../../pages/CaseDetailPage";
import { SettingsPage } from "../../pages/SettingsPage";
import { PROJECTS, KNOWLEDGE_BASE_INPUT } from "../../config/testData";
import {
  CASE_DETAIL,
  CASE_KNOWLEDGE_BASE,
  CREATE_CASE,
  NOVERA_CHAT,
  SETTINGS,
} from "../../utils/selectors";
import {
  NOVERA_REPLY_SETTLE_MS,
  setNoveraViaApi,
} from "../../utils/noveraFlows";
import { isSuccess } from "../../utils/caseFlows";
import {
  permanentWriteSkipReason,
  permanentWritesAllowed,
} from "../../utils/permanentWrites";

withSession(test);

const project = PROJECTS[KNOWLEDGE_BASE_INPUT.projectType];

test.describe("Knowledge Base", () => {
  // Enabling the assistant, a chat round trip, the assistant's reply, a case
  // creation and the recommendation fetch — well past the 30s default.
  test.describe.configure({ timeout: 420_000 });

  // Whether THIS test switched the assistant on. Restoring is conditional on
  // it, because the flag is shared project state: the Subscription project
  // normally has Novera already enabled, so an unconditional "off" would turn
  // off a setting this test never turned on — and afterEach runs for SKIPPED
  // tests too, which would disable it without the test having done anything at
  // all.
  let enabledByThisTest = false;

  test.beforeEach(() => {
    enabledByThisTest = false;
  });

  // Switched back off only when this test switched it on, and a failure to do
  // so FAILS the run rather than being swallowed: `hasAgent` decides whether
  // Get Help opens the chat or the case form, so leaving it on silently breaks
  // every create-case spec that follows.
  //
  // In afterEach rather than a `finally`, deliberately. A throw inside finally
  // replaces the error the test body raised, so a restore problem would mask
  // the real failure; as a hook it is reported alongside it instead.
  test.afterEach(async ({ page }) => {
    if (!enabledByThisTest || !project.id) return;
    await setNoveraViaApi(page, project.id, false);
  });

  test("case raised from a chat carries KB articles", async ({ page }) => {
    test.skip(
    !project.id,
    `${KNOWLEDGE_BASE_INPUT.projectType} needs a project id.`,
    );

    // Checked BEFORE anything is mutated: this test raises a case that cannot
    // be deleted and flips the project's `hasAgent` flag, and the flag change
    // alters what Get Help does for every other spec while it is in effect.
    // Same gate as the other creation specs.
    test.skip(
    !permanentWritesAllowed(),
    permanentWriteSkipReason(
      "a support case, and it toggles the project's AI assistant",
    ),
    );

    //
    // 1. Settings → AI Assistant: the assistant must be active, or Get Help
    //    opens the plain case form and there is no conversation to raise a
    //    case from.
    //
    const settings = new SettingsPage(page);
    await settings.openViaSideNav(project.id);
    await settings.openTab(SETTINGS.tabs.aiAssistant);

    await expect(settings.capabilitiesSection()).toBeVisible({
      timeout: 30_000,
    });

    // The switch renders disabled while project details load, and isChecked()
    // does not retry — so wait for it to be interactive before reading it.
    await expect(settings.noveraToggle()).toBeEnabled({ timeout: 30_000 });
    if (!(await settings.noveraToggle().isChecked())) {
      await settings.setNovera(project.id, true);
      // Ownership: only a test that made the change may undo it.
      enabledByThisTest = true;
    }

    const novera = SETTINGS.aiAssistant.novera;
    await expect(settings.noveraToggle()).toBeChecked({ timeout: 30_000 });
    await expect(
      settings.noveraChip(novera.activeChip),
      "the assistant must report Active before the chat can be used",
    ).toBeVisible({ timeout: 30_000 });

    //
    // 2. Get Help → the assistant, and ask the question.
    //
    const chat = new NoveraChatPage(page);
    await chat.openViaGetHelp(project.id);
    await expect(chat.heading()).toBeVisible();

    await chat.issueInput().fill(KNOWLEDGE_BASE_INPUT.question);
    await expect(chat.submitButton()).toBeEnabled({ timeout: 30_000 });
    await chat.submitButton().click();

    // The conversation's own id, not just the chat route: the id arrives a
    // second or two after the navigation, and acting before it lands aborts
    // the conversation's creation.
    await expect(page).toHaveURL(NOVERA_CHAT.conversationIdPattern, {
      timeout: 60_000,
    });
    console.log(`Knowledge Base: conversation at ${page.url()}`);

    //
    // 3. Let the assistant answer. The case description is built from the
    //    exchange, so this wait is what gives the backend something to
    //    recommend against.
    //
    await page.waitForTimeout(NOVERA_REPLY_SETTLE_MS);

    //
    // 4. Raise the case from the conversation. The form arrives
    //    pre-populated — deployment, product, title and description are
    //    already filled from the chat — so this is review-and-submit.
    //
    await chat.openCreateCase(project.id);

    const createCase = new CaseCreatePage(page);
    await expect(
      page.getByRole("heading", { name: CREATE_CASE.heading }),
    ).toBeVisible({ timeout: 60_000 });
    await expect(createCase.submitButton()).toBeEnabled({ timeout: 60_000 });

    const [createResponse] = await Promise.all([
      page.waitForResponse(
        (r) =>
          new URL(r.url()).pathname.endsWith("/cases") &&
          r.request().method() === "POST" &&
          isSuccess(r.status()),
      ),
      createCase.submit(),
    ]);

    const created = (await createResponse.json()) as {
      id?: string;
      number?: string;
    };
    expect(created.id, "backend returned no case id").toBeTruthy();
    console.log(
      `Knowledge Base: created case ${created.number ?? created.id}`,
    );

    //
    // 5. The Knowledge Base tab of that case.
    //
    // Armed BEFORE the case detail loads, not alongside the tab click. The
    // recommendations call fires when the tab opens in the current build, but
    // nothing guarantees that — the tab label carries a count, so a build that
    // prefetched to populate it would send the request during page load and a
    // listener registered later would miss it entirely, failing as a 60s
    // timeout rather than as the empty result it actually is. A wait registered
    // early still matches a request made later, so this is strictly safer.
    //
    // Capturing it at all matters because an empty list and a failed render
    // look identical on screen; only the wire tells them apart.
    const recommendationResponse = page.waitForResponse(
      (r) =>
        r.url().includes(CASE_KNOWLEDGE_BASE.recommendationsPath) &&
        r.request().method() === "POST",
      { timeout: 120_000 },
    );

    const caseDetail = new CaseDetailPage(page);
    await expect(page).toHaveURL(new RegExp(CASE_DETAIL.pathSegment), {
      timeout: 60_000,
    });
    await expect(caseDetail.caseNumber()).toBeVisible({ timeout: 60_000 });

    await caseDetail.openKnowledgeBaseTab();
    const recommendation = await recommendationResponse;

    expect(
      isSuccess(recommendation.status()),
      `the recommendation request failed (${recommendation.status()})`,
    ).toBe(true);

    const recommended = (await recommendation.json()) as {
      query?: string;
      recommendations?: unknown[];
    };
    console.log(
      `Knowledge Base: service returned ` +
        `${recommended.recommendations?.length ?? 0} recommendation(s) for ` +
        `"${recommended.query}"`,
    );

    // Neither "no articles" nor "not enough content" is acceptable here: the
    // case came from a real question, which is exactly the input the
    // recommendation service exists to act on. Asserting their absence names
    // WHICH of the two happened when it fails, where a bare count of zero
    // would not.
    await expect(
      caseDetail.detailsText(CASE_KNOWLEDGE_BASE.needsContentMessage),
      "the case should carry enough text to recommend from",
    ).toHaveCount(0);
    await expect(
      caseDetail.detailsText(CASE_KNOWLEDGE_BASE.emptyMessage),
      `the recommendation service returned no articles for ` +
        `"${recommended.query}". The request succeeded, so this is the ` +
        `service or its corpus, not the portal — check that staging's ` +
        `knowledge base is populated.`,
    ).toHaveCount(0);

    const articles = await caseDetail.knowledgeBaseArticles().count();
    expect(
      articles,
      "the Knowledge Base tab should list at least one article",
    ).toBeGreaterThan(0);

    // The tab's own count must agree with what is rendered — they come from
    // the same fetch, so a mismatch means one of them is stale.
    const tabCount = await caseDetail.knowledgeBaseTabCount();
    expect(tabCount, "the tab count should match the rendered articles").toBe(
      articles,
    );

    await expect(caseDetail.knowledgeBaseArticles().first()).toBeVisible();

    console.log(
      `Knowledge Base: ${articles} article(s) on ` +
        `${created.number ?? created.id}`,
    );
  });
});
