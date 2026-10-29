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

import { Navigate, Route } from "react-router";
import CloudOrganizationDetailPage from "./pages/CloudOrganizationDetailPage";
import CloudOrganizationUpgradePage from "./pages/CloudOrganizationUpgradePage";
import CloudOrganizationsPage from "./pages/CloudOrganizationsPage";
import CloudRequestsPage from "./pages/CloudRequestsPage";
import CloudUserDetailPage from "./pages/CloudUserDetailPage";
import CloudUsersPage from "./pages/CloudUsersPage";

/**
 * The WSO2 Cloud section's routes, spread into csm-portal's `<Routes>` inside
 * the same `AuthGuard` / `FeatureRouteGuard` pair as every other section.
 * A function rather than a component because `<Routes>` reads its children as
 * configuration (same reason as `plgRoutes`). Pages are imported eagerly, per
 * csm-portal's one-bundle convention.
 */
export function wso2CloudRoutes() {
  return (
    <Route path="wso2-cloud">
      <Route index element={<Navigate to="organizations" replace />} />
      <Route path="organizations" element={<CloudOrganizationsPage />} />
      <Route path="organizations/:organizationId" element={<CloudOrganizationDetailPage />} />
      <Route
        path="organizations/:organizationId/upgrade"
        element={<CloudOrganizationUpgradePage />}
      />
      <Route path="users" element={<CloudUsersPage />} />
      <Route path="users/:userId" element={<CloudUserDetailPage />} />
      <Route path="requests" element={<CloudRequestsPage />} />
    </Route>
  );
}
