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
 * Everything here is made up. Requests created while the page is open, and
 * the changes approving them makes, live only in this module and are gone on
 * reload.
 */
import {
  CLOUD_REQUEST_TYPE_LABELS,
  type CloudOrganization,
  type CloudRequest,
  type CloudRequestStatus,
  type CloudUser,
  type NewCloudRequest,
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
    remark: "The lock clears on its own after the lockout period.",
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

// Counted, not derived from the list's length: a cancelled request leaves the
// list, and its number must not be handed out again.
let lastRequestNumber = requests.length;

/**
 * Raises a request. As in the Ops portal, an organisation or user can have only
 * one pending request of each type: a second upgrade or deletion waits for the
 * first to be decided or cancelled.
 */
export function createRequest(input: NewCloudRequest, requestedBy: string): Promise<CloudRequest> {
  const waiting = requests.find(
    (r) =>
      r.status === "pending" &&
      r.type === input.type &&
      r.target.kind === input.target.kind &&
      r.target.id === input.target.id,
  );
  if (waiting) {
    return Promise.reject(
      new Error(
        `${input.target.label} already has a pending ${CLOUD_REQUEST_TYPE_LABELS[input.type].toLowerCase()} request (${waiting.id}). It has to be approved, rejected or cancelled before another one is raised.`,
      ),
    );
  }
  lastRequestNumber += 1;
  const created: CloudRequest = {
    ...input,
    id: `REQ-${String(lastRequestNumber).padStart(4, "0")}`,
    requestedBy,
    requestedOn: new Date().toISOString(),
    status: "pending",
  };
  requests = [created, ...requests];
  return delay(created);
}

/**
 * Withdraws a request before anyone has decided it. As in the Ops portal, only
 * the person who raised it can, only while it is pending, and the request is
 * removed rather than kept with a status of its own.
 */
export function cancelRequest(id: string, cancelledBy: string): Promise<void> {
  const request = requests.find((r) => r.id === id);
  if (!request) return Promise.reject(new Error(`${id} no longer exists.`));
  if (request.requestedBy !== cancelledBy) {
    return Promise.reject(new Error("Only the person who raised a request can cancel it."));
  }
  if (request.status !== "pending") {
    return Promise.reject(new Error(`${id} has already been ${request.status}.`));
  }
  requests = requests.filter((r) => r.id !== id);
  return delay(undefined);
}

/**
 * Approves or rejects a pending request. As in the Ops portal, an approval
 * carries out the change first and is recorded only once that succeeds: if it
 * fails, the request stays pending and the error says why, so the approver can
 * reject it instead.
 */
export function resolveRequest(
  id: string,
  status: Exclude<CloudRequestStatus, "pending">,
  resolvedBy: string,
  remark?: string,
): Promise<CloudRequest> {
  const request = requests.find((r) => r.id === id);
  if (!request) return Promise.reject(new Error(`${id} no longer exists.`));
  if (request.status !== "pending") {
    return Promise.reject(new Error(`${id} has already been ${request.status}.`));
  }
  if (status === "approved") {
    try {
      applyApproved(request);
    } catch (e) {
      return Promise.reject(e);
    }
  }
  const resolved: CloudRequest = {
    ...request,
    status,
    resolvedBy,
    resolvedOn: new Date().toISOString(),
    remark: remark?.trim() || undefined,
  };
  requests = requests.map((r) => (r.id === id ? resolved : r));
  return delay(resolved);
}

/**
 * Carries out an approved request, standing in for the call the backend will
 * make to WSO2 Cloud once the real API is wired in. Throws when it cannot,
 * which leaves the request pending. Records are replaced, not mutated, so a
 * cached copy is never changed under a page that holds it.
 */
function applyApproved(request: CloudRequest): void {
  const now = new Date().toISOString();
  const { id } = request.target;

  if (request.target.kind === "organization") {
    const index = ORGANIZATIONS.findIndex((o) => o.id === id);
    if (index < 0) throw targetGone(request);
    if (request.type === "upgrade_organization") {
      const org = ORGANIZATIONS[index];
      ORGANIZATIONS[index] = {
        ...org,
        tier: request.tier ?? org.tier,
        limits: request.limits ?? org.limits,
      };
    } else if (request.type === "delete_organization") {
      ORGANIZATIONS.splice(index, 1);
      USERS.forEach((u, i) => {
        if (u.organizations.some((o) => o.id === id)) {
          USERS[i] = { ...u, organizations: u.organizations.filter((o) => o.id !== id) };
        }
      });
    }
    return;
  }

  const index = USERS.findIndex((u) => u.id === id);
  if (index < 0) throw targetGone(request);
  const user = USERS[index];
  switch (request.type) {
    case "lock_user":
      USERS[index] = { ...user, locked: true, lockedReason: "Locked by WSO2 support", lastModifiedOn: now };
      break;
    case "unlock_user":
      USERS[index] = { ...user, locked: false, lockedReason: undefined, failedLoginAttempts: 0, lastModifiedOn: now };
      break;
    case "disable_user":
      USERS[index] = { ...user, disabled: true, lastModifiedOn: now };
      break;
    case "enable_user":
      USERS[index] = { ...user, disabled: false, lastModifiedOn: now };
      break;
    case "delete_user":
      USERS.splice(index, 1);
      break;
  }
}

function targetGone(request: CloudRequest): Error {
  const what = request.target.kind === "organization" ? "organisation" : "user";
  return new Error(
    `${request.id} could not be applied: the ${what} ${request.target.label} no longer exists. It is still pending; reject it instead.`,
  );
}
