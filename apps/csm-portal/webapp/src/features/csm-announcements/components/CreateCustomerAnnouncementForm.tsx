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

import {
  Box,
  Button,
  Card,
  Checkbox,
  FormControlLabel,
  Grid,
  TextField,
  Typography,
} from "@wso2/oxygen-ui";
import { useMemo, useState, type JSX } from "react";
import type { BeSubscriptionType } from "@api/backend/types";
import EditorWithSourceToggle from "@components/rich-text-editor/EditorWithSourceToggle";
import { useErrorBanner } from "@context/error-banner/ErrorBannerContext";
import { useAddTagToCase } from "@features/csm-cases/api/useCaseTags";
import { usePostCsmCase } from "@features/csm-cases/api/usePostCsmCase";
import { DRY_RUN_TAG_LABEL, useAnnouncementDryRun } from "@features/csm-announcements/api/useAnnouncementDryRun";
import { useAnnouncementExcludedProjectKeys } from "@features/csm-announcements/api/useAnnouncementExcludedProjectKeys";
import { useResolveAnnouncementAudience } from "@features/csm-announcements/api/useResolveAnnouncementAudience";
import AnnouncementDryRunCard from "@features/csm-announcements/components/AnnouncementDryRunCard";
import AudienceScopeControls, {
  type AnnouncementAudienceScope,
} from "@features/csm-announcements/components/AudienceScopeControls";
import ResolvedAudienceList from "@features/csm-announcements/components/ResolvedAudienceList";
import {
  ANNOUNCEMENT_CASE_CREATE_CONCURRENCY_LIMIT,
  settleWithConcurrencyLimit,
} from "@features/csm-announcements/utils/settleWithConcurrencyLimit";
import { useNavTransition } from "@hooks/useNavTransition";

/**
 * The two default exclusions offered for the "all customer projects" scope,
 * mirroring the ServiceNow flow conditions this replaces (see
 * AudienceScopeControls' own doc comment for the Account Life Cycle caveat).
 */
const CLOUD_SUBSCRIPTION_TYPES: BeSubscriptionType[] = ["cloud_support", "cloud_evaluation_support"];
const CLOSED_STATES: string[] = ["Restricted", "Suspended"];

/**
 * The fixed tag label attached to every case in a security announcement.
 * There is no dedicated "announcement type" field anywhere in the platform
 * yet (confirmed: CreateCaseRequest's announcement branch only validates
 * subject/description) — this reuses the existing free-text case-tag
 * mechanism, which already works end-to-end for announcement cases and
 * already renders as a chip on the case detail page's Details tab with no
 * further changes needed. A fixed, exact string (rather than free typing)
 * is what makes it findable/filterable later despite tags having no closed
 * vocabulary on the backend.
 */
const SECURITY_ANNOUNCEMENT_TAG_LABEL = "Security Announcement";

/** The rich-text editor emits `<p></p>` when empty; check the stripped text. */
function isEmptyHtml(html: string): boolean {
  return html.replace(/<[^>]*>/g, "").replace(/&nbsp;/g, " ").trim().length === 0;
}

const BACK_TARGET = "/announcements";

/**
 * The "Create announcement for customers" flow (Option 1 of the two-option
 * announcement create page — see AnnouncementKindSelector). Creates an
 * announcement (`type: "announcement"`) against one or more projects. Unlike
 * a standard case, this is a broadcast — no severity/issueType/deployment/
 * deployedProduct/attachments, just a subject and description.
 *
 * Two audience scopes (see AudienceScopeControls): a hand-picked project
 * list, same as before, or "all customer projects" resolved from the entity
 * service under a pair of default exclusions. Either way, a single
 * announcement "record" per project: the backend's `POST /cases` create call
 * takes exactly one `projectId`, so the resolved/picked list fans out into
 * one independent create call per project (same subject/description on
 * each), not one record with a target list — there is still no batch entity
 * (see the announcement-enhancement brief's Phase 3). Submitting is
 * therefore a batch: if some calls fail while others succeed, the succeeded
 * ones stand (no auto-retry) and the failures are reported by project so the
 * engineer can retry just those.
 *
 * "This is a security announcement" tags every created case with a fixed
 * label (see SECURITY_ANNOUNCEMENT_TAG_LABEL) via a second call per case,
 * `POST /cases/{id}/tags` — there's no dedicated announcement-type field to
 * set this on instead. A tag-attach failure never invalidates its case
 * (the case already exists by then) — it's tracked and reported separately
 * from a create failure, since the fix is "add the label by hand," not
 * "retry the create."
 *
 * "Dry run" is the shared useAnnouncementDryRun/AnnouncementDryRunCard pair
 * (also used by the EOL/product-version flow) — creates exactly one real
 * case in a single fixed test project, tagged with DRY_RUN_TAG_LABEL plus
 * SECURITY_ANNOUNCEMENT_TAG_LABEL when this checkbox is on. It's independent
 * of audience/scope entirely: no project needs to be picked or resolved to
 * dry-run, since the test project is fixed regardless.
 */
export default function CreateCustomerAnnouncementForm(): JSX.Element {
  const navigate = useNavTransition();
  const { showError } = useErrorBanner();

  const [scope, setScope] = useState<AnnouncementAudienceScope>("specific");
  const [projectIds, setProjectIds] = useState<string[]>([]);
  const [excludeCloudTypes, setExcludeCloudTypes] = useState(true);
  const [excludeClosedStates, setExcludeClosedStates] = useState(true);
  const [subject, setSubject] = useState("");
  const [description, setDescription] = useState("");
  const [isSecurityAnnouncement, setIsSecurityAnnouncement] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  const postCase = usePostCsmCase();
  const addTag = useAddTagToCase();

  const dryRunTagLabels = useMemo(
    () => [DRY_RUN_TAG_LABEL, ...(isSecurityAnnouncement ? [SECURITY_ANNOUNCEMENT_TAG_LABEL] : [])],
    [isSecurityAnnouncement],
  );
  const { runningDryRun, dryRunResult, canRunDryRun, handleRunDryRun } = useAnnouncementDryRun({
    subject,
    description,
    tagLabels: dryRunTagLabels,
    extraCanRun: !submitting,
  });

  const audienceFilters = useMemo(
    () => ({
      excludeSubscriptionTypes: excludeCloudTypes ? CLOUD_SUBSCRIPTION_TYPES : [],
      excludeClosureStates: excludeClosedStates ? CLOSED_STATES : [],
    }),
    [excludeCloudTypes, excludeClosedStates],
  );
  const resolvedAudience = useResolveAnnouncementAudience(scope === "all", audienceFilters);
  const excludedProjectKeysQuery = useAnnouncementExcludedProjectKeys();

  // The scope actually being sent: today's hand-picked list, or every id
  // resolved for "all customer projects" (see AudienceScopeControls' own doc
  // comment on what that scope does and doesn't filter).
  const targetProjectIds = useMemo(
    () => (scope === "all" ? resolvedAudience.projects.map((p) => p.id) : projectIds),
    [scope, resolvedAudience.projects, projectIds],
  );

  const canSubmit = useMemo(
    () =>
      targetProjectIds.length > 0 &&
      !(scope === "all" && resolvedAudience.isLoading) &&
      // TanStack Query can retain a previous successful fetch's `data` after
      // a later refetch fails (isLoading goes back to false, but the stale
      // list is still sitting there) — without this check, targetProjectIds
      // would still look populated and a resolution failure could let the
      // sender submit against an audience that's actually out of date.
      !(scope === "all" && resolvedAudience.isError) &&
      subject.trim().length > 0 &&
      !isEmptyHtml(description) &&
      !submitting,
    [
      targetProjectIds,
      scope,
      resolvedAudience.isLoading,
      resolvedAudience.isError,
      subject,
      description,
      submitting,
    ],
  );

  const handleSubmit = async (): Promise<void> => {
    if (!canSubmit) return;
    setSubmitting(true);

    const trimmedSubject = subject.trim();
    // Tag failures are tracked separately from create failures: the case
    // already exists by the time a tag-attach could fail, so it must not be
    // reported (or retried) as a failed create — just as a narrower, secondary
    // problem to fix on an otherwise-successful case.
    const failedTagProjectIds: string[] = [];
    const results = await settleWithConcurrencyLimit(
      targetProjectIds,
      ANNOUNCEMENT_CASE_CREATE_CONCURRENCY_LIMIT,
      async (projectId) => {
        const created = await postCase.mutateAsync({
          type: "announcement",
          projectId,
          subject: trimmedSubject,
          description,
        });
        if (isSecurityAnnouncement) {
          try {
            await addTag.mutateAsync({
              caseId: created.id,
              label: SECURITY_ANNOUNCEMENT_TAG_LABEL,
            });
          } catch {
            failedTagProjectIds.push(projectId);
          }
        }
        return created;
      },
    );
    setSubmitting(false);

    // Neither audience source exposes picked project names for a failure
    // report beyond what's already resolved, so a failure is reported by id
    // — still enough for the engineer to identify which project(s) to retry.
    const failedProjectIds = targetProjectIds.filter(
      (_, i) => results[i].status === "rejected",
    );

    if (failedProjectIds.length === 0 && failedTagProjectIds.length === 0) {
      navigate(BACK_TARGET);
      return;
    }

    const succeededCount = targetProjectIds.length - failedProjectIds.length;
    if (succeededCount > 0) {
      // Partial failure: the succeeded creates already landed and aren't
      // retried automatically, so navigate away and surface exactly which
      // project(s) still need attention — a failed create needs retrying,
      // a failed tag attach just needs the label added by hand.
      const messages: string[] = [];
      if (failedProjectIds.length > 0) {
        messages.push(
          `created for ${succeededCount} of ${targetProjectIds.length} project${
            targetProjectIds.length === 1 ? "" : "s"
          }, but failed for project${failedProjectIds.length === 1 ? "" : "s"} ${failedProjectIds.join(
            ", ",
          )} — create it again for the failed project${failedProjectIds.length === 1 ? "" : "s"} only`,
        );
      } else {
        messages.push(`created for all ${targetProjectIds.length} project${targetProjectIds.length === 1 ? "" : "s"}`);
      }
      if (failedTagProjectIds.length > 0) {
        messages.push(
          `the security label couldn't be attached for project${
            failedTagProjectIds.length === 1 ? "" : "s"
          } ${failedTagProjectIds.join(", ")} — add it manually on ${
            failedTagProjectIds.length === 1 ? "that case" : "those cases"
          }`,
        );
      }
      showError(`The announcement was ${messages.join("; ")}.`);
      navigate(BACK_TARGET);
    } else {
      showError("Could not create the announcement. Please try again.");
    }
  };

  return (
    <Card variant="outlined" sx={{ p: 3 }}>
      <Grid container spacing={2.5}>
        <Grid size={{ xs: 12 }}>
          <AudienceScopeControls
            scope={scope}
            onScopeChange={setScope}
            projectIds={projectIds}
            onProjectIdsChange={setProjectIds}
            excludeCloudTypes={excludeCloudTypes}
            onExcludeCloudTypesChange={setExcludeCloudTypes}
            excludeClosedStates={excludeClosedStates}
            onExcludeClosedStatesChange={setExcludeClosedStates}
            excludedProjectKeys={excludedProjectKeysQuery.data ?? []}
            disabled={submitting}
          />
        </Grid>

        {scope === "all" && (
          <Grid size={{ xs: 12 }}>
            <ResolvedAudienceList
              projects={resolvedAudience.projects}
              total={resolvedAudience.total}
              isLoading={resolvedAudience.isLoading}
              isError={resolvedAudience.isError}
            />
          </Grid>
        )}

        <Grid size={{ xs: 12 }}>
          <TextField
            label="Subject"
            size="small"
            fullWidth
            required
            value={subject}
            onChange={(e) => setSubject(e.target.value.slice(0, 200))}
            helperText={
              subject.length >= 160 ? `${subject.length}/200` : undefined
            }
          />
        </Grid>
        <Grid size={{ xs: 12 }}>
          <FormControlLabel
            control={
              <Checkbox
                size="small"
                checked={isSecurityAnnouncement}
                disabled={submitting}
                onChange={(e) => setIsSecurityAnnouncement(e.target.checked)}
              />
            }
            label="This is a security announcement"
          />
          <Typography variant="caption" color="text.secondary" sx={{ display: "block", mt: -0.5 }}>
            Attaches a &quot;{SECURITY_ANNOUNCEMENT_TAG_LABEL}&quot; label to every case this
            creates.
          </Typography>
        </Grid>
        <Grid size={{ xs: 12 }}>
          <Typography
            id="announcement-description-label"
            component="label"
            variant="caption"
            color="text.secondary"
            sx={{ display: "block", mb: 0.5 }}
          >
            Description
          </Typography>
          {/* Editor doesn't accept an `id`, so associate the label by wrapping
              the editor in a labelled group for assistive tech. */}
          <Box role="group" aria-labelledby="announcement-description-label">
            <EditorWithSourceToggle
              value={description}
              onChange={setDescription}
              placeholder="Describe the announcement…"
              minHeight={180}
              maxHeight={420}
              toolbarVariant="full"
              disabled={submitting}
            />
          </Box>
        </Grid>
      </Grid>

      <AnnouncementDryRunCard
        runningDryRun={runningDryRun}
        dryRunResult={dryRunResult}
        canRunDryRun={canRunDryRun}
        onRunDryRun={() => void handleRunDryRun()}
      />

      <Box
        sx={{
          display: "flex",
          justifyContent: "flex-end",
          gap: 1.5,
          mt: 2.5,
          pt: 2,
          borderTop: 1,
          borderColor: "divider",
        }}
      >
        <Button variant="outlined" onClick={() => navigate(BACK_TARGET)}>
          Cancel
        </Button>
        <Button
          variant="contained"
          onClick={() => void handleSubmit()}
          disabled={!canSubmit}
        >
          {submitting ? "Creating…" : "Create announcement"}
        </Button>
      </Box>
    </Card>
  );
}
