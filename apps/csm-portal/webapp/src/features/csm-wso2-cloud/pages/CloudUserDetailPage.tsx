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

import { Alert, Box, Chip, Link, Skeleton, Typography } from "@wso2/oxygen-ui";
import { useState, type JSX } from "react";
import { Link as RouterLink, useLocation, useParams } from "react-router";
import { useNavTransition } from "@hooks/useNavTransition";
import { useGetCloudRequests } from "@features/csm-wso2-cloud/api/useGetCloudRequests";
import { useGetCloudUser } from "@features/csm-wso2-cloud/api/useGetCloudUser";
import ConfirmRequestDialog from "@features/csm-wso2-cloud/components/ConfirmRequestDialog";
import UserStatusChip from "@features/csm-wso2-cloud/components/UserStatusChip";
import {
  BackButton,
  DangerZone,
  MetaCell,
  MetaGrid,
  SampleDataNotice,
  SectionCard,
} from "@features/csm-wso2-cloud/components/Wso2CloudCommon";
import { formatDateTime } from "@features/csm-wso2-cloud/utils/format";
import { WSO2_CLOUD_BASE } from "@features/csm-wso2-cloud/config/wso2CloudNavItems";
import {
  CLOUD_REQUEST_TYPE_LABELS,
  type CloudRequestType,
  type CloudUser,
  type NewCloudRequest,
} from "@features/csm-wso2-cloud/types/wso2Cloud";

interface PendingAction {
  request: NewCloudRequest;
  title: string;
  body: string;
  confirmLabel: string;
}

function userAction(
  user: CloudUser,
  type: CloudRequestType,
  title: string,
  body: string,
  confirmLabel: string,
): PendingAction {
  return {
    request: {
      type,
      target: { kind: "user", id: user.id, label: user.email },
      summary: `${CLOUD_REQUEST_TYPE_LABELS[type]} ${user.email}`,
    },
    title,
    body,
    confirmLabel,
  };
}

/**
 * One user: their account details, sign-in status and organisations, and the
 * lock / disable / delete actions. Lock and disable toggle: on a locked user
 * the action offered is Unlock, on a disabled one Enable.
 */
export default function CloudUserDetailPage(): JSX.Element {
  const { userId } = useParams<{ userId: string }>();
  const navigate = useNavTransition();
  const location = useLocation();
  const backPath =
    (location.state as { from?: string } | undefined)?.from ?? `${WSO2_CLOUD_BASE}/users`;
  const here = `${location.pathname}${location.search}`;

  const { data: user, isLoading, isError } = useGetCloudUser(userId);
  const { data: requests } = useGetCloudRequests();
  const [action, setAction] = useState<PendingAction | null>(null);
  const [raised, setRaised] = useState(false);

  const back = <BackButton onClick={() => navigate(backPath)} />;

  if (isLoading) {
    return (
      <Box sx={{ display: "flex", flexDirection: "column", gap: 2 }}>
        {back}
        <Skeleton variant="rounded" width={280} height={32} />
        <Skeleton variant="rounded" height={160} />
      </Box>
    );
  }

  if (isError || !user) {
    return (
      <Box sx={{ display: "flex", flexDirection: "column", gap: 2 }}>
        {back}
        <Typography variant="h5">User not found</Typography>
        <Typography variant="body2" color="text.secondary">
          No user with id <code>{userId}</code>.
        </Typography>
      </Box>
    );
  }

  const pending = (requests ?? []).filter(
    (r) => r.status === "pending" && r.target.kind === "user" && r.target.id === user.id,
  );

  return (
    <Box sx={{ display: "flex", flexDirection: "column", gap: 2.5 }}>
      {back}
      <SampleDataNotice />

      <Box>
        <Box sx={{ display: "flex", alignItems: "center", gap: 1.5, flexWrap: "wrap" }}>
          <Typography variant="h5">{user.email}</Typography>
          <UserStatusChip user={user} />
        </Box>
        <Typography variant="body2" color="text.secondary" sx={{ fontFamily: "monospace" }}>
          {user.id}
        </Typography>
      </Box>

      {raised && (
        <Alert severity="success" onClose={() => setRaised(false)}>
          Request raised. Follow it under{" "}
          <Link component={RouterLink} to={`${WSO2_CLOUD_BASE}/requests?tab=mine`}>
            My requests
          </Link>
          .
        </Alert>
      )}
      {pending.length > 0 && (
        <Alert severity="info">
          Waiting for approval: {pending.map((r) => CLOUD_REQUEST_TYPE_LABELS[r.type]).join(", ")}.
        </Alert>
      )}

      <SectionCard title="User details">
        <MetaGrid>
          <MetaCell label="User ID (SCIM)" mono>
            {user.id}
          </MetaCell>
          <MetaCell label="Username">{user.username}</MetaCell>
          <MetaCell label="Email">{user.email}</MetaCell>
          <MetaCell label="Created">{formatDateTime(user.createdOn)}</MetaCell>
          <MetaCell label="Last modified">{formatDateTime(user.lastModifiedOn)}</MetaCell>
        </MetaGrid>
      </SectionCard>

      <SectionCard title="Account status">
        <MetaGrid>
          <MetaCell label="Locked">{user.locked ? "Yes" : "No"}</MetaCell>
          <MetaCell label="Locked reason">{user.lockedReason ?? "—"}</MetaCell>
          <MetaCell label="Disabled">{user.disabled ? "Yes" : "No"}</MetaCell>
          <MetaCell label="Last sign-in">{formatDateTime(user.lastLoginOn)}</MetaCell>
          <MetaCell label="Failed sign-in attempts">{user.failedLoginAttempts}</MetaCell>
        </MetaGrid>
      </SectionCard>

      <SectionCard title="Organizations">
        {user.organizations.length === 0 ? (
          <Typography variant="body2" color="text.secondary">
            Not a member of any organization.
          </Typography>
        ) : (
          <Box sx={{ display: "flex", gap: 1, flexWrap: "wrap" }}>
            {user.organizations.map((o) => (
              <Chip
                key={o.id}
                size="small"
                variant="outlined"
                label={o.handle}
                clickable
                component={RouterLink}
                to={`${WSO2_CLOUD_BASE}/organizations/${o.id}`}
                state={{ from: here }}
              />
            ))}
          </Box>
        )}
      </SectionCard>

      <DangerZone
        actions={[
          user.locked
            ? {
                title: "Unlock user",
                description: "Lets the user sign in again.",
                buttonLabel: "Unlock",
                onClick: () =>
                  setAction(
                    userAction(user, "unlock_user", `Unlock ${user.email}?`, "Once approved, the user can sign in again.", "Request unlock"),
                  ),
              }
            : {
                title: "Lock user",
                description: "Once locked, the user can no longer sign in.",
                buttonLabel: "Lock",
                onClick: () =>
                  setAction(
                    userAction(user, "lock_user", `Lock ${user.email}?`, "Once approved, the user can no longer sign in.", "Request lock"),
                  ),
              },
          user.disabled
            ? {
                title: "Enable user",
                description: "Gives the user access to the system again.",
                buttonLabel: "Enable",
                onClick: () =>
                  setAction(
                    userAction(user, "enable_user", `Enable ${user.email}?`, "Once approved, the user can access the system again.", "Request enable"),
                  ),
              }
            : {
                title: "Disable user",
                description: "Once disabled, the user cannot access the system.",
                buttonLabel: "Disable",
                onClick: () =>
                  setAction(
                    userAction(user, "disable_user", `Disable ${user.email}?`, "Once approved, the user cannot access the system.", "Request disable"),
                  ),
              },
          {
            title: "Delete user",
            description: "Removes the user and their membership of every organization.",
            buttonLabel: "Delete",
            onClick: () =>
              setAction(
                userAction(user, "delete_user", `Delete ${user.email}?`, "Once approved, the user is removed from every organization they belong to.", "Request deletion"),
              ),
          },
        ]}
      />

      <ConfirmRequestDialog
        request={action?.request ?? null}
        title={action?.title ?? ""}
        body={action?.body ?? ""}
        confirmLabel={action?.confirmLabel ?? ""}
        onClose={() => setAction(null)}
        onRaised={() => {
          setAction(null);
          setRaised(true);
        }}
      />
    </Box>
  );
}
