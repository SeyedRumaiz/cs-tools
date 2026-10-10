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

// TEMPORARY / LOCAL DEV ONLY flag — see the CSM_PORTAL_DEV_BYPASS_ACCESS_CHECK
// declaration on Window.config in authConfig.ts for the full explanation.
// Deliberately its OWN module, separate from authConfig.ts: that file's
// `authConfig` export calls getAuthConfig(), which throws synchronously (at
// module-evaluation time, for EVERY importer) when window.config lacks the
// real OIDC fields — true in every unit test, which stub only the handful of
// config keys each test actually needs. This flag must stay importable from
// a plain component/hook test with no OIDC config stubbed at all, so it
// reads window.config directly here rather than living behind that throw.
export const devBypassAccessCheck: boolean =
  window.config?.CSM_PORTAL_DEV_BYPASS_ACCESS_CHECK === true;
