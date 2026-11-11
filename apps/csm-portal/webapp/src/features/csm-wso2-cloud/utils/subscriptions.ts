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

import type { CloudSubscription } from "@features/csm-wso2-cloud/types/wso2Cloud";

/** Subscription states WSO2 Cloud refuses a plan change for. */
const UNCHANGEABLE_STATUSES: Record<string, string> = {
  suspended: "Suspended",
  cancelled: "Cancelled",
  canceled: "Cancelled",
  pending_cancellation: "Pending cancellation",
};

/** Why WSO2 Cloud would refuse to change this subscription's plan, or null when it can be changed. */
export function subscriptionBlockedReason(sub: CloudSubscription): string | null {
  const label = UNCHANGEABLE_STATUSES[sub.status.toLowerCase()];
  return label ? `${label}: WSO2 Cloud does not change the plan of a subscription in this state.` : null;
}
