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

import { useContext, useMemo, useState, type JSX, type ReactNode } from "react";
import { AsgardeoContext } from "@asgardeo/react";

/**
 * Keeps the SDK's `isLoading` at `false` for everything below it once the
 * session has been ready, for as long as the user stays signed in. Mount it
 * directly inside `AsgardeoProvider`.
 *
 * Why: the provider mirrors its client's busy flag into `isLoading` (polled
 * every 100 ms), and that flag goes true whenever the client touches the token,
 * including every `getIdToken()` an API call makes and the periodic background
 * refresh. Nearly every data hook gates on `isSignedIn && !isLoading` with
 * `staleTime: 0`, so each of those blips switched every active query off and on
 * again, and React Query refetches stale data when a query is re-enabled. The
 * refetch calls `getIdToken()`, which can raise the flag again: an open page
 * then repeated its user, project and list requests about once a second.
 *
 * Only the first load needs the gate (nothing can be fetched before the session
 * exists). After that a background token refresh is not a reason to switch
 * queries off. Signing out clears the latch, so the next sign-in waits for the
 * SDK again. Every other field of the SDK's context is passed through
 * unchanged, and components keep importing `useAsgardeo` from the SDK as before.
 *
 * @param {object} props - Component props.
 * @param {ReactNode} props.children - The app below the SDK provider.
 * @returns {JSX.Element} The children, under a context whose `isLoading` is latched.
 */
export default function StableAuthLoadingProvider({
  children,
}: {
  children: ReactNode;
}): JSX.Element {
  const sdk = useContext(AsgardeoContext);
  const isSignedIn = sdk?.isSignedIn ?? false;
  const isLoading = sdk?.isLoading ?? false;
  const [hasBeenReady, setHasBeenReady] = useState(false);

  // Derived state, adjusted while rendering (React's documented alternative to
  // an effect for this): set once the session is up, cleared on sign-out. Each
  // branch only fires when it changes the value, so it settles in one extra
  // render.
  if (!isSignedIn && hasBeenReady) {
    setHasBeenReady(false);
  } else if (isSignedIn && !isLoading && !hasBeenReady) {
    setHasBeenReady(true);
  }

  const hideLoading = hasBeenReady && isSignedIn && isLoading;
  const value = useMemo(
    () => (sdk && hideLoading ? { ...sdk, isLoading: false } : sdk),
    [sdk, hideLoading],
  );

  // Defensive only: the context is typed as nullable, but the SDK's default
  // value is a non-null object, so this is not reached in practice.
  if (!value) {
    return <>{children}</>;
  }

  return (
    <AsgardeoContext.Provider value={value}>{children}</AsgardeoContext.Provider>
  );
}
