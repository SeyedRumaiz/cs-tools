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
 * In-memory sample data for the WSO2 Cloud section.
 *
 * The pages are built before the backend exists: which API the Ops portal
 * calls for organisation search, upgrade, delete and user locking is still to
 * be confirmed with the cloud team. Every hook in this folder goes through the
 * functions below, so swapping in the real backend means changing the hooks'
 * `queryFn`/`mutationFn` and deleting this file, not touching the pages.
 *
 * Everything here is made up. Requests created while the page is open live
 * only in this module and are gone on reload.
 */
import type {
  CloudOrganization,
  CloudRequest,
  CloudRequestStatus,
  CloudUser,
  NewCloudRequest,
} from "@features/csm-wso2-cloud/types/wso2Cloud";

const SAMPLE_DELAY_MS = 300;

function delay<T>(value: T): Promise<T> {
  return new Promise((resolve) => setTimeout(() => resolve(value), SAMPLE_DELAY_MS));
}

const ORGANIZATIONS: CloudOrganization[] = [
  {
    id: "0f6c1a52-5b1e-4c39-9a51-2d7e1f3b8a01",
    handle: "acme",
    name: "Acme Corp",
    owner: { id: "u-1001", email: "jane.doe@example.com" },
    tier: "free",
    limits: {
      monthlyActiveUsers: 1000,
      serviceProviders: 5,
      identityProviders: 1,
      subOrganizations: 1,
      supportLevel: "community",
    },
    createdOn: "2025-02-10T10:44:56Z",
  },
  {
    id: "4b2d9e7c-0a3f-4e11-8c6d-7f5a2b9c1e02",
    handle: "acme-dev",
    name: "Acme Corp (dev)",
    owner: { id: "u-1001", email: "jane.doe@example.com" },
    tier: "payg",
    limits: {
      monthlyActiveUsers: 15000,
      serviceProviders: 20,
      identityProviders: 5,
      subOrganizations: 10,
      supportLevel: "community",
    },
    createdOn: "2025-06-02T08:15:00Z",
  },
  {
    id: "9e8a7b6c-1d2e-4f30-a1b2-c3d4e5f60703",
    handle: "globex",
    name: "Globex",
    owner: { id: "u-1002", email: "john.smith@example.com" },
    tier: "enterprise",
    limits: {
      monthlyActiveUsers: 250000,
      serviceProviders: 100,
      identityProviders: 25,
      subOrganizations: 50,
      supportLevel: "premium",
    },
    createdOn: "2024-11-20T14:02:11Z",
  },
  {
    id: "2c3d4e5f-6a7b-4c8d-9e0f-1a2b3c4d5e04",
    handle: "initech",
    name: "Initech",
    owner: { id: "u-1003", email: "alex.lee@example.org" },
    tier: "free",
    limits: {
      monthlyActiveUsers: 1000,
      serviceProviders: 5,
      identityProviders: 1,
      subOrganizations: 1,
      supportLevel: "community",
    },
    createdOn: "2026-03-14T09:30:00Z",
  },
];

const USERS: CloudUser[] = [
  {
    id: "u-1001",
    username: "jane.doe@example.com",
    email: "jane.doe@example.com",
    createdOn: "2025-02-10T10:40:00Z",
    lastModifiedOn: "2026-05-12T20:10:33Z",
    lastLoginOn: "2026-07-15T20:56:17Z",
    locked: false,
    disabled: false,
    failedLoginAttempts: 0,
    organizations: [
      { id: ORGANIZATIONS[0].id, handle: "acme" },
      { id: ORGANIZATIONS[1].id, handle: "acme-dev" },
    ],
  },
  {
    id: "u-1002",
    username: "john.smith@example.com",
    email: "john.smith@example.com",
    createdOn: "2024-11-20T14:00:00Z",
    lastModifiedOn: "2026-01-04T11:00:00Z",
    lastLoginOn: "2026-10-01T07:12:45Z",
    locked: true,
    lockedReason: "Too many failed sign-in attempts",
    disabled: false,
    failedLoginAttempts: 5,
    organizations: [{ id: ORGANIZATIONS[2].id, handle: "globex" }],
  },
  {
    id: "u-1003",
    username: "alex.lee@example.org",
    email: "alex.lee@example.org",
    createdOn: "2026-03-14T09:25:00Z",
    lastModifiedOn: "2026-03-14T09:25:00Z",
    locked: false,
    disabled: false,
    failedLoginAttempts: 0,
    organizations: [{ id: ORGANIZATIONS[3].id, handle: "initech" }],
  },
];

let requests: CloudRequest[] = [
  {
    id: "REQ-0003",
    type: "upgrade_organization",
    target: { kind: "organization", id: ORGANIZATIONS[3].id, label: "initech" },
    summary: "Free → Enterprise",
    tier: "enterprise",
    requestedBy: "sam.taylor@example.com",
    requestedOn: "2026-10-27T09:00:00Z",
    status: "pending",
  },
  {
    id: "REQ-0002",
    type: "unlock_user",
    target: { kind: "user", id: "u-1002", label: "john.smith@example.com" },
    summary: "Unlock after failed sign-in attempts",
    requestedBy: "sam.taylor@example.com",
    requestedOn: "2026-10-20T13:30:00Z",
    status: "rejected",
    resolvedBy: "pat.morgan@example.com",
    resolvedOn: "2026-10-21T08:00:00Z",
  },
  {
    id: "REQ-0001",
    type: "upgrade_organization",
    target: { kind: "organization", id: ORGANIZATIONS[2].id, label: "globex" },
    summary: "Pay as you go → Enterprise",
    tier: "enterprise",
    requestedBy: "sam.taylor@example.com",
    requestedOn: "2026-09-02T10:00:00Z",
    status: "approved",
    resolvedBy: "pat.morgan@example.com",
    resolvedOn: "2026-09-03T09:15:00Z",
  },
];

/**
 * Matches an organisation on its handle, name or id, or on its owner's email.
 * An exact handle or id match comes back alone, so the search page can open
 * that organisation directly.
 */
export function searchOrganizations(query: string): Promise<CloudOrganization[]> {
  const q = query.trim().toLowerCase();
  if (!q) return delay([...ORGANIZATIONS]);
  const exact = ORGANIZATIONS.find((o) => o.handle === q || o.id === q);
  if (exact) return delay([exact]);
  return delay(
    ORGANIZATIONS.filter(
      (o) =>
        o.handle.includes(q) ||
        o.name.toLowerCase().includes(q) ||
        o.owner.email.toLowerCase().includes(q),
    ),
  );
}

export function getOrganization(id: string): Promise<CloudOrganization | null> {
  return delay(ORGANIZATIONS.find((o) => o.id === id) ?? null);
}

export function searchUsers(query: string): Promise<CloudUser[]> {
  const q = query.trim().toLowerCase();
  if (!q) return delay([...USERS]);
  return delay(
    USERS.filter(
      (u) => u.email.toLowerCase().includes(q) || u.username.toLowerCase().includes(q) || u.id === q,
    ),
  );
}

export function getUser(id: string): Promise<CloudUser | null> {
  return delay(USERS.find((u) => u.id === id) ?? null);
}

export function listRequests(): Promise<CloudRequest[]> {
  return delay([...requests]);
}

export function createRequest(input: NewCloudRequest, requestedBy: string): Promise<CloudRequest> {
  const created: CloudRequest = {
    ...input,
    id: `REQ-${String(requests.length + 1).padStart(4, "0")}`,
    requestedBy,
    requestedOn: new Date().toISOString(),
    status: "pending",
  };
  requests = [created, ...requests];
  return delay(created);
}

export function resolveRequest(
  id: string,
  status: Exclude<CloudRequestStatus, "pending">,
  resolvedBy: string,
): Promise<CloudRequest | null> {
  let resolved: CloudRequest | null = null;
  requests = requests.map((r) => {
    if (r.id !== id || r.status !== "pending") return r;
    resolved = { ...r, status, resolvedBy, resolvedOn: new Date().toISOString() };
    return resolved;
  });
  return delay(resolved);
}

