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


import type { ProjectContact } from "@features/settings/types/users";

const REGISTERED_STATUS = "REGISTERED";

/**
 * Whether a project contact has completed registration. Invited, re-invited and
 * deactivated contacts are not registered, and the backend rejects a case with
 * a watcher who is not a registered contact of the project.
 *
 * @param {ProjectContact} contact - The project contact.
 * @returns {boolean} True when the contact's membership status is REGISTERED.
 */
export function isRegisteredContact(
  contact: Pick<ProjectContact, "membershipStatus">,
): boolean {
  return (contact.membershipStatus ?? "").trim().toUpperCase() === REGISTERED_STATUS;
}

/**
 * Whether a project contact may be offered (and pre-selected) as a case watcher.
 *
 * The backend rejects a case-creation request when any watcher is not a
 * registered contact of the project, so invited, re-invited and deactivated
 * contacts must never appear here. The role rule (admin, integration user,
 * portal user, or any non-security contact) is kept as is.
 *
 * The membership status is compared trimmed and case-insensitively because the
 * wire value is an upstream state string (e.g. "REGISTERED", "INVITED",
 * "RE-INVITED", "DEACTIVATED"); a missing status is treated as not registered.
 *
 * @param {ProjectContact} contact - The project contact.
 * @returns {boolean} True when the contact is eligible as a watcher.
 */
export function isEligibleWatcher(
  contact: Pick<
    ProjectContact,
    | "membershipStatus"
    | "isCsAdmin"
    | "isCsIntegrationUser"
    | "isPortalUser"
    | "isSecurityContact"
  >,
): boolean {
  const isRegistered = isRegisteredContact(contact);
  const hasEligibleRole =
    contact.isCsAdmin ||
    contact.isCsIntegrationUser ||
    !!contact.isPortalUser ||
    !contact.isSecurityContact;
  return isRegistered && hasEligibleRole;
}
