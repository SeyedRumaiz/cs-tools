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
  Checkbox,
  FormControlLabel,
  MenuItem,
  Skeleton,
  TextField,
  Typography,
} from "@wso2/oxygen-ui";
import { useState, type JSX } from "react";
import { useLocation, useParams } from "react-router";
import { useNavTransition } from "@hooks/useNavTransition";
import { useGetCloudOrganization } from "@features/csm-wso2-cloud/api/useGetCloudOrganization";
import { useGetCloudPlans } from "@features/csm-wso2-cloud/api/useGetCloudPlans";
import { useGetCloudRegions } from "@features/csm-wso2-cloud/api/useGetCloudRegions";
import { useGetCloudSubscriptions } from "@features/csm-wso2-cloud/api/useGetCloudSubscriptions";
import {
  useGetCloudOrganizationSalesforce,
  useGetSalesforceAccount,
} from "@features/csm-wso2-cloud/api/useGetCloudSalesforce";
import SalesforceUpgradeSection from "@features/csm-wso2-cloud/components/SalesforceUpgradeSection";
import { usePostCloudRequest } from "@features/csm-wso2-cloud/api/usePostCloudRequest";
import {
  BackButton,
  SampleDataNotice,
  SectionCard,
  TierChip,
} from "@features/csm-wso2-cloud/components/Wso2CloudCommon";
import { WSO2_CLOUD_BASE } from "@features/csm-wso2-cloud/config/wso2CloudNavItems";
import { subscriptionBlockedReason } from "@features/csm-wso2-cloud/utils/subscriptions";
import {
  CLOUD_BILLING_PERIOD_LABELS,
  CLOUD_REGION_LABELS,
  CLOUD_REGIONS,
  type CloudBillingPeriod,
  type CloudOrganization,
  type CloudRegion,
  type CloudUpgradeKind,
} from "@features/csm-wso2-cloud/types/wso2Cloud";

/** The status read by a backend error, or undefined for any other error. */
function errorStatus(error: unknown): number | undefined {
  const status = (error as { status?: unknown } | null)?.status;
  return typeof status === "number" ? status : undefined;
}

/**
 * Upgrade one of an organisation's subscriptions to an Enterprise plan billed
 * by wire transfer. An internal upgrade is raised for someone else to approve;
 * nothing changes in WSO2 Cloud until then. A customer upgrade needs no
 * approval (the customer's active subscription is the proof): the backend
 * applies it at once (creates the billing account, changes the plan, links the
 * organisation in Salesforce), and the page then opens the request.
 *
 * The region comes first: WSO2 Cloud runs a billing API per region, so the
 * subscriptions are read from, and the upgrade applied through, the region the
 * organisation is in. It is prefilled from the organisation's Salesforce record
 * when it has one, and preselected when this environment has a single region.
 *
 * The Salesforce section says whether it is a customer or an internal
 * upgrade. A customer upgrade names the customer's Salesforce account (it must
 * have an active subscription); when the organisation is already under an
 * account with an active subscription, that is the default. Once approved, the
 * backend links the organisation to that account in Salesforce.
 */
export default function CloudOrganizationUpgradePage(): JSX.Element {
  const { organizationId } = useParams<{ organizationId: string }>();
  const navigate = useNavTransition();
  const location = useLocation();
  const backPath =
    (location.state as { from?: string } | undefined)?.from ??
    `${WSO2_CLOUD_BASE}/organizations/${organizationId}`;
  const { data: org, isLoading } = useGetCloudOrganization(organizationId);

  const back = <BackButton onClick={() => navigate(backPath)} />;

  if (isLoading) {
    return (
      <Box sx={{ display: "flex", flexDirection: "column", gap: 2 }}>
        {back}
        <Skeleton variant="rounded" height={320} />
      </Box>
    );
  }
  if (!org) {
    return (
      <Box sx={{ display: "flex", flexDirection: "column", gap: 2 }}>
        {back}
        <Typography variant="h5">Organization not found</Typography>
      </Box>
    );
  }

  // Keyed on the organisation so the form starts empty for each one.
  return <UpgradeForm key={org.id} org={org} back={back} backPath={backPath} />;
}

function UpgradeForm({
  org,
  back,
  backPath,
}: {
  org: CloudOrganization;
  back: JSX.Element;
  backPath: string;
}): JSX.Element {
  const navigate = useNavTransition();
  const post = usePostCloudRequest();
  const regions = useGetCloudRegions();
  const plans = useGetCloudPlans();
  const [subscriptionId, setSubscriptionId] = useState("");
  const [planCode, setPlanCode] = useState("");
  const [billingPeriod, setBillingPeriod] = useState<CloudBillingPeriod | "">("");
  const [notifyCustomer, setNotifyCustomer] = useState(false);
  const [reason, setReason] = useState("");

  // Salesforce. Each choice is undefined until the user makes it, and reads
  // what the organisation's Salesforce record says until then.
  const salesforce = useGetCloudOrganizationSalesforce(org.id);
  const salesforceNotConnected = errorStatus(salesforce.error) === 503;
  const linkedAccount = salesforce.data?.account ?? null;
  const recordRegion = CLOUD_REGIONS.find((r) => r === salesforce.data?.record?.region?.toLowerCase());
  const [kindChoice, setKindChoice] = useState<CloudUpgradeKind>();
  const [accountInput, setAccountInput] = useState<string>();
  const [checkedId, setCheckedId] = useState<string>();
  const [regionChoice, setRegionChoice] = useState<CloudRegion>();
  // The region the organisation is in: chosen, else its Salesforce record's (when this
  // environment has it), else the only region there is.
  const availableRegions = regions.data ?? [];
  const recordRegionHere = recordRegion && availableRegions.includes(recordRegion) ? recordRegion : undefined;
  const region =
    regionChoice ?? recordRegionHere ?? (availableRegions.length === 1 ? availableRegions[0] : undefined);
  const regionFromRecord = !regionChoice && !!recordRegionHere;
  // With no regions WSO2 Cloud is not connected: the subscriptions call says so.
  const subscriptions = useGetCloudSubscriptions(
    org.id,
    region,
    regions.isSuccess && (availableRegions.length === 0 || !!region),
  );
  const kind = kindChoice ?? (linkedAccount?.hasActiveSubscription ? "customer" : undefined);
  const accountValue = accountInput ?? linkedAccount?.id ?? "";
  // The prefilled account counts as checked: the organisation's status already read it.
  const accountToCheck = checkedId ?? (accountInput === undefined ? linkedAccount?.id : undefined);
  const usePrefilled = !!linkedAccount && accountToCheck === linkedAccount.id;
  const accountCheck = useGetSalesforceAccount(kind === "customer" && !usePrefilled ? accountToCheck : undefined);
  const checkedAccount = usePrefilled ? linkedAccount : accountToCheck ? accountCheck.data : undefined;
  const salesforceReady = kind === "internal" || (kind === "customer" && !!checkedAccount?.hasActiveSubscription);

  const notConnected = errorStatus(subscriptions.error) === 503;
  const notInRegion = errorStatus(subscriptions.error) === 404;
  const subs = subscriptions.data ?? [];
  const subscription = subs.find((s) => s.id === subscriptionId);
  // The plans of the chosen subscription's product, without the one it is already on.
  const productPlans = subscription
    ? (plans.data?.[subscription.product] ?? []).filter((p) => p.code !== subscription.planCode)
    : [];
  const canSubmit =
    !!region &&
    !!subscription &&
    !subscriptionBlockedReason(subscription) &&
    !!planCode &&
    salesforceReady &&
    !post.isPending;

  const chooseSubscription = (id: string) => {
    setSubscriptionId(id);
    setPlanCode("");
  };

  // Another region is another billing API: the subscriptions start again.
  const chooseRegion = (r: CloudRegion) => {
    setRegionChoice(r);
    chooseSubscription("");
  };

  const handleSubmit = () => {
    if (!subscription || !region) return;
    post.mutate(
      {
        type: "upgrade_organization",
        target: { kind: "organization", id: org.id, label: org.handle },
        reason,
        upgrade: {
          product: subscription.product,
          subscriptionId: subscription.id,
          planCode,
          previousPlanCode: subscription.planCode || undefined,
          billingPeriod: billingPeriod || undefined,
          notifyCustomer,
          kind: kind as CloudUpgradeKind,
          salesforceAccountId: kind === "customer" ? checkedAccount?.id : undefined,
          region,
        },
      },
      {
        onSuccess: (created) =>
          navigate(
            kind === "customer"
              ? `${WSO2_CLOUD_BASE}/requests/${created.id}`
              : `${WSO2_CLOUD_BASE}/requests?tab=mine`,
          ),
      },
    );
  };

  return (
    <Box sx={{ display: "flex", flexDirection: "column", gap: 2.5, maxWidth: 880 }}>
      {back}
      <SampleDataNotice />

      <Box>
        <Typography variant="h5">Upgrade {org.handle}</Typography>
        <Box sx={{ display: "flex", alignItems: "center", gap: 1, mt: 0.5 }}>
          <Typography variant="body2" color="text.secondary">
            Current tier
          </Typography>
          <TierChip tier={org.tier} />
        </Box>
      </Box>

      {regions.isLoading ? (
        <Skeleton variant="rounded" height={96} />
      ) : regions.isError ? (
        <Alert severity="error">Could not load this environment's WSO2 Cloud regions. Try again later.</Alert>
      ) : availableRegions.length > 0 ? (
        <SectionCard title="WSO2 Cloud region">
          <TextField
            select
            required
            size="small"
            label="Region"
            value={region ?? ""}
            onChange={(e) => chooseRegion(e.target.value as CloudRegion)}
            disabled={availableRegions.length === 1}
            helperText={
              availableRegions.length === 1
                ? "This environment has one WSO2 Cloud region."
                : regionFromRecord
                  ? "From the organization's Salesforce record. Change it if that is wrong."
                  : "Where the organization runs. The upgrade goes through this region's WSO2 Cloud, and the region is saved on its Salesforce record."
            }
            sx={{ maxWidth: 480 }}
          >
            {availableRegions.map((r) => (
              <MenuItem key={r} value={r}>
                {CLOUD_REGION_LABELS[r]} ({r})
              </MenuItem>
            ))}
          </TextField>
        </SectionCard>
      ) : null}

      <SectionCard title="Subscription">
        <Typography variant="body2" color="text.secondary">
          Moves one product subscription to an Enterprise plan billed by wire transfer. A customer upgrade is
          applied straight away; an internal one waits until another person approves it.
        </Typography>
        {regions.isLoading || subscriptions.isLoading ? (
          <Skeleton variant="rounded" height={40} sx={{ maxWidth: 480 }} />
        ) : availableRegions.length > 0 && !region ? (
          <Alert severity="info" variant="outlined">
            Choose the organization&apos;s WSO2 Cloud region to load its subscriptions.
          </Alert>
        ) : notInRegion ? (
          <Alert severity="warning">{subscriptions.error?.message}</Alert>
        ) : notConnected ? (
          <Alert severity="info" variant="outlined">
            WSO2 Cloud is not connected yet, so this organization's subscriptions cannot be loaded and no
            upgrade can be raised.
          </Alert>
        ) : subscriptions.isError ? (
          <Alert severity="error">Could not load this organization's subscriptions. Try again later.</Alert>
        ) : subs.length === 0 ? (
          <Alert severity="info" variant="outlined">
            This organization has no subscriptions to upgrade.
          </Alert>
        ) : (
          <>
            <TextField
              select
              required
              size="small"
              label="Subscription"
              value={subscriptionId}
              onChange={(e) => chooseSubscription(e.target.value)}
              helperText={subscription ? (subscriptionBlockedReason(subscription) ?? " ") : " "}
              error={!!subscription && !!subscriptionBlockedReason(subscription)}
              sx={{ maxWidth: 480 }}
            >
              {subs.map((s) => (
                <MenuItem key={s.id} value={s.id}>
                  {s.product}: {s.planCode || "no plan"} ({s.status})
                </MenuItem>
              ))}
            </TextField>
            <TextField
              select
              required
              size="small"
              label="Enterprise plan"
              value={planCode}
              onChange={(e) => setPlanCode(e.target.value)}
              disabled={!subscription || productPlans.length === 0}
              helperText={
                subscription && !plans.isLoading && productPlans.length === 0
                  ? `No Enterprise plans are configured for ${subscription.product}.`
                  : " "
              }
              sx={{ maxWidth: 480 }}
            >
              {productPlans.map((p) => (
                <MenuItem key={p.code} value={p.code}>
                  {p.label === p.code ? p.code : `${p.label} (${p.code})`}
                </MenuItem>
              ))}
            </TextField>
            <TextField
              select
              size="small"
              label="Billing period"
              value={billingPeriod}
              onChange={(e) => setBillingPeriod(e.target.value as CloudBillingPeriod | "")}
              helperText="Leave as is to use the plan's own period."
              sx={{ maxWidth: 320 }}
            >
              <MenuItem value="">Plan default</MenuItem>
              {(Object.keys(CLOUD_BILLING_PERIOD_LABELS) as CloudBillingPeriod[]).map((p) => (
                <MenuItem key={p} value={p}>
                  {CLOUD_BILLING_PERIOD_LABELS[p]}
                </MenuItem>
              ))}
            </TextField>
            <FormControlLabel
              control={
                <Checkbox checked={notifyCustomer} onChange={(e) => setNotifyCustomer(e.target.checked)} />
              }
              label="Email the customer about the plan change"
            />
            <TextField
              size="small"
              label="Reason"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              multiline
              minRows={2}
              helperText="Sent to WSO2 Cloud as the plan change's notes, with this request's number."
              slotProps={{ htmlInput: { maxLength: 2000 } }}
            />
          </>
        )}
      </SectionCard>

      <SectionCard title="Salesforce">
        <SalesforceUpgradeSection
          status={salesforce.data}
          statusLoading={salesforce.isLoading}
          notConnected={salesforceNotConnected}
          statusFailed={salesforce.isError && !salesforceNotConnected}
          kind={kind}
          onKindChange={setKindChoice}
          accountInput={accountValue}
          onAccountInputChange={(value) => {
            setAccountInput(value);
            setCheckedId(undefined);
          }}
          onCheck={() => setCheckedId(accountValue.trim())}
          checked={checkedAccount}
          checking={accountCheck.isFetching}
          checkFailed={accountCheck.isError}
        />
      </SectionCard>

      {post.isError && <Alert severity="error">{post.error.message}</Alert>}

      <Box sx={{ display: "flex", gap: 1, justifyContent: "flex-end" }}>
        <Button onClick={() => navigate(backPath)} disabled={post.isPending}>
          Cancel
        </Button>
        <Button variant="contained" onClick={handleSubmit} disabled={!canSubmit}>
          {kind === "customer" ? "Upgrade now" : "Submit for approval"}
        </Button>
      </Box>
    </Box>
  );
}
