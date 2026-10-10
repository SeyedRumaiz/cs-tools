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

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { AsgardeoContext, useAsgardeo } from "@asgardeo/react";
import useGetUserDetails from "@features/settings/api/useGetUserDetails";
import StableAuthLoadingProvider from "@providers/StableAuthLoadingProvider";

// The real SDK is not loaded in tests (see vitest.setup.ts). This stand-in
// mirrors what the SDK does that matters here: its `useAsgardeo` reads the
// exported `AsgardeoContext` and spreads it into the result. The context is
// created once, in the hoisted block, so the mock and the tests share it.
const { authFetchMock, FakeContext } = await vi.hoisted(async () => {
  const React = await import("react");
  return {
    authFetchMock: vi.fn(),
    FakeContext: React.createContext<Record<string, unknown>>({
      isSignedIn: false,
      isLoading: true,
    }),
  };
});

vi.mock("@asgardeo/react", async () => {
  const React = await import("react");
  return {
    AsgardeoContext: FakeContext,
    useAsgardeo: () => ({ ...React.useContext(FakeContext) }),
  };
});

vi.mock("@/hooks/useAuthApiClient", () => ({
  useAuthApiClient: () => authFetchMock,
}));

vi.mock("@hooks/useLogger", () => ({
  useLogger: () => ({ debug: vi.fn(), error: vi.fn(), warn: vi.fn() }),
}));

type SdkState = { isSignedIn: boolean; isLoading: boolean };
const getIdToken = vi.fn();

/** What the SDK's provider would hand down, wrapped in the provider under test. */
function AuthTree({ state, children }: { state: SdkState; children: ReactNode }) {
  return (
    <AsgardeoContext.Provider value={{ ...state, getIdToken } as never}>
      <StableAuthLoadingProvider>{children}</StableAuthLoadingProvider>
    </AsgardeoContext.Provider>
  );
}

const settle = (ms = 25) => act(() => new Promise<void>((resolve) => setTimeout(resolve, ms)));

/** Renders `ui` under the auth tree and lets a test move the SDK's state afterwards. */
function mountUnderAuth(initial: SdkState, ui: () => ReactNode) {
  const view = render(<AuthTree state={initial}>{ui()}</AuthTree>);
  return {
    setSdk: (next: SdkState) => view.rerender(<AuthTree state={next}>{ui()}</AuthTree>),
  };
}

/** A consumer of the SDK hook that records what each render saw. */
function recordAuth() {
  const seen: Array<{ isSignedIn: boolean; isLoading: boolean; getIdToken: unknown }> = [];
  const Probe = () => {
    const auth = useAsgardeo() as unknown as {
      isSignedIn: boolean;
      isLoading: boolean;
      getIdToken: unknown;
    };
    seen.push({ isSignedIn: auth.isSignedIn, isLoading: auth.isLoading, getIdToken: auth.getIdToken });
    return null;
  };
  return { Probe, last: () => seen[seen.length - 1] };
}

describe("StableAuthLoadingProvider", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    (
      window as unknown as { config?: { CUSTOMER_PORTAL_BACKEND_BASE_URL?: string } }
    ).config = { CUSTOMER_PORTAL_BACKEND_BASE_URL: "https://api.test" };
    authFetchMock.mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ id: "u-1" }),
    });
  });

  describe("with a real data hook (useGetUserDetails, unchanged)", () => {
    function UserDetails() {
      useGetUserDetails();
      return null;
    }
    function underQueryClient() {
      // staleTime stays at the library default (0), like the real hooks
      const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, refetchOnMount: true } },
      });
      return (
        <QueryClientProvider client={queryClient}>
          <UserDetails />
        </QueryClientProvider>
      );
    }

    it("fetches once, then ignores the SDK's loading flag flipping while signed in", async () => {
      // one tree, one QueryClient: only the SDK state changes between renders
      const tree = underQueryClient();
      const { setSdk } = mountUnderAuth({ isSignedIn: true, isLoading: false }, () => tree);
      await waitFor(() => expect(authFetchMock).toHaveBeenCalledTimes(1));

      // the SDK reports "loading" while it touches the token, over and over
      for (let i = 0; i < 4; i++) {
        setSdk({ isSignedIn: true, isLoading: true });
        await settle();
        setSdk({ isSignedIn: true, isLoading: false });
        await settle();
      }

      expect(authFetchMock).toHaveBeenCalledTimes(1);
    });

    it("still waits for the first ready state before fetching", async () => {
      const tree = underQueryClient();
      const { setSdk } = mountUnderAuth({ isSignedIn: true, isLoading: true }, () => tree);
      await settle();
      expect(authFetchMock).not.toHaveBeenCalled();

      setSdk({ isSignedIn: true, isLoading: false });
      await waitFor(() => expect(authFetchMock).toHaveBeenCalledTimes(1));
    });

    it("does not fetch while signed out", async () => {
      const tree = underQueryClient();
      mountUnderAuth({ isSignedIn: false, isLoading: false }, () => tree);
      await settle();
      expect(authFetchMock).not.toHaveBeenCalled();
    });
  });

  describe("what consumers of the SDK hook see", () => {
    it("reports loading until the session has been ready once, then stays settled", () => {
      const { Probe, last } = recordAuth();
      const { setSdk } = mountUnderAuth({ isSignedIn: true, isLoading: true }, () => <Probe />);
      expect(last().isLoading).toBe(true);

      setSdk({ isSignedIn: true, isLoading: false });
      expect(last().isLoading).toBe(false);

      setSdk({ isSignedIn: true, isLoading: true });
      expect(last().isLoading).toBe(false);
    });

    it("passes loading through again after a sign-out", () => {
      const { Probe, last } = recordAuth();
      const { setSdk } = mountUnderAuth({ isSignedIn: true, isLoading: false }, () => <Probe />);
      expect(last().isLoading).toBe(false);

      setSdk({ isSignedIn: false, isLoading: false });
      expect(last().isSignedIn).toBe(false);

      // signing back in while the SDK is still busy: not settled yet
      setSdk({ isSignedIn: true, isLoading: true });
      expect(last().isLoading).toBe(true);
    });

    it("never reports a signed-out session as ready", () => {
      const { Probe, last } = recordAuth();
      mountUnderAuth({ isSignedIn: false, isLoading: true }, () => <Probe />);
      expect(last().isSignedIn).toBe(false);
      expect(last().isLoading).toBe(true);
    });

    it("passes every other field through untouched, also while it is holding isLoading", () => {
      const { Probe, last } = recordAuth();
      const { setSdk } = mountUnderAuth({ isSignedIn: true, isLoading: false }, () => <Probe />);
      expect(last().getIdToken).toBe(getIdToken);

      // the holding branch builds a new context object: nothing else may change
      setSdk({ isSignedIn: true, isLoading: true });
      expect(last().isLoading).toBe(false);
      expect(last().getIdToken).toBe(getIdToken);
      expect(last().isSignedIn).toBe(true);
    });

    it("does not hold loading when the session ends together with a busy flag", () => {
      const { Probe, last } = recordAuth();
      const { setSdk } = mountUnderAuth({ isSignedIn: true, isLoading: false }, () => <Probe />);

      // a failed token refresh: the SDK reports signed out and busy in the same update
      setSdk({ isSignedIn: false, isLoading: true });
      expect(last().isSignedIn).toBe(false);
      expect(last().isLoading).toBe(true);
    });
  });
});
