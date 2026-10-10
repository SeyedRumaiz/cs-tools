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

/* eslint-disable react-refresh/only-export-components -- Provider component and its useXxx hook are colocated per the repo's context idiom (fast-refresh DX only) */

import { createContext, useContext, type JSX, type ReactNode } from "react";

/**
 * Whether the kept-alive case tab rendering this subtree is the one the user is
 * actually looking at. Deliberately its own tiny context rather than a field on
 * `CaseRouteOverrideValue`: that value is read by the whole (very large) case
 * detail page, so adding a flag that flips on every tab switch would re-render
 * every mounted page each time the active tab changes.
 *
 * Defaults to `true` outside a `CaseTabIsolatedRouter` (a directly-routed page
 * is always the visible one).
 */
const CaseTabVisibilityContext = createContext<boolean>(true);

export function CaseTabVisibilityProvider({
  isVisible,
  children,
}: {
  isVisible: boolean;
  children: ReactNode;
}): JSX.Element {
  return (
    <CaseTabVisibilityContext.Provider value={isVisible}>
      {children}
    </CaseTabVisibilityContext.Provider>
  );
}

/** `true` when this page is on screen (or not tab-managed at all). */
export function useIsCaseTabVisible(): boolean {
  return useContext(CaseTabVisibilityContext);
}
