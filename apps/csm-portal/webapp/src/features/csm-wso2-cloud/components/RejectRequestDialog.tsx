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
  Alert,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  TextField,
  Typography,
} from "@wso2/oxygen-ui";
import { useState, type JSX } from "react";
import { usePatchCloudRequest } from "@features/csm-wso2-cloud/api/usePatchCloudRequest";
import {
  CLOUD_REQUEST_TYPE_LABELS,
  type CloudRequest,
} from "@features/csm-wso2-cloud/types/wso2Cloud";

/**
 * Rejects a pending request with a reason, which the requester sees next to
 * the status. `request` is null while closed.
 */
export default function RejectRequestDialog({
  request,
  onClose,
}: {
  request: CloudRequest | null;
  onClose: () => void;
}): JSX.Element {
  const resolve = usePatchCloudRequest();
  const [remark, setRemark] = useState("");

  const handleClose = () => {
    if (resolve.isPending) return;
    resolve.reset();
    setRemark("");
    onClose();
  };

  const handleConfirm = () => {
    if (!request || !remark.trim()) return;
    resolve.mutate(
      { id: request.id, status: "rejected", remark },
      {
        onSuccess: () => {
          resolve.reset();
          setRemark("");
          onClose();
        },
      },
    );
  };

  return (
    <Dialog open={request !== null} onClose={handleClose} maxWidth="xs" fullWidth>
      <DialogTitle>Reject {request?.id}?</DialogTitle>
      <DialogContent sx={{ display: "flex", flexDirection: "column", gap: 1.5 }}>
        {request && (
          <Typography variant="body2">
            {CLOUD_REQUEST_TYPE_LABELS[request.type]}: {request.target.label} ({request.summary}),
            raised by {request.requestedBy}.
          </Typography>
        )}
        <TextField
          label="Reason"
          required
          multiline
          minRows={3}
          value={remark}
          onChange={(e) => setRemark(e.target.value)}
          helperText="Shown to the requester with the decision."
          autoFocus
        />
        {resolve.isError && <Alert severity="error">{resolve.error.message}</Alert>}
      </DialogContent>
      <DialogActions>
        <Button onClick={handleClose} disabled={resolve.isPending}>
          Go back
        </Button>
        <Button
          variant="contained"
          color="error"
          onClick={handleConfirm}
          disabled={resolve.isPending || !remark.trim()}
        >
          Reject request
        </Button>
      </DialogActions>
    </Dialog>
  );
}
