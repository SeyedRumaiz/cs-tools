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
 * The WSO2 Cloud section's entry in csm-portal's navigation: organisation and
 * user management that used to need the separate Ops portal.
 *
 * Every child carries a `tab` so the sidebar renders them as a submenu (see
 * `plgNavItems.ts` for why that marker is required), and each one is
 * route-backed rather than a `?tab=` query.
 *
 * The ids are a public contract (`CSM_PORTAL_FEATURE_OVERRIDES` keys off
 * them). While the pages serve sample data, a deployment should list
 * `wso2-cloud` there to hide or WIP-mark the whole section.
 */
import { Building2, ClipboardList, Cloud, Users } from "@wso2/oxygen-ui-icons-react";
import type { CsmNavSection } from "@config/csmNavItems";

export const WSO2_CLOUD_BASE = "/wso2-cloud";

export const WSO2_CLOUD_NAV_SECTION: CsmNavSection = {
  id: "wso2-cloud",
  label: "WSO2 Cloud",
  href: `${WSO2_CLOUD_BASE}/organizations`,
  icon: Cloud,
  // Who should see this section is still to be decided; canWrite (admin and
  // cs_engineer) keeps it away from view-only roles until then.
  requires: "canWrite",
  children: [
    {
      id: "wso2-cloud.organizations",
      tab: "organizations",
      label: "Organizations",
      href: `${WSO2_CLOUD_BASE}/organizations`,
      routes: [`${WSO2_CLOUD_BASE}/organizations`],
      icon: Building2,
    },
    {
      id: "wso2-cloud.users",
      tab: "users",
      label: "Users",
      href: `${WSO2_CLOUD_BASE}/users`,
      routes: [`${WSO2_CLOUD_BASE}/users`],
      icon: Users,
    },
    {
      id: "wso2-cloud.requests",
      tab: "requests",
      label: "Requests",
      href: `${WSO2_CLOUD_BASE}/requests`,
      routes: [`${WSO2_CLOUD_BASE}/requests`],
      icon: ClipboardList,
    },
  ],
};
