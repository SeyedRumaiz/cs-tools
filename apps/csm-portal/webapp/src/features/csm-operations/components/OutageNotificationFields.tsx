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

import { Box, Checkbox, FormControlLabel, TextField, Typography } from "@wso2/oxygen-ui";
import { type JSX } from "react";

/** Width of outage.impact / outage.state in the database. */
export const OUTAGE_LABEL_MAX_LENGTH = 40;

export interface OutageNotificationValues {
  notifyInternalStakeholders: boolean;
  outageCommunication: boolean;
  impact: string;
  state: string;
}

interface OutageNotificationFieldsProps {
  value: OutageNotificationValues;
  onChange: (next: OutageNotificationValues) => void;
  disabled?: boolean;
}

/**
 * The two email opt-ins and the two values the outage-communication email
 * prints -- ServiceNow's outage form has a checkbox for each email, and its
 * flows only mail for outages someone ticked. Without these an outage created
 * here could never trigger either email. Impact and State are free text:
 * ServiceNow's choice lists for them have not been captured.
 */
export default function OutageNotificationFields({
  value,
  onChange,
  disabled,
}: OutageNotificationFieldsProps): JSX.Element {
  const set = (patch: Partial<OutageNotificationValues>): void => onChange({ ...value, ...patch });
  return (
    <Box sx={{ display: "flex", flexDirection: "column", gap: 1 }}>
      <Typography variant="caption" color="text.secondary">
        Notifications
      </Typography>
      <FormControlLabel
        control={
          <Checkbox
            checked={value.notifyInternalStakeholders}
            onChange={(e) => set({ notifyInternalStakeholders: e.target.checked })}
            disabled={disabled}
          />
        }
        label="Notify internal stakeholders (declared, update and resolved emails)"
      />
      <FormControlLabel
        control={
          <Checkbox
            checked={value.outageCommunication}
            onChange={(e) => set({ outageCommunication: e.target.checked })}
            disabled={disabled}
          />
        }
        label="Outage communication (declaration and resolution emails to SRE)"
      />
      <Box sx={{ display: "flex", gap: 2, flexWrap: "wrap" }}>
        <TextField
          label="Impact"
          size="small"
          value={value.impact}
          onChange={(e) => set({ impact: e.target.value })}
          disabled={disabled}
          inputProps={{ maxLength: OUTAGE_LABEL_MAX_LENGTH }}
          helperText='Shown as "Impact" in the outage communication email.'
          sx={{ flex: "1 1 220px" }}
        />
        <TextField
          label="Current status"
          size="small"
          value={value.state}
          onChange={(e) => set({ state: e.target.value })}
          disabled={disabled}
          inputProps={{ maxLength: OUTAGE_LABEL_MAX_LENGTH }}
          helperText='Shown as "Current Status" in the outage communication email.'
          sx={{ flex: "1 1 220px" }}
        />
      </Box>
    </Box>
  );
}
