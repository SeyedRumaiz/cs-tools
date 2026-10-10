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
  Dialog,
  DialogActions,
  DialogContent,
  DialogContentText,
  DialogTitle,
  TextField,
} from "@wso2/oxygen-ui";
import { useState, type JSX } from "react";
import { useSearchInternalUsersByName } from "@api/useSearchUsersByName";
import AsyncEntitySelect from "@components/AsyncEntitySelect";
import { userLabel } from "@features/csm-operations/utils/incidentFormOptions";
import type { BeUpdateProblemPayload, BeUser } from "@api/backend/types";

/** The two moves ServiceNow refuses without a field: Assess needs an
 * assignee, Resolved needs fix notes (its own "Assess"/"Resolve" dialogs). */
export type ProblemRequirementTransition = "assess" | "resolve";

interface ProblemTransitionRequirementDialogProps {
  transition: ProblemRequirementTransition;
  isSubmitting: boolean;
  onClose: () => void;
  /** The PATCH body: the transition plus the field it needs. */
  onConfirm: (patch: BeUpdateProblemPayload) => void;
}

const COPY: Record<ProblemRequirementTransition, { title: string; text: string; action: string }> = {
  assess: {
    title: "Move to Assess",
    text: "A problem needs an assignee before it can be assessed.",
    action: "Assign and move to Assess",
  },
  resolve: {
    title: "Move to Resolved",
    text: "Describe the fix that was applied. A problem needs fix notes before it can be resolved.",
    action: "Move to Resolved",
  },
};

/**
 * Collects the one field ServiceNow's problem state model requires for a
 * move (discovery script 61) and sends it with the transition in the same
 * PATCH, as ServiceNow's own Assess / Resolve dialogs do. Without it the
 * move fails: dual-write gets ServiceNow's 409, Postgres-only a 400.
 * `ProblemDetailPage` opens this only when the problem lacks the field.
 */
export default function ProblemTransitionRequirementDialog({
  transition,
  isSubmitting,
  onClose,
  onConfirm,
}: ProblemTransitionRequirementDialogProps): JSX.Element {
  const [assignedToId, setAssignedToId] = useState("");
  const [fixNotes, setFixNotes] = useState("");
  const copy = COPY[transition];
  const ready = transition === "assess" ? assignedToId !== "" : fixNotes.trim() !== "";

  const submit = (): void => {
    if (!ready) return;
    onConfirm(
      transition === "assess"
        ? { transition, assignedToId }
        : { transition, fixNotes: fixNotes.trim() },
    );
  };

  return (
    <Dialog
      open
      onClose={() => {
        if (!isSubmitting) onClose();
      }}
      maxWidth="xs"
      fullWidth
      aria-labelledby="problem-transition-requirement-title"
    >
      <DialogTitle id="problem-transition-requirement-title">{copy.title}</DialogTitle>
      <DialogContent dividers>
        <Box sx={{ display: "flex", flexDirection: "column", gap: 2, pt: 0.5 }}>
          <DialogContentText variant="body2">{copy.text}</DialogContentText>
          {transition === "assess" ? (
            <AsyncEntitySelect<BeUser>
              id="problem-transition-assigned-to"
              label="Assigned to"
              required
              placeholder="Search people…"
              value={assignedToId}
              onChange={setAssignedToId}
              disabled={isSubmitting}
              useSearch={useSearchInternalUsersByName}
              getId={(u) => u.id!}
              getLabel={userLabel}
            />
          ) : (
            <TextField
              label="Fix notes"
              required
              multiline
              minRows={3}
              fullWidth
              size="small"
              value={fixNotes}
              disabled={isSubmitting}
              onChange={(e) => setFixNotes(e.target.value)}
            />
          )}
        </Box>
      </DialogContent>
      <DialogActions>
        <Button color="inherit" onClick={onClose} disabled={isSubmitting}>
          Cancel
        </Button>
        <Button variant="contained" onClick={submit} disabled={!ready || isSubmitting} loading={isSubmitting}>
          {copy.action}
        </Button>
      </DialogActions>
    </Dialog>
  );
}
