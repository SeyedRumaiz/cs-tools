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

import { Chip } from "@wso2/oxygen-ui";
import type { JSX } from "react";
import type { CloudUser } from "@features/csm-wso2-cloud/types/wso2Cloud";

/** Disabled outranks locked: a disabled account cannot be used whether or not it is locked. */
export default function UserStatusChip({
  user,
}: {
  user: Pick<CloudUser, "locked" | "disabled">;
}): JSX.Element {
  if (user.disabled) return <Chip size="small" variant="outlined" color="error" label="Disabled" />;
  if (user.locked) return <Chip size="small" variant="outlined" color="warning" label="Locked" />;
  return <Chip size="small" variant="outlined" color="success" label="Active" />;
}
