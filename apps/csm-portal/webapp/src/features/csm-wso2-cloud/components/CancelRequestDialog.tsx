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
  Typography,
} from "@wso2/oxygen-ui";
import type { JSX } from "react";
import { useDeleteCloudRequest } from "@features/csm-wso2-cloud/api/useDeleteCloudRequest";
import {
  CLOUD_REQUEST_TYPE_LABELS,
  type CloudRequest,
} from "@features/csm-wso2-cloud/types/wso2Cloud";

/**
 * Confirms cancelling one of your own pending requests. `request` is null while
 * closed; `onCancelled` runs once the request is gone, for a page that showed
 * only that request.
 */
export default function CancelRequestDialog({
  request,
  onClose,
  onCancelled,
}: {
  request: CloudRequest | null;
  onClose: () => void;
  onCancelled?: () => void;
}): JSX.Element {
  const cancel = useDeleteCloudRequest();

  const handleClose = () => {
    if (cancel.isPending) return;
    cancel.reset();
    onClose();
  };

  const handleConfirm = () => {
    if (!request) return;
    cancel.mutate(request.id, {
      onSuccess: () => {
        cancel.reset();
        onClose();
        onCancelled?.();
      },
    });
  };

  return (
    <Dialog open={request !== null} onClose={handleClose} maxWidth="xs" fullWidth>
      <DialogTitle>Cancel {request?.id}?</DialogTitle>
      <DialogContent sx={{ display: "flex", flexDirection: "column", gap: 1.5 }}>
        {request && (
          <Typography variant="body2">
            {CLOUD_REQUEST_TYPE_LABELS[request.type]}: {request.target.label} ({request.summary}).
          </Typography>
        )}
        <Typography variant="body2" color="text.secondary">
          The request is withdrawn and no longer waits for approval. Raise it again if you still
          need the change.
        </Typography>
        {cancel.isError && <Alert severity="error">{cancel.error.message}</Alert>}
      </DialogContent>
      <DialogActions>
        <Button onClick={handleClose} disabled={cancel.isPending}>
          Go back
        </Button>
        <Button
          variant="contained"
          color="error"
          onClick={handleConfirm}
          disabled={cancel.isPending}
        >
          Cancel request
        </Button>
      </DialogActions>
    </Dialog>
  );
}
