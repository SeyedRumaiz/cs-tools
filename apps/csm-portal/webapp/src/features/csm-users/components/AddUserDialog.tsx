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
  Checkbox,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControlLabel,
  FormGroup,
  MenuItem,
  TextField,
  Typography,
} from "@wso2/oxygen-ui";
import { useState, type FormEvent, type JSX } from "react";
import { useGetGrantableRoles } from "@features/csm-users/api/useGetGrantableRoles";
import { usePostUser } from "@features/csm-users/api/usePostUser";
import { grantableRoleLabel } from "@features/csm-users/utils/grantableRoleLabels";
import { isPlausibleEmail } from "@features/csm-users/utils/isPlausibleEmail";

export interface AddUserDialogProps {
  open: boolean;
  onClose: () => void;
  /** Called once the user is created, so the caller can e.g. show a toast. */
  onCreated?: (userId: string | undefined) => void;
}

/**
 * The two user types this form can create. entity-service's `user_type` has
 * no plain settable column -- it's derived by a DB trigger from role
 * membership (`recompute_user_type`, migration 0011) -- so picking one here
 * means sending the matching role (`internal`/`external`) in `roles`, not a
 * `type` field on the wire. `external` (not `customer`/`partner`/...) is the
 * role every externally-onboarded contact actually holds; the finer-grained
 * ones are refinements applied elsewhere, not choices this form makes.
 *
 * `external` is disabled for now -- the backend rejects it too (see
 * entity-service's own `requestsExternalUserType`) -- so this list only ever
 * offers one real, selectable choice until that's lifted.
 */
const USER_TYPE_OPTIONS = [
  { value: "internal", label: "Internal (WSO2 staff)", role: "internal", disabled: false },
  {
    value: "external",
    label: "External (customer/partner) — currently unavailable",
    role: "external",
    disabled: true,
  },
] as const;

type NewUserType = (typeof USER_TYPE_OPTIONS)[number]["value"];

const WSO2_EMAIL_DOMAIN = "@wso2.com";

function isWso2Email(email: string): boolean {
  return email.toLowerCase().endsWith(WSO2_EMAIL_DOMAIN);
}

const EMPTY_FORM: { firstName: string; lastName: string; email: string; userType: NewUserType | "" } = {
  firstName: "",
  lastName: "",
  email: "",
  userType: "",
};

/**
 * Admin-only "Add User" form (`POST /users`). Sets the new user's type by
 * granting the matching `internal`/`external` role (see `USER_TYPE_OPTIONS`'s
 * own doc comment) -- unrelated to the "Portal roles" section below, which
 * grants zero or more additional portal permissions via SCIM.
 *
 * "Portal roles" is fetched from `GET /roles/grantable` only while this
 * dialog is open, and is itself admin-only on the backend (`PermAdmin`, the
 * same gate `POST /users` sits behind) -- the UI-side protection is simply
 * that this whole dialog only renders for an admin in the first place (see
 * `CsmUsersPage.tsx`'s `canCreateUser` gate), so no separate check is needed
 * here. A failed fetch is shown as its own error state with a retry action,
 * never silently collapsed to "no roles configured" -- those two cases look
 * identical from an empty array alone, and conflating them would let a
 * transient fetch failure quietly remove an admin's ability to grant any
 * portal role on this user, with nothing on screen explaining why.
 *
 * An Internal user must have a `@wso2.com` email -- entity-service enforces
 * this as the real constraint (a non-wso2.com address must never resolve to
 * `user_type = INTERNAL`); this form blocks the same case up front so the
 * admin sees it immediately rather than after a round trip.
 */
export default function AddUserDialog({ open, onClose, onCreated }: AddUserDialogProps): JSX.Element {
  const [form, setForm] = useState(EMPTY_FORM);
  const [selectedGrantRoles, setSelectedGrantRoles] = useState<string[]>([]);
  const { mutate, isPending, error, reset } = usePostUser();
  const {
    data: grantableRoles,
    isLoading: grantableRolesLoading,
    isError: grantableRolesErrored,
    refetch: refetchGrantableRoles,
  } = useGetGrantableRoles(open);

  const toggleGrantRole = (key: string, checked: boolean): void => {
    setSelectedGrantRoles((prev) => (checked ? [...prev, key] : prev.filter((k) => k !== key)));
  };

  const handleClose = (): void => {
    if (isPending) return;
    setForm(EMPTY_FORM);
    setSelectedGrantRoles([]);
    reset();
    onClose();
  };

  const trimmedEmail = form.email.trim();
  const hasName = form.firstName.trim() !== "" || form.lastName.trim() !== "";
  const emailValid = isPlausibleEmail(trimmedEmail);
  const internalEmailViolation = form.userType === "internal" && emailValid && !isWso2Email(trimmedEmail);
  const canSubmit = hasName && emailValid && form.userType !== "" && !internalEmailViolation;

  const handleSubmit = (e: FormEvent<HTMLFormElement>): void => {
    e.preventDefault();
    if (!canSubmit) return;
    const selected = USER_TYPE_OPTIONS.find((o) => o.value === form.userType);
    mutate(
      {
        firstName: form.firstName.trim() || undefined,
        lastName: form.lastName.trim() || undefined,
        email: trimmedEmail,
        roles: selected ? [selected.role] : undefined,
        grantRoles: selectedGrantRoles.length > 0 ? selectedGrantRoles : undefined,
      },
      {
        onSuccess: (created) => {
          setForm(EMPTY_FORM);
          setSelectedGrantRoles([]);
          onCreated?.(created.id);
          onClose();
        },
      },
    );
  };

  return (
    <Dialog open={open} onClose={handleClose} maxWidth="xs" fullWidth>
      <DialogTitle>Add user</DialogTitle>
      <Box component="form" onSubmit={handleSubmit} noValidate>
        <DialogContent>
          <Box sx={{ display: "flex", flexDirection: "column", gap: 2, pt: 0.5 }}>
            {error && (
              <Typography variant="body2" color="error">
                {error.message || "Failed to create the user."}
              </Typography>
            )}
            <TextField
              label="First name"
              value={form.firstName}
              onChange={(e) => setForm({ ...form, firstName: e.target.value })}
              fullWidth
              disabled={isPending}
              autoFocus
            />
            <TextField
              label="Last name"
              value={form.lastName}
              onChange={(e) => setForm({ ...form, lastName: e.target.value })}
              fullWidth
              disabled={isPending}
            />
            <TextField
              label="Email"
              type="email"
              value={form.email}
              onChange={(e) => setForm({ ...form, email: e.target.value })}
              fullWidth
              disabled={isPending}
              required
              error={(form.email.trim() !== "" && !emailValid) || internalEmailViolation}
              helperText={
                form.email.trim() !== "" && !emailValid
                  ? "Enter a valid email address."
                  : internalEmailViolation
                    ? `An internal user must have a ${WSO2_EMAIL_DOMAIN} email address.`
                    : undefined
              }
            />
            <TextField
              select
              label="User type"
              value={form.userType}
              onChange={(e) => setForm({ ...form, userType: e.target.value as NewUserType })}
              fullWidth
              disabled={isPending}
              required
            >
              {USER_TYPE_OPTIONS.map((option) => (
                <MenuItem key={option.value} value={option.value} disabled={option.disabled}>
                  {option.label}
                </MenuItem>
              ))}
            </TextField>
            {!hasName && (
              <Typography variant="caption" color="text.secondary">
                At least a first or last name is required.
              </Typography>
            )}
            {grantableRolesLoading && (
              <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
                <CircularProgress size={16} />
                <Typography variant="body2" color="text.secondary">
                  Loading portal roles…
                </Typography>
              </Box>
            )}
            {grantableRolesErrored && (
              <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
                <Typography variant="body2" color="error">
                  Failed to load portal roles.
                </Typography>
                <Button type="button" size="small" onClick={() => refetchGrantableRoles()}>
                  Retry
                </Button>
              </Box>
            )}
            {grantableRoles && grantableRoles.length > 0 && (
              <Box>
                <Typography variant="subtitle2" sx={{ mb: 0.5 }}>
                  Portal roles
                </Typography>
                <FormGroup>
                  {grantableRoles.map((role) => (
                    <FormControlLabel
                      key={role.key}
                      control={
                        <Checkbox
                          checked={selectedGrantRoles.includes(role.key)}
                          onChange={(e) => toggleGrantRole(role.key, e.target.checked)}
                          disabled={isPending}
                        />
                      }
                      label={grantableRoleLabel(role.key)}
                    />
                  ))}
                </FormGroup>
              </Box>
            )}
          </Box>
        </DialogContent>
        <DialogActions>
          <Button type="button" color="inherit" onClick={handleClose} disabled={isPending}>
            Cancel
          </Button>
          <Button
            type="submit"
            variant="contained"
            disabled={!canSubmit || isPending}
            loading={isPending}
          >
            Add user
          </Button>
        </DialogActions>
      </Box>
    </Dialog>
  );
}
