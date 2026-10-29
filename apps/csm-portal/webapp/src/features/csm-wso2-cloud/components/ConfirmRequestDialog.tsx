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
import { usePostCloudRequest } from "@features/csm-wso2-cloud/api/usePostCloudRequest";
import type { NewCloudRequest } from "@features/csm-wso2-cloud/types/wso2Cloud";

/**
 * Confirms one danger-zone action and raises it as a request. `request` is
 * null while the dialog is closed.
 */
export default function ConfirmRequestDialog({
  request,
  title,
  body,
  confirmLabel,
  onClose,
  onRaised,
}: {
  request: NewCloudRequest | null;
  title: string;
  body: string;
  confirmLabel: string;
  onClose: () => void;
  onRaised: () => void;
}): JSX.Element {
  const post = usePostCloudRequest();

  const handleConfirm = () => {
    if (!request) return;
    post.mutate(request, {
      onSuccess: () => {
        post.reset();
        onRaised();
      },
    });
  };

  const handleClose = () => {
    if (post.isPending) return;
    post.reset();
    onClose();
  };

  return (
    <Dialog open={request !== null} onClose={handleClose} maxWidth="xs" fullWidth>
      <DialogTitle>{title}</DialogTitle>
      <DialogContent sx={{ display: "flex", flexDirection: "column", gap: 1.5 }}>
        <Typography variant="body2">{body}</Typography>
        <Typography variant="body2" color="text.secondary">
          This raises a request. Nothing changes until it is approved.
        </Typography>
        {post.isError && <Alert severity="error">{post.error.message}</Alert>}
      </DialogContent>
      <DialogActions>
        <Button onClick={handleClose} disabled={post.isPending}>
          Go back
        </Button>
        <Button variant="contained" color="error" onClick={handleConfirm} disabled={post.isPending}>
          {confirmLabel}
        </Button>
      </DialogActions>
    </Dialog>
  );
}
