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

import { useState, type JSX } from "react";
import {
  Button,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogContentText,
  FormControl,
  IconButton,
  InputLabel,
  MenuItem,
  Select,
  TextField,
  DialogTitle,
  type SelectChangeEvent,
} from "@wso2/oxygen-ui";
import { X } from "@wso2/oxygen-ui-icons-react";
import type { MetadataItem } from "@/types/common";

const CLOSE_NOTES_MAX_LENGTH = 1000;

export interface CaseResolutionFields {
  resolutionCode: string;
  cause: string;
  closeNotes: string;
}

export interface CaseStateConfirmDialogProps {
  open: boolean;
  actionLabel: string;
  isPending: boolean;
  onClose: () => void;
  onConfirm: (resolution?: CaseResolutionFields) => void;
  // When true, closing/accepting-solution requires resolutionCode, cause,
  // and closeNotes — entity-service rejects the state change without them
  // (see PatchCaseRequest's own doc comment). resolutionCodes/causes are the
  // options to offer; both are required alongside actionLabel when this is
  // true.
  requiresResolutionFields?: boolean;
  resolutionCodes?: MetadataItem[];
  causes?: MetadataItem[];
}

export default function CaseStateConfirmDialog({
  open,
  actionLabel,
  isPending,
  onClose,
  onConfirm,
  requiresResolutionFields = false,
  resolutionCodes = [],
  causes = [],
}: CaseStateConfirmDialogProps): JSX.Element {
  const [resolutionCode, setResolutionCode] = useState("");
  const [cause, setCause] = useState("");
  const [closeNotes, setCloseNotes] = useState("");

  // Reset the resolution fields exactly once per open/close transition —
  // the same during-render "adjusting state when a prop changes" pattern
  // RejectSolutionDialog uses, so a previous action's selections never leak
  // into the next one.
  const [prevOpen, setPrevOpen] = useState(open);
  if (open !== prevOpen) {
    setPrevOpen(open);
    if (!open) {
      setResolutionCode("");
      setCause("");
      setCloseNotes("");
    }
  }

  const handleClose = (): void => {
    if (isPending) return;
    onClose();
  };

  const handleConfirm = (): void => {
    if (isPending) return;
    if (requiresResolutionFields) {
      onConfirm({ resolutionCode, cause, closeNotes: closeNotes.trim() });
      return;
    }
    onConfirm();
  };

  const trimmedNotes = closeNotes.trim();
  const resolutionFieldsIncomplete =
    requiresResolutionFields &&
    (!resolutionCode || !cause || trimmedNotes.length === 0);

  return (
    <Dialog open={open} onClose={isPending ? undefined : handleClose} maxWidth="xs" fullWidth>
      <DialogTitle
        sx={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}
      >
        Confirm State Change
        <IconButton size="small" onClick={handleClose} aria-label="close" disabled={isPending}>
          <X size={18} />
        </IconButton>
      </DialogTitle>
      <DialogContent>
        <DialogContentText sx={{ mb: requiresResolutionFields ? 2 : 0 }}>
          Are you sure you want to{" "}
          <strong>{actionLabel.toLowerCase()}</strong> this case?
        </DialogContentText>
        {requiresResolutionFields && (
          <>
            <FormControl fullWidth size="small" sx={{ mt: 1, mb: 2 }}>
              <InputLabel id="case-resolution-code-label">Resolution Code *</InputLabel>
              <Select
                labelId="case-resolution-code-label"
                label="Resolution Code *"
                value={resolutionCode}
                disabled={isPending}
                onChange={(e: SelectChangeEvent<string>) => setResolutionCode(e.target.value)}
              >
                {resolutionCodes.map((c) => (
                  <MenuItem key={c.id} value={c.id}>
                    {c.label}
                  </MenuItem>
                ))}
              </Select>
            </FormControl>
            <FormControl fullWidth size="small" sx={{ mb: 2 }}>
              <InputLabel id="case-cause-label">Cause *</InputLabel>
              <Select
                labelId="case-cause-label"
                label="Cause *"
                value={cause}
                disabled={isPending}
                onChange={(e: SelectChangeEvent<string>) => setCause(e.target.value)}
              >
                {causes.map((c) => (
                  <MenuItem key={c.id} value={c.id}>
                    {c.label}
                  </MenuItem>
                ))}
              </Select>
            </FormControl>
            <TextField
              id="case-close-notes"
              label="Close Notes *"
              placeholder="Summarize how this case was resolved..."
              value={closeNotes}
              onChange={(e) => setCloseNotes(e.target.value.slice(0, CLOSE_NOTES_MAX_LENGTH))}
              fullWidth
              multiline
              rows={3}
              disabled={isPending}
              inputProps={{
                "aria-label": "Close notes",
                maxLength: CLOSE_NOTES_MAX_LENGTH,
              }}
              helperText={`${closeNotes.length}/${CLOSE_NOTES_MAX_LENGTH}`}
              FormHelperTextProps={{ sx: { textAlign: "right", m: 0, mt: 0.5 } }}
            />
          </>
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={handleClose} disabled={isPending}>
          Cancel
        </Button>
        <Button
          variant="contained"
          onClick={handleConfirm}
          disabled={isPending || resolutionFieldsIncomplete}
          startIcon={
            isPending ? <CircularProgress size={16} color="inherit" /> : undefined
          }
        >
          {isPending ? "Updating…" : "Confirm"}
        </Button>
      </DialogActions>
    </Dialog>
  );
}
