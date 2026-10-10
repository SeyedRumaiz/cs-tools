/**
 * Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

import { useEffect, useRef, useState, type JSX } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { BackendApiError } from "@api/backend/client";
import { useErrorBanner } from "@context/error-banner/ErrorBannerContext";
import { useDeletePagingContact, usePutPagingContact, useTestPagingContact } from "../api/usePagingChain";
import type { PagingChainMember } from "../types";
import { testCallAwaited } from "../utils/pagingPhone";
import PagingPhoneDialog from "./PagingPhoneDialog";
import type { PagingPhoneControls } from "./PhoneChip";

/** The server's own words for a refusal the reader can act on. */
function refusal(err: unknown): string | undefined {
  return err instanceof BackendApiError && (err.status === 403 || err.status === 409) && err.message
    ? err.message
    : undefined;
}

function saveError(err: unknown): string {
  if (err instanceof BackendApiError && err.status === 400) return "That number was not accepted. Check it and try again.";
  return refusal(err) ?? "The paging number was not saved. Try again.";
}

function testError(err: unknown): string {
  if (err instanceof BackendApiError && err.status === 429) {
    return err.message || "This number was tested less than 2 minutes ago. Try again shortly.";
  }
  if (err instanceof BackendApiError && err.status === 409) return "There is no paging number to test.";
  return refusal(err) ?? "The test call could not be started. Try again.";
}

/**
 * The paging-number actions of the Case Paging tab -- add, change, remove,
 * test call -- and the one dialog they open. `members` are the chain members
 * on screen and `readAt` when they were read (ms), which decides whether a
 * pending test call is still awaited.
 */
export function usePagingPhoneControls(
  members: readonly PagingChainMember[],
  readAt: number,
): { controls: PagingPhoneControls; dialog: JSX.Element | null } {
  const put = usePutPagingContact();
  const del = useDeletePagingContact();
  const test = useTestPagingContact();
  const qc = useQueryClient();
  const { showError } = useErrorBanner();
  const [open, setOpen] = useState<{ mode: "edit" | "remove"; member: PagingChainMember } | null>(null);
  const [error, setError] = useState<string | undefined>();

  // A test call's result changes readiness too; the chain polls for it, the
  // strip does not, so it is redrawn when the last awaited call settles.
  const anyPending = members.some((m) => m.pagingPhone?.lastTestStatus === "pending");
  const wasPending = useRef(anyPending);
  useEffect(() => {
    if (wasPending.current && !anyPending) {
      void qc.invalidateQueries({ queryKey: ["team-schedule", "paging-readiness"] });
    }
    wasPending.current = anyPending;
  }, [anyPending, qc]);

  const close = () => {
    setOpen(null);
    setError(undefined);
  };

  const controls: PagingPhoneControls = {
    edit: (member) => {
      setError(undefined);
      setOpen({ mode: "edit", member });
    },
    remove: (member) => {
      setError(undefined);
      setOpen({ mode: "remove", member });
    },
    test: (member) =>
      test.mutate(member.userId, {
        onError: (err) => showError(testError(err), err),
      }),
    calling: (member) =>
      (test.isPending && test.variables === member.userId) || testCallAwaited(member, readAt),
  };

  const dialog = open ? (
    <PagingPhoneDialog
      key={`${open.mode}-${open.member.userId}`}
      mode={open.mode}
      member={open.member}
      busy={put.isPending || del.isPending}
      error={error}
      onClose={close}
      onSave={(phone) =>
        put.mutate(
          { userId: open.member.userId, phone },
          { onSuccess: close, onError: (err) => setError(saveError(err)) },
        )
      }
      onRemove={() =>
        del.mutate(open.member.userId, {
          onSuccess: close,
          onError: (err) => setError(refusal(err) ?? "The paging number was not removed. Try again."),
        })
      }
    />
  ) : null;

  return { controls, dialog };
}
