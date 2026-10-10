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

// SME Day/Night shifts have L1, L2 and L3 layers (Case Paging's SME ladder
// calls them in turn). This drives the real local stack end to end:
//
//   1. the roster's cell picker offers L1/L2/L3 for the SME team's Day and
//      Night windows;
//   2. rostering an L1, an L2 and an L3 on one SME night through that picker
//      clears exactly that night's three gaps on the Case Paging tab's SME
//      chain, while the same day's Day window keeps its gaps.
//
// It WRITES to the stack's Postgres (three test people, tagged qa-sme-e2e, and
// their turns), so it runs only when told which stack: set
// E2E_POSTGRES_CONTAINER (the local compose stack's is csm-platform-postgres-1).
// Everything it adds is removed before and after. It signs in through the local
// mock identity provider as the seeded SME rota admin, no password involved.
//
//   E2E_NO_WEBSERVER=1 E2E_POSTGRES_CONTAINER=csm-platform-postgres-1 \
//     npx playwright test tests/e2e/specs/team-schedule/sme-tiers.spec.ts

import { spawn } from "node:child_process";
import { expect, test, type Page } from "@playwright/test";

const POSTGRES_CONTAINER = process.env.E2E_POSTGRES_CONTAINER?.trim();
const ROTA_ADMIN = "sme.rota.admin@example.com";
const TEAM_KEY = "asgardeo";

/** The test people, one per tier. Fake names and addresses on purpose. */
const PEOPLE = [
  { name: "QA SME One", email: "qa.sme.one@example.com", tier: "L1" },
  { name: "QA SME Two", email: "qa.sme.two@example.com", tier: "L2" },
  { name: "QA SME Three", email: "qa.sme.three@example.com", tier: "L3" },
] as const;

/** Runs SQL on the stack's Postgres and resolves with what it printed. */
function psql(sql: string): Promise<string> {
  return new Promise((resolve, reject) => {
    const child = spawn("docker", [
      "exec", "-i", POSTGRES_CONTAINER ?? "", "psql", "-U", "postgres", "-d", "csm_platform",
      "-v", "ON_ERROR_STOP=1", "-q", "-t", "-A", "-f", "-",
    ]);
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (d: Buffer) => (stdout += d.toString()));
    child.stderr.on("data", (d: Buffer) => (stderr += d.toString()));
    child.on("error", reject);
    child.on("close", (code) => (code === 0 ? resolve(stdout.trim()) : reject(new Error(`psql exited ${code}: ${stderr}`))));
    child.stdin.end(sql);
  });
}

const emailsSql = PEOPLE.map((p) => `'${p.email}'`).join(", ");

/** Adds the test people to the SME team, once. */
async function seedPeople(): Promise<void> {
  const values = PEOPLE.map(
    (p) => `('${p.email}', '${p.email}', '${p.name}', 'QA', '${p.name.replace("QA ", "")}', TRUE, now(), now(), 'qa-sme-e2e', 'qa-sme-e2e')`,
  ).join(",\n");
  await psql(`
    INSERT INTO "user" (user_name, email, name, first_name, last_name, is_active, created_on, updated_on, created_by, updated_by)
    VALUES ${values}
    ON CONFLICT (user_name) DO NOTHING;
    INSERT INTO team_member (team_id, user_id, role, created_on, updated_on, created_by, updated_by)
    SELECT t.id, u.id, 'engineer', now(), now(), 'qa-sme-e2e', 'qa-sme-e2e'
      FROM team t, "user" u
     WHERE t.key = '${TEAM_KEY}' AND u.user_name IN (${emailsSql})
       AND NOT EXISTS (SELECT 1 FROM team_member m WHERE m.team_id = t.id AND m.user_id = u.id);`);
}

/** Removes the test people's turns (and, with people, the people too). */
async function cleanUp(people: boolean): Promise<void> {
  await psql(`
    DELETE FROM team_schedule_assignment
     WHERE user_id IN (SELECT id FROM "user" WHERE user_name IN (${emailsSql}));
    ${
      people
        ? `DELETE FROM team_member WHERE user_id IN (SELECT id FROM "user" WHERE user_name IN (${emailsSql}));
           DELETE FROM "user" WHERE user_name IN (${emailsSql}) AND created_by = 'qa-sme-e2e';`
        : ""
    }`);
}

/** A date inside the readiness window (today .. +6, IST): two days from now. */
function targetDay(): string {
  const ist = new Date(Date.now() + 5.5 * 3600_000);
  ist.setUTCDate(ist.getUTCDate() + 2);
  return ist.toISOString().slice(0, 10);
}

async function signIn(page: Page): Promise<void> {
  await page.goto("/team-schedule");
  const email = page.getByLabel("Email");
  await email.waitFor();
  await email.fill(ROTA_ADMIN);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Team Schedule" })).toBeVisible({ timeout: 20_000 });
}

/** The SME chain's gap messages on the Case Paging tab, every one of them. */
async function smeGaps(page: Page): Promise<string[]> {
  await page.getByRole("tab", { name: /^Case Paging/ }).click();
  await page.getByRole("tab", { name: "SME", exact: true }).click();
  // The readiness strip loads after the tab: wait for the chain's heading.
  await expect(page.getByText(/^SME page —/)).toBeVisible({ timeout: 20_000 });
  const showAll = page.getByRole("button", { name: /^Show all \d+/ });
  if (await showAll.isVisible().catch(() => false)) {
    await showAll.click();
    await expect(showAll).toBeHidden();
  }
  const text = await page.locator("main").innerText();
  return text.split("\n").filter((l) => l.startsWith("Asgardeo has no "));
}

const gap = (tier: string, window: "day" | "night", day: string) =>
  `Asgardeo has no ${tier} rostered for Asgardeo ${window} escalation on ${day}.`;

/** Opens the month roster in edit mode. */
async function editRoster(page: Page): Promise<void> {
  await page.getByRole("tab", { name: /^Month roster/ }).click();
  await page.getByRole("button", { name: "Edit rota" }).click();
  await expect(page.getByRole("button", { name: "Done editing" })).toBeVisible();
}

/** Opens a person's cell picker (any editable cell in their row). */
async function openPicker(page: Page, name: string) {
  await page.locator(`td.editable[title^="${name} ·"]`).first().click();
  const picker = page.getByLabel(`Change ${name}'s rota`);
  await expect(picker).toBeVisible();
  return picker;
}

test.describe("SME shifts have L1-L3 layers", () => {
  test.skip(
    !POSTGRES_CONTAINER,
    "writes to the stack under test: set E2E_POSTGRES_CONTAINER (local compose: csm-platform-postgres-1)",
  );

  test.beforeAll(async () => {
    await cleanUp(false);
    await seedPeople();
  });
  test.afterAll(async () => {
    if (POSTGRES_CONTAINER) await cleanUp(true);
  });

  test("the cell picker offers L1, L2 and L3 for the SME team's Day and Night", async ({ page }) => {
    await signIn(page);
    await editRoster(page);
    const picker = await openPicker(page, PEOPLE[0].name);
    for (const window of ["Day", "Night"]) {
      for (const tier of ["L1", "L2", "L3"]) {
        await expect(picker.getByRole("button", { name: new RegExp(`^${tier} for .*${window}`) })).toBeVisible();
      }
    }
    await picker.getByRole("button", { name: "Close" }).click();
  });

  test("rostering L1-L3 on an SME night clears that night's gaps, and only those", async ({ page }) => {
    const day = targetDay();
    await signIn(page);

    const before = await smeGaps(page);
    for (const p of PEOPLE) {
      expect(before, `before rostering, the ${p.tier} gap on the night of ${day}`).toContain(gap(p.tier, "night", day));
    }

    await editRoster(page);
    for (const p of PEOPLE) {
      const picker = await openPicker(page, p.name);
      await picker.getByLabel("Mark from").fill(day);
      await picker.getByLabel("Mark until").fill(day);
      await picker.getByRole("button", { name: new RegExp(`^${p.tier} for .*Night`) }).click();
      await expect(picker).toBeHidden();
    }
    await page.getByRole("button", { name: "Done editing" }).click();

    // The turns are stored with their tiers.
    const stored = await psql(`
      SELECT string_agg(coalesce(a.tier::text, '-'), ',' ORDER BY a.tier)
        FROM team_schedule_assignment a JOIN team_schedule_shift s ON s.id = a.shift_id
       WHERE a.rota_date = '${day}' AND s.code = 'SME_ASG_NIGHT'
         AND a.user_id IN (SELECT id FROM "user" WHERE user_name IN (${emailsSql}));`);
    expect(stored).toBe("L1,L2,L3");

    await page.reload();
    await expect(page.getByRole("heading", { name: "Team Schedule" })).toBeVisible();
    const after = await smeGaps(page);
    for (const p of PEOPLE) {
      expect(after, `after rostering, the ${p.tier} gap on the night of ${day}`).not.toContain(gap(p.tier, "night", day));
      expect(after, `the ${p.tier} gap on the DAY of ${day} is untouched`).toContain(gap(p.tier, "day", day));
    }
    expect(after.length).toBe(before.length - 3);
  });
});
