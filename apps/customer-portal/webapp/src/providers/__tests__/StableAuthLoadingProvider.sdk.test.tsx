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

import { render } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { AsgardeoContext, useAsgardeo } from "@asgardeo/react";
import StableAuthLoadingProvider from "@providers/StableAuthLoadingProvider";

// Contract test against the REAL SDK. vitest.setup.ts mocks the package for
// every other test; this file puts the real one back so the provider and the
// SDK's own `useAsgardeo` are exercised together. If an SDK upgrade moves or
// stops exporting `AsgardeoContext`, or its hook stops reading it, this fails
// instead of the portal silently going back to refetching on every blip.
vi.mock("@asgardeo/react", async (importActual) => await importActual());

type SdkState = { isSignedIn: boolean; isLoading: boolean };

function Tree({ state, children }: { state: SdkState; children: ReactNode }) {
  return (
    <AsgardeoContext.Provider value={{ ...state, tag: "provided" } as never}>
      <StableAuthLoadingProvider>{children}</StableAuthLoadingProvider>
    </AsgardeoContext.Provider>
  );
}

describe("StableAuthLoadingProvider with the real @asgardeo/react", () => {
  it("makes the SDK's own useAsgardeo report a steady isLoading after the first ready state", () => {
    const seen: Array<{ isLoading: boolean; isSignedIn: boolean; tag: unknown }> = [];
    const Probe = () => {
      const auth = useAsgardeo() as unknown as { isLoading: boolean; isSignedIn: boolean; tag: unknown };
      seen.push({ isLoading: auth.isLoading, isSignedIn: auth.isSignedIn, tag: auth.tag });
      return null;
    };
    const last = () => seen[seen.length - 1];

    const view = render(
      <Tree state={{ isSignedIn: true, isLoading: true }}>
        <Probe />
      </Tree>,
    );
    expect(last().isLoading).toBe(true);

    view.rerender(
      <Tree state={{ isSignedIn: true, isLoading: false }}>
        <Probe />
      </Tree>,
    );
    expect(last().isLoading).toBe(false);

    view.rerender(
      <Tree state={{ isSignedIn: true, isLoading: true }}>
        <Probe />
      </Tree>,
    );
    expect(last()).toEqual({ isLoading: false, isSignedIn: true, tag: "provided" });

    view.rerender(
      <Tree state={{ isSignedIn: false, isLoading: true }}>
        <Probe />
      </Tree>,
    );
    expect(last()).toEqual({ isLoading: true, isSignedIn: false, tag: "provided" });
  });
});
