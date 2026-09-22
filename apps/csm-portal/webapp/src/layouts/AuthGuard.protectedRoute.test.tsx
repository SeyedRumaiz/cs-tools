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

import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import AuthGuard from "./AuthGuard";
import { POST_LOGIN_REDIRECT_KEY } from "@layouts/postLoginRedirect";

const signIn = vi.fn<() => Promise<unknown>>(() => Promise.resolve());
const signInSilently = vi.fn<() => Promise<unknown>>(() => Promise.resolve(false));
const authState = { isSignedIn: false, isLoading: false };

// Only `useAsgardeo` is stubbed. The real `@asgardeo/react-router`
// ProtectedRoute stays in the tree on purpose: stubbing it out would hide the
// exact behaviour these tests exist to pin down — its `onSignIn` branch falls
// through to an unconditional throw, so AuthGuard has to drive sign-in from
// `fallback` instead.
vi.mock("@asgardeo/react", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@asgardeo/react")>()),
  useAsgardeo: () => ({ ...authState, signIn, signInSilently }),
}));

vi.mock("@layouts/AppLayout", () => ({
  default: () => <div data-testid="app-layout">App layout</div>,
}));

// AuthorizedAppShell renders this as a sibling of AppLayout (both inside a
// real ChatSessionsProvider -- see AuthGuard.tsx). Its own hooks
// (useChatAlertsStream, useGetEngineerStatus, the accept/decline/send
// mutations) need real API config/react-query wiring this test doesn't set
// up, and nothing here asserts on its rendering -- stub it out the same way
// AppLayout is stubbed above, rather than mocking its whole context surface.
vi.mock("@features/csm-chat/components/EngineerAlertNotification", () => ({
  default: () => null,
}));

// AuthGuard.tsx also imports the chat-sessions context module directly (it
// wraps AppLayout + EngineerAlertNotification in one provider so both sit in
// the same tree -- see AuthGuard.tsx). That import alone pulls in the chat
// API hooks, which read apiConfig.ts at import time and throw when
// CSM_PORTAL_BACKEND_BASE_URL isn't configured, regardless of whether
// anything actually calls the useChatSessions hook -- so this needs its own
// mock even with the notification component stubbed out above.
vi.mock("@context/chat-sessions/ChatSessionsContext", () => ({
  ChatSessionsProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

vi.mock("@context/current-user/CurrentUserContext", () => ({
  CurrentUserProvider: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="current-user-provider">{children}</div>
  ),
  useCurrentUser: () => ({ user: undefined, isLoading: false, isError: false, error: null }),
}));

const isTopLevel = { value: true };
vi.mock("@utils/isTopLevelWindow", () => ({
  isTopLevelWindow: () => isTopLevel.value,
}));

vi.mock("@hooks/useLogger", () => ({
  useLogger: () => ({
    debug: vi.fn(),
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
  }),
}));

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <AuthGuard />
    </MemoryRouter>,
  );
}

describe("AuthGuard against the real ProtectedRoute", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    signIn.mockResolvedValue(undefined);
    signInSilently.mockResolvedValue(false);
    sessionStorage.clear();
    authState.isSignedIn = false;
    authState.isLoading = false;
    isTopLevel.value = true;
  });

  it("keeps the shell up and starts sign-in instead of crashing when signed out", async () => {
    renderAt("/cases/abc123");

    expect(screen.queryByTestId("app-layout")).not.toBeNull();
    expect(screen.queryByTestId("current-user-provider")).toBeNull();
    await waitFor(() => expect(signIn).toHaveBeenCalledTimes(1));
    expect(sessionStorage.getItem(POST_LOGIN_REDIRECT_KEY)).toBe("/cases/abc123");
  });

  it("tries a silent sign-in before the interactive redirect", async () => {
    signInSilently.mockResolvedValue(true);
    renderAt("/cases/abc123");

    await waitFor(() => expect(signInSilently).toHaveBeenCalledTimes(1));
    expect(signIn).not.toHaveBeenCalled();
  });

  it("does not save the bare root as a post-login redirect", async () => {
    renderAt("/");

    await waitFor(() => expect(signIn).toHaveBeenCalledTimes(1));
    expect(sessionStorage.getItem(POST_LOGIN_REDIRECT_KEY)).toBeNull();
  });

  it("renders the protected tree when signed in", () => {
    authState.isSignedIn = true;
    renderAt("/cases/abc123");

    expect(screen.queryByTestId("current-user-provider")).not.toBeNull();
    expect(signIn).not.toHaveBeenCalled();
  });

  it("starts no sign-in from the silent-re-auth iframe's own boot of the app", async () => {
    // That iframe shares this origin (and so sessionStorage) with the real page:
    // a nested authorize round-trip nobody awaits, and the real page's deep link
    // overwritten with the iframe's URL.
    isTopLevel.value = false;
    sessionStorage.setItem(POST_LOGIN_REDIRECT_KEY, "/cases/the-real-one");

    renderAt("/?error=login_required&state=instance_0_request_0");

    await waitFor(() => expect(screen.queryByTestId("app-layout")).not.toBeNull());
    expect(signIn).not.toHaveBeenCalled();
    expect(signInSilently).not.toHaveBeenCalled();
    expect(sessionStorage.getItem(POST_LOGIN_REDIRECT_KEY)).toBe(
      "/cases/the-real-one",
    );
  });

  it("shows the loader without starting sign-in while auth is resolving", () => {
    authState.isLoading = true;
    renderAt("/cases/abc123");

    expect(screen.queryByTestId("app-layout")).not.toBeNull();
    expect(signIn).not.toHaveBeenCalled();
  });
});
