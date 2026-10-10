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

import { Button, Dialog, DialogActions, DialogContent, DialogTitle, Typography } from "@wso2/oxygen-ui";
import type { JSX } from "react";

/** Outcome of the deployed-dashboard lookup, as far as the page knows it. */
export type DeployedLookup = "exists" | "pending" | "failed" | "missing";

const CONSEQUENCE: Record<DeployedLookup, string> = {
  exists: "The dashboard will reset to the deployed version the next time it is opened.",
  pending:
    "The deployed version is still being checked, so what would be left after discarding is not known yet.",
  failed:
    "Could not check whether a deployed version exists, so discarding may leave nothing to restore.",
  missing:
    "There is no deployed version, so this draft is the only copy and discarding deletes it entirely.",
};

interface DiscardDraftDialogProps {
  open: boolean;
  /** Name shown in the body; the caller falls back to the draft id. */
  dashboardName: string;
  /** Whether a deployed version is confirmed to exist. Only "exists" may
   * promise a reset; the draft's origin alone does not prove one. */
  deployedLookup: DeployedLookup;
  onCancel: () => void;
  onConfirm: () => void;
}

/** Confirms throwing away the builder's local draft of one dashboard. */
export default function DiscardDraftDialog({
  open,
  dashboardName,
  deployedLookup,
  onCancel,
  onConfirm,
}: DiscardDraftDialogProps): JSX.Element {
  return (
    <Dialog open={open} onClose={onCancel} maxWidth="xs" fullWidth>
      <DialogTitle>Discard local draft?</DialogTitle>
      <DialogContent>
        <Typography variant="body2">
          Your local edits to &quot;{dashboardName}&quot; will be lost and this cannot be undone.{" "}
          {CONSEQUENCE[deployedLookup]}
        </Typography>
      </DialogContent>
      <DialogActions>
        <Button onClick={onCancel}>Cancel</Button>
        <Button variant="contained" color="error" onClick={onConfirm}>
          Discard
        </Button>
      </DialogActions>
    </Dialog>
  );
}
