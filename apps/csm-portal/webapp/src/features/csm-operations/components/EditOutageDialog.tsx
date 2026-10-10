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
  Box,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControl,
  InputLabel,
  MenuItem,
  Select,
  TextField,
  Typography,
} from "@wso2/oxygen-ui";
import { useMemo, useState, type JSX } from "react";
import { useSearchConfigurationItems } from "@api/useSearchConfigurationItems";
import { useSearchIncidentsForSelect } from "@features/csm-operations/api/useSearchIncidentsForSelect";
import { useGetOutageMetadata } from "@features/csm-operations/api/useOutages";
import AsyncEntitySelect from "@components/AsyncEntitySelect";
import AsyncEntityMultiSelect from "@components/AsyncEntityMultiSelect";
import OutageNotificationFields, {
  type OutageNotificationValues,
} from "@features/csm-operations/components/OutageNotificationFields";
import OutagePublicationNotice from "@features/csm-operations/components/OutagePublicationNotice";
import { outageTypeLabel } from "@features/csm-operations/utils/outages";
import type {
  BeConfigurationItem,
  BeIncident,
  BeOutageDetail,
  BeOutageType,
  BePatchOutagePayload,
} from "@api/backend/types";

interface EditOutageDialogProps {
  outage: BeOutageDetail;
  isSaving: boolean;
  saveError?: string | null;
  onClose: () => void;
  onSave: (patch: BePatchOutagePayload) => void;
}

function configurationItemLabel(c: BeConfigurationItem): string {
  return c.name || c.id;
}

function incidentSearchLabel(i: BeIncident): string {
  return [i.number, i.subject].filter(Boolean).join(" — ") || i.id || "";
}

const OUTAGE_TYPES: BeOutageType[] = ["outage", "degradation", "planned"];

/**
 * Edit an outage's classification and links: `type`, `shortDescription`,
 * `configurationItemId`, `incidentId`. `begin`/`end` are deliberately not
 * editable here — `end` is the dedicated close/reopen action on the detail
 * page (see `CloseOutageDialog`) and `begin` is left as ServiceNow set it on
 * create, to keep this dialog from becoming a second, less-visible way to
 * change what the close action already owns.
 */
export default function EditOutageDialog({
  outage,
  isSaving,
  saveError,
  onClose,
  onSave,
}: EditOutageDialogProps): JSX.Element {
  const { data: metadata } = useGetOutageMetadata();

  const initialType = (outage.type as BeOutageType) ?? "";
  const initialShortDescription = outage.shortDescription ?? "";
  const initialConfigurationItemId = outage.configurationItem?.id ?? "";
  const initialIncidentId = outage.incident?.id ?? "";

  const [type, setType] = useState<BeOutageType | "">(initialType);
  const [shortDescription, setShortDescription] = useState(initialShortDescription);
  const [configurationItemId, setConfigurationItemId] = useState(initialConfigurationItemId);
  const [incidentId, setIncidentId] = useState(initialIncidentId);
  // Already publishing (or the caller already acknowledged once) counts as
  // pre-acknowledged so re-saving unrelated fields on an already-linked,
  // already-public outage doesn't re-block on a checkbox that adds nothing.
  const [acknowledged, setAcknowledged] = useState(outage.publishesToStatusPage);
  const initialNotifications = useMemo<OutageNotificationValues>(
    () => ({
      notifyInternalStakeholders: outage.notifyInternalStakeholders ?? false,
      outageCommunication: outage.outageCommunication ?? false,
      impact: outage.impact ?? "",
      state: outage.state ?? "",
    }),
    [outage.notifyInternalStakeholders, outage.outageCommunication, outage.impact, outage.state],
  );
  const [notifications, setNotifications] = useState<OutageNotificationValues>(initialNotifications);
  const initialAffected = useMemo(
    () => (outage.affectedConfigurationItems ?? []).map((ci) => ci.id),
    [outage.affectedConfigurationItems],
  );
  const affectedLabels = useMemo(
    () => Object.fromEntries((outage.affectedConfigurationItems ?? []).map((ci) => [ci.id, ci.name || ci.id])),
    [outage.affectedConfigurationItems],
  );
  const [affectedIds, setAffectedIds] = useState<string[]>(initialAffected);

  const isShortDescriptionValid = shortDescription.trim().length > 0;
  const configurationItemChanged = configurationItemId !== initialConfigurationItemId;
  // Only ADDED affected CIs can newly put the outage on the status page.
  const addedAffected = affectedIds.filter((id) => !initialAffected.includes(id));
  const affectedChanged =
    addedAffected.length > 0 || initialAffected.some((id) => !affectedIds.includes(id));
  const publicationMayChange = (configurationItemChanged && !!configurationItemId) || addedAffected.length > 0;
  const needsAcknowledgement = publicationMayChange && !acknowledged;

  const patch = useMemo<BePatchOutagePayload>(() => {
    const next: BePatchOutagePayload = {};
    if (type !== initialType && type) next.type = type;
    if (shortDescription !== initialShortDescription && isShortDescriptionValid) {
      next.shortDescription = shortDescription.trim();
    }
    if (configurationItemChanged) {
      next.configurationItemId = configurationItemId || null;
    }
    if (incidentId !== initialIncidentId) next.incidentId = incidentId || null;
    if (affectedChanged) next.affectedConfigurationItemIds = affectedIds;
    if (publicationMayChange) {
      next.acknowledgePublicPublication = acknowledged;
    }
    if (notifications.notifyInternalStakeholders !== initialNotifications.notifyInternalStakeholders) {
      next.notifyInternalStakeholders = notifications.notifyInternalStakeholders;
    }
    if (notifications.outageCommunication !== initialNotifications.outageCommunication) {
      next.outageCommunication = notifications.outageCommunication;
    }
    // Trimmed on both sides; an emptied field is sent as "" so the backend
    // clears it rather than leaving the old value in the email.
    if (notifications.impact.trim() !== initialNotifications.impact.trim()) {
      next.impact = notifications.impact.trim();
    }
    if (notifications.state.trim() !== initialNotifications.state.trim()) {
      next.state = notifications.state.trim();
    }
    return next;
  }, [
    type,
    initialType,
    shortDescription,
    initialShortDescription,
    isShortDescriptionValid,
    configurationItemChanged,
    configurationItemId,
    incidentId,
    initialIncidentId,
    acknowledged,
    notifications,
    initialNotifications,
    affectedChanged,
    affectedIds,
    publicationMayChange,
  ]);

  const hasChanges = Object.keys(patch).length > 0;
  const canSave = hasChanges && isShortDescriptionValid && !needsAcknowledgement && !isSaving;

  return (
    <Dialog open onClose={onClose} maxWidth="sm" fullWidth>
      <DialogTitle>Edit outage</DialogTitle>
      <DialogContent dividers>
        <Box sx={{ display: "flex", flexDirection: "column", gap: 2, pt: 0.5 }}>
          {saveError && (
            <Alert severity="error" sx={{ width: "100%" }}>
              {saveError}
            </Alert>
          )}

          <TextField
            label="Short description"
            value={shortDescription}
            onChange={(e) => setShortDescription(e.target.value)}
            fullWidth
            required
            error={!isShortDescriptionValid}
            helperText={
              !isShortDescriptionValid
                ? "Required — cannot be cleared."
                : undefined
            }
            disabled={isSaving}
          />

          <FormControl fullWidth size="small" disabled={isSaving}>
            <InputLabel id="outage-edit-type-label" shrink>
              Type
            </InputLabel>
            <Select
              labelId="outage-edit-type-label"
              label="Type"
              value={type}
              displayEmpty
              onChange={(e) => setType(e.target.value as BeOutageType)}
            >
              {(metadata?.types.map((t) => t.value as BeOutageType) ?? OUTAGE_TYPES).map((t) => (
                <MenuItem key={t} value={t}>
                  {outageTypeLabel(t)}
                </MenuItem>
              ))}
            </Select>
          </FormControl>

          <AsyncEntitySelect<BeConfigurationItem>
            id="outage-edit-configuration-item"
            label="Configuration item"
            placeholder="Search configuration items…"
            value={configurationItemId}
            onChange={(next) => {
              setConfigurationItemId(next);
              if (!next) setAcknowledged(false);
            }}
            disabled={isSaving}
            useSearch={useSearchConfigurationItems}
            getId={(c) => c.id}
            getLabel={configurationItemLabel}
            knownLabel={outage.configurationItem?.name}
            helperText="Must be a Service offering — decides whether this outage is publicly visible."
          />

          <AsyncEntitySelect<BeIncident>
            id="outage-edit-incident"
            label="Related incident"
            placeholder="Search incidents…"
            value={incidentId}
            onChange={setIncidentId}
            disabled={isSaving}
            useSearch={useSearchIncidentsForSelect}
            getId={(i) => i.id!}
            getLabel={incidentSearchLabel}
            knownLabel={outage.incident?.number}
          />

          <AsyncEntityMultiSelect<BeConfigurationItem>
            id="outage-edit-affected-configuration-items"
            label="Affected configuration items"
            placeholder="Search service offerings…"
            values={affectedIds}
            onChange={setAffectedIds}
            disabled={isSaving}
            useSearch={useSearchConfigurationItems}
            getId={(c) => c.id}
            getLabel={configurationItemLabel}
            knownLabels={affectedLabels}
            helperText="Other service offerings this outage affects. Each one's status-page monitor and availability reflect the outage."
          />

          <OutageNotificationFields
            value={notifications}
            onChange={setNotifications}
            disabled={isSaving}
          />

          {(configurationItemChanged || addedAffected.length > 0) && (
            <OutagePublicationNotice
              hasConfigurationItem={publicationMayChange}
              monitoredClouds={metadata?.statusPageClouds}
              acknowledged={acknowledged}
              onAcknowledgedChange={setAcknowledged}
              disabled={isSaving}
            />
          )}

          {!hasChanges && (
            <Typography variant="caption" color="text.secondary">
              No changes yet.
            </Typography>
          )}
        </Box>
      </DialogContent>
      <DialogActions>
        <Button color="inherit" onClick={onClose} disabled={isSaving}>
          Cancel
        </Button>
        <Button variant="contained" onClick={() => onSave(patch)} disabled={!canSave}>
          {isSaving ? "Saving…" : "Save"}
        </Button>
      </DialogActions>
    </Dialog>
  );
}
