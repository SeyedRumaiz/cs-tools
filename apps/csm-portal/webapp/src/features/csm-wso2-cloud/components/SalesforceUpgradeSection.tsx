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
  FormControl,
  FormControlLabel,
  FormLabel,
  MenuItem,
  Radio,
  RadioGroup,
  Skeleton,
  TextField,
  Typography,
} from "@wso2/oxygen-ui";
import type { JSX } from "react";
import { SALESFORCE_ACCOUNT_ID_PATTERN } from "@features/csm-wso2-cloud/api/cloudRequestsApi";
import {
  CLOUD_REGIONS,
  CLOUD_REGION_LABELS,
  type CloudRegion,
  type CloudSalesforceAccount,
  type CloudSalesforceStatus,
  type CloudUpgradeKind,
} from "@features/csm-wso2-cloud/types/wso2Cloud";

export interface SalesforceUpgradeSectionProps {
  /** The organisation's Salesforce record and account. */
  status: CloudSalesforceStatus | undefined;
  statusLoading: boolean;
  /** True when Salesforce is not connected (only an internal upgrade can be raised). */
  notConnected: boolean;
  statusFailed: boolean;
  kind: CloudUpgradeKind | undefined;
  onKindChange: (kind: CloudUpgradeKind) => void;
  accountInput: string;
  onAccountInputChange: (value: string) => void;
  onCheck: () => void;
  /** The account the id last checked names: undefined until checked, null when Salesforce has none. */
  checked: CloudSalesforceAccount | null | undefined;
  checking: boolean;
  checkFailed: boolean;
  region: CloudRegion | "";
  onRegionChange: (region: CloudRegion) => void;
}

function accountName(account: CloudSalesforceAccount): string {
  return account.name ? `${account.name} (${account.id})` : account.id;
}

/** What Salesforce says about the organisation today. */
function RecordSummary({ status }: { status: CloudSalesforceStatus }): JSX.Element {
  if (!status.record) {
    return <Typography variant="body2">This organization has no Salesforce record yet.</Typography>;
  }
  if (!status.account) {
    return <Typography variant="body2">This organization has a Salesforce record, but it is not under an account.</Typography>;
  }
  return status.account.hasActiveSubscription ? (
    <Alert severity="success" variant="outlined">
      In Salesforce under {accountName(status.account)}, which has an active subscription.
    </Alert>
  ) : (
    <Alert severity="warning" variant="outlined">
      In Salesforce under {accountName(status.account)}, which has no active subscription.
    </Alert>
  );
}

/** The result of checking an account id. */
function CheckResult({ checked }: { checked: CloudSalesforceAccount | null }): JSX.Element {
  if (checked === null) {
    return <Alert severity="error">Salesforce has no account with this ID.</Alert>;
  }
  if (!checked.hasActiveSubscription) {
    return (
      <Alert severity="error">
        {accountName(checked)} has no active subscription. A customer upgrade needs one: ask the account
        manager to set it up in Salesforce, or raise an internal upgrade.
      </Alert>
    );
  }
  return (
    <Alert severity="success" variant="outlined">
      <Typography variant="body2">{accountName(checked)} has an active subscription.</Typography>
      <Box component="ul" sx={{ m: 0, mt: 0.5, pl: 2.5 }}>
        {checked.activeOpportunities.map((o) => (
          <li key={o.id}>
            <Typography variant="body2">
              {o.name ?? o.id}
              {o.endDate ? `, ends ${o.endDate}` : ""}
            </Typography>
          </li>
        ))}
      </Box>
    </Alert>
  );
}

/**
 * The Salesforce half of an upgrade. A customer upgrade names the customer's
 * Salesforce account, which must have an active subscription, and the
 * organisation's region; once approved, the organisation is linked to that
 * account in Salesforce. An internal upgrade never touches Salesforce. The
 * backend checks the account again when the request is raised and when it is
 * approved, so this is guidance, not the gate.
 */
export default function SalesforceUpgradeSection(props: SalesforceUpgradeSectionProps): JSX.Element {
  const { status, statusLoading, notConnected, statusFailed, kind, accountInput, checked } = props;
  const wellFormed = SALESFORCE_ACCOUNT_ID_PATTERN.test(accountInput.trim());

  return (
    <>
      {statusLoading ? (
        <Skeleton variant="rounded" height={40} />
      ) : notConnected ? (
        <Alert severity="info" variant="outlined">
          Salesforce is not connected yet, so only an internal upgrade can be raised.
        </Alert>
      ) : statusFailed ? (
        <Alert severity="warning" variant="outlined">
          Could not read this organization's Salesforce record. You can still enter the customer's account.
        </Alert>
      ) : status ? (
        <RecordSummary status={status} />
      ) : null}

      <FormControl required>
        <FormLabel id="upgrade-kind-label">Upgrade kind</FormLabel>
        <RadioGroup
          aria-labelledby="upgrade-kind-label"
          value={kind ?? ""}
          onChange={(e) => props.onKindChange(e.target.value as CloudUpgradeKind)}
        >
          <FormControlLabel
            value="customer"
            disabled={notConnected}
            control={<Radio size="small" />}
            label="Customer upgrade: a paying customer, with an active subscription in Salesforce"
          />
          <FormControlLabel
            value="internal"
            control={<Radio size="small" />}
            label="Internal upgrade: Salesforce is not checked or changed"
          />
        </RadioGroup>
      </FormControl>

      {kind === "customer" && (
        <>
          <Box sx={{ display: "flex", gap: 1, alignItems: "flex-start" }}>
            <TextField
              required
              size="small"
              label="Salesforce account ID"
              value={accountInput}
              onChange={(e) => props.onAccountInputChange(e.target.value)}
              error={accountInput.trim() !== "" && !wellFormed}
              helperText="15 or 18 characters, starting with 001."
              sx={{ maxWidth: 320, flex: 1 }}
              slotProps={{ htmlInput: { maxLength: 18 } }}
            />
            <Button variant="outlined" onClick={props.onCheck} disabled={!wellFormed || props.checking} sx={{ mt: 0.25 }}>
              Check
            </Button>
          </Box>
          {props.checking ? (
            <Skeleton variant="rounded" height={40} />
          ) : props.checkFailed ? (
            <Alert severity="error">Could not check this account in Salesforce. Try again later.</Alert>
          ) : checked !== undefined ? (
            <CheckResult checked={checked} />
          ) : null}
          <TextField
            select
            required
            size="small"
            label="WSO2 Cloud region"
            value={props.region}
            onChange={(e) => props.onRegionChange(e.target.value as CloudRegion)}
            helperText="Where the organization runs. Saved on its Salesforce record."
            sx={{ maxWidth: 320 }}
          >
            {CLOUD_REGIONS.map((r) => (
              <MenuItem key={r} value={r}>
                {CLOUD_REGION_LABELS[r]} ({r})
              </MenuItem>
            ))}
          </TextField>
        </>
      )}
    </>
  );
}
