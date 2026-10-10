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

import { Box, Tab, Tabs } from "@wso2/oxygen-ui";
import { type JSX } from "react";
import CsmIssuesView from "@features/csm-cases/components/CsmIssuesView";
import ConversationsTab from "@features/csm-projects/components/ConversationsTab";
import { usePortalAccess } from "@context/current-user/usePortalAccess";
import { useQueryParamTabs } from "@hooks/useSectionTabs";

// Conversations (chat sessions) aren't a case type (`BeCaseType`) — they're a
// different resource entirely, backed by their own search/list, not
// `/cases/search` — so they stay a separate sub-tab rather than folding into
// the flat issues list below. Everything that IS a case type (cases, service
// requests, security reports, engagements, announcements) now renders as one
// flat, unlocked-type list instead of one sub-tab per type.
type WorkItemSubTab = "issues" | "conversations";

const WORK_ITEM_SUB_TABS: readonly WorkItemSubTab[] = ["issues", "conversations"];

interface WorkItemsTabProps {
  projectId: string;
}

/**
 * A project's work items: a single flat list spanning every case type this
 * caller can see (Case / Service request / Security report / Engagement /
 * Announcement — `caseType.ts`'s `visibleCaseTypes`, which drops Security
 * report for a caller without security-report access (Security Center or
 * comment_updater); the backend already
 * returns announcements for a project, so hiding that type here would be a
 * silent regression), filtered by a "Work item type" multi-select rather than
 * one sub-tab per type. Detail links resolve per-row to each type's own detail page via
 * `CasesList`'s `caseTypeDetailBasePath` fallback (no `detailBasePath` is
 * passed here, unlike the old single-type sub-tabs, since a mixed list can't
 * point every row at one fixed base path).
 *
 * `hideProjectFilter` is passed (this view is already locked to one project
 * via `lockedFilters`) and `typeFilterLabel="Work item type"` renders in the
 * project filter's old grid slot, since a project-scoped list has no use for
 * its own project filter — see `CasesFilterBar`'s `typeFilterLabel` doc
 * comment for why this reuses the existing "Case type" control (an actual
 * closed `<Select multiple>` dropdown already, not an inline checkbox list)
 * rather than introducing a new control.
 *
 * `hideOnboardingStatusFilter`/`hideCreTeamFilter`/`hideSreTeamFilter` are
 * also passed: all three are per-project attributes (a project's onboarding
 * status, the CS/SRE teams its account is scoped to), so every work item on
 * this already project-scoped tab shares the same value for each —
 * filtering by them here is a no-op that only adds clutter.
 * `showSeverityFilter` is passed `true` (overriding
 * `CsmIssuesView`'s own "only when type is locked to Case" default, which
 * would never fire here since this tab's type filter is unlocked): Severity
 * is still a genuinely useful control on this mixed list, since narrowing by
 * it implicitly narrows to Case-type rows (the only type severity applies
 * to) the same way picking "Case" in the Work item type control would;
 * non-case rows simply have no severity to match a picked value.
 *
 * Conversations is the project's chat sessions (`ConversationsTab`), kept as
 * its own sub-tab alongside the flat issues list rather than a third
 * top-level project tab — it was already nested here before this revamp.
 */
export default function WorkItemsTab({ projectId }: WorkItemsTabProps): JSX.Element {
  // Chats (the project's pre-case Novera conversations) is shown only to
  // cs_engineer/admin/timecard_approver -- see canViewWorkItemsStaffView's
  // own doc comment. Everyone else gets only the flat issues list, with the
  // sub-tab strip itself skipped too: a single-option tab switcher is
  // clutter, not a real choice.
  const { canViewWorkItemsStaffView } = usePortalAccess();
  // Kept in the URL (`?subTab=`), not local state, alongside the parent
  // page's own `?tab=` -- see CsmProjectDetailPage.tsx's `projectPath` -- so
  // a create-flow round trip back to this project restores the exact sub-tab
  // the engineer was on, not just the Work items tab in general.
  const { activeTab: subTab, setActiveTab: setSubTab } = useQueryParamTabs<WorkItemSubTab>(
    WORK_ITEM_SUB_TABS,
    "issues",
    { paramName: "subTab" },
  );
  // A caller without the staff view never reaches "conversations" at all (no
  // tab strip to pick it from), regardless of what a stale ?subTab= in the
  // URL claims.
  const effectiveSubTab = canViewWorkItemsStaffView ? subTab : "issues";

  return (
    <Box sx={{ display: "flex", flexDirection: "column", gap: 2 }}>
      {canViewWorkItemsStaffView && (
        <Tabs value={effectiveSubTab} onChange={(_, v) => setSubTab(v as WorkItemSubTab)}>
          <Tab value="issues" label="Work items" />
          <Tab value="conversations" label="Chats" />
        </Tabs>
      )}

      {effectiveSubTab === "issues" && (
        <CsmIssuesView
          entityNoun="work items"
          lockedFilters={{ projects: [projectId] }}
          hideProjectFilter
          hideOnboardingStatusFilter
          hideCreTeamFilter
          hideSreTeamFilter
          showSeverityFilter
          typeFilterLabel="Work item type"
          hideBackButton
        />
      )}

      {effectiveSubTab === "conversations" && <ConversationsTab projectId={projectId} />}
    </Box>
  );
}
