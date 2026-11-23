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

/**
 * Shapes for the WSO2 Cloud section: the organisation and user management that
 * used to live in the separate Ops portal.
 *
 * These are the frontend's own types. Requests come from the portal backend
 * (`../api/cloudRequestsApi.ts` maps them onto these types). Organisations and
 * users have no backend yet (the WSO2 Cloud API is still to come), so their
 * hooks serve sample data in this shape; when the real API is known, map its
 * responses onto these types rather than changing the pages.
 */

export type CloudTier = "free" | "payg" | "enterprise";

export const CLOUD_TIER_LABELS: Record<CloudTier, string> = {
  free: "Free",
  payg: "Pay as you go",
  enterprise: "Enterprise",
};

export type CloudSupportLevel = "community" | "standard" | "premium";

export const CLOUD_SUPPORT_LEVEL_LABELS: Record<CloudSupportLevel, string> = {
  community: "Community",
  standard: "Standard",
  premium: "Premium",
};

/** What an organisation's subscription allows. */
export interface CloudOrganizationLimits {
  monthlyActiveUsers: number;
  serviceProviders: number;
  identityProviders: number;
  subOrganizations: number;
  supportLevel: CloudSupportLevel;
}

export const CLOUD_LIMIT_LABELS: Record<keyof CloudOrganizationLimits, string> = {
  monthlyActiveUsers: "Monthly active users",
  serviceProviders: "Service providers",
  identityProviders: "Identity providers",
  subOrganizations: "Sub-organizations",
  supportLevel: "Support level",
};

export interface CloudUserRef {
  id: string;
  email: string;
}

export interface CloudOrganization {
  /** The organisation's UUID. */
  id: string;
  /** The unique handle (shown as "Domain" in the Ops portal). */
  handle: string;
  name: string;
  owner: CloudUserRef;
  tier: CloudTier;
  limits: CloudOrganizationLimits;
  createdOn: string;
}

export interface CloudOrganizationRef {
  id: string;
  handle: string;
}

export interface CloudUser {
  /** The user's SCIM id. */
  id: string;
  username: string;
  email: string;
  createdOn: string;
  lastModifiedOn: string;
  lastLoginOn?: string;
  locked: boolean;
  lockedReason?: string;
  disabled: boolean;
  failedLoginAttempts: number;
  organizations: CloudOrganizationRef[];
}

/**
 * Every change made from this section is a request that someone else
 * approves, as in the Ops portal.
 */
export type CloudRequestType =
  | "upgrade_organization"
  | "delete_organization"
  | "lock_user"
  | "unlock_user"
  | "disable_user"
  | "enable_user"
  | "delete_user";

export const CLOUD_REQUEST_TYPE_LABELS: Record<CloudRequestType, string> = {
  upgrade_organization: "Upgrade organisation",
  delete_organization: "Delete organisation",
  lock_user: "Lock user",
  unlock_user: "Unlock user",
  disable_user: "Disable user",
  enable_user: "Enable user",
  delete_user: "Delete user",
};

export type CloudRequestStatus = "pending" | "approved" | "rejected";

export interface CloudRequestTarget {
  kind: "organization" | "user";
  id: string;
  /** Handle for an organisation, email for a user. */
  label: string;
}

export interface CloudRequest {
  id: string;
  /** WCR0000001: what people see and quote; never reused. */
  number: string;
  type: CloudRequestType;
  target: CloudRequestTarget;
  /** One line saying what changes, e.g. "Upgrade identity to identity-enterprise" (derived, not stored). */
  summary: string;
  /**
   * Why the requester raised it. Lock and disable ask for one, as in the Ops
   * portal; an approved lock shows it as the user's locked reason.
   */
  reason?: string;
  /** The subject of the customer's consent email, when one was given (deletions). */
  customerConsent?: string;
  /**
   * Present on an upgrade request. An upgrade moves one subscription (one
   * product of the organisation) to an Enterprise plan billed by wire transfer,
   * through WSO2 Cloud's billing admin API.
   */
  upgrade?: CloudUpgrade;
  requestedBy: string;
  requestedOn: string;
  status: CloudRequestStatus;
  resolvedBy?: string;
  resolvedOn?: string;
  /** Why it was rejected. The Ops portal keeps one remark per decision. */
  remark?: string;
}

export interface NewCloudRequest {
  type: CloudRequestType;
  target: CloudRequestTarget;
  reason?: string;
  customerConsent?: string;
  upgrade?: NewCloudUpgrade;
}

export type CloudBillingPeriod = "monthly" | "annual";

export const CLOUD_BILLING_PERIOD_LABELS: Record<CloudBillingPeriod, string> = {
  monthly: "Monthly",
  annual: "Annual",
};

/**
 * Whether an upgrade is for a paying customer or internal. A customer upgrade
 * names the customer's Salesforce account, which must have an active
 * subscription (an active opportunity), and the organisation is linked to that
 * account in Salesforce once approved. An internal upgrade never touches
 * Salesforce.
 */
export type CloudUpgradeKind = "customer" | "internal";

/** The WSO2 Cloud regions an organisation can be in (Salesforce's region picklist). */
export const CLOUD_REGIONS = ["us", "eu"] as const;
export type CloudRegion = (typeof CLOUD_REGIONS)[number];

export const CLOUD_REGION_LABELS: Record<CloudRegion, string> = {
  us: "United States",
  eu: "Europe",
};

/** What an upgrade asks for, as it is raised. */
export interface NewCloudUpgrade {
  product: string;
  subscriptionId: string;
  planCode: string;
  /** The plan the subscription had when the upgrade was raised. */
  previousPlanCode?: string;
  billingPeriod?: CloudBillingPeriod;
  /** Whether WSO2 Cloud emails the customer about the plan change. */
  notifyCustomer: boolean;
  kind: CloudUpgradeKind;
  /** A customer upgrade's Salesforce Account Id. */
  salesforceAccountId?: string;
  /** A customer upgrade's WSO2 Cloud region. */
  region?: CloudRegion;
}

/** The Salesforce link of an approved customer upgrade, once it was tried. */
export interface CloudSalesforceLink {
  status: "linked" | "failed";
  /** Why it failed, written for the person who will put it right. */
  error?: string;
  linkedOn?: string;
}

/** An upgrade as stored. */
export interface CloudUpgrade extends Omit<NewCloudUpgrade, "product" | "kind" | "region"> {
  product?: string;
  /** The wire billing account created in WSO2 Cloud while the upgrade was approved. */
  billingAccountId?: string;
  /** Absent on an upgrade raised before the kind existed (treated as internal). */
  kind?: CloudUpgradeKind;
  region?: string;
  salesforceLink?: CloudSalesforceLink;
}

/** One product subscription of an organisation in WSO2 Cloud. */
export interface CloudSubscription {
  id: string;
  product: string;
  planCode: string;
  /** WSO2 Cloud's own status, e.g. active, trial, suspended, cancelled. */
  status: string;
  trialEnd?: string;
}

/** An Enterprise plan an upgrade can ask for. */
export interface CloudPlan {
  code: string;
  label: string;
}

/** The configured Enterprise plans, per product. */
export type CloudPlansByProduct = Record<string, CloudPlan[]>;

/** An active opportunity of a customer's Salesforce account. */
export interface CloudSalesforceOpportunity {
  id: string;
  name?: string;
  /** When the support it sold ends (YYYY-MM-DD). */
  endDate?: string;
}

/** A customer's Salesforce account and whether it has an active subscription. */
export interface CloudSalesforceAccount {
  /** The Account Id, 18 characters. */
  id: string;
  name?: string;
  hasActiveSubscription: boolean;
  activeOpportunities: CloudSalesforceOpportunity[];
}

/** What Salesforce knows about an organisation: its record and the account it is under, when it has one. */
export interface CloudSalesforceStatus {
  record: { id: string; handle?: string; region?: string } | null;
  account: CloudSalesforceAccount | null;
}
