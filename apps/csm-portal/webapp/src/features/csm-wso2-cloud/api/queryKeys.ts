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

/** Root of every WSO2 Cloud query key, so a request write can refresh them all. */
export const WSO2_CLOUD_QUERY_KEY = "wso2-cloud";

/**
 * How often an open request list, or an open request that is still pending,
 * is read again. A request is approved or rejected by someone else, in another
 * browser, so the requester's page would otherwise show "Pending" until it is
 * reloaded. Polling stops while the tab is in the background.
 */
export const WSO2_CLOUD_REQUESTS_REFRESH_MS = 15_000;
