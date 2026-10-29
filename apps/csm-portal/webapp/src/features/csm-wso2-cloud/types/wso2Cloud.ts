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
 * These are the frontend's own types. The backend routes they will come from
 * do not exist yet, so the hooks in `../api` serve sample data in this shape;
 * when the real API is known, map its responses onto these types rather than
 * changing the pages.
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
  type: CloudRequestType;
  target: CloudRequestTarget;
  /** One line saying what changes, e.g. "Free → Enterprise". */
  summary: string;
  /** Present on an upgrade request: the limits asked for. */
  limits?: CloudOrganizationLimits;
  tier?: CloudTier;
  requestedBy: string;
  requestedOn: string;
  status: CloudRequestStatus;
  resolvedBy?: string;
  resolvedOn?: string;
}

export interface NewCloudRequest {
  type: CloudRequestType;
  target: CloudRequestTarget;
  summary: string;
  limits?: CloudOrganizationLimits;
  tier?: CloudTier;
}
