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

import { type JSX, useEffect, useRef, useState } from "react";
import { useAsgardeo } from "@asgardeo/react";
import { ProtectedRoute } from "@asgardeo/react-router";
import { useLocation, useNavigate } from "react-router";
import AppLayout from "@layouts/AppLayout";
import { POST_LOGIN_REDIRECT_KEY } from "@layouts/postLoginRedirect";
import { isTopLevelWindow } from "@utils/isTopLevelWindow";
import {
  CurrentUserProvider,
  useCurrentUser,
} from "@context/current-user/CurrentUserContext";
import RouteSuspenseFallback from "@components/route-fallback/RouteSuspenseFallback";
import NoPortalAccessPage from "@components/error/NoPortalAccessPage";
import EngineerAlertNotification from "@features/csm-chat/components/EngineerAlertNotification";
import { useLogger } from "@hooks/useLogger";
import { trySilentSignInOnce } from "@hooks/silentSignIn";
import { isForbiddenError, isUnauthorizedError } from "@utils/ApiError";

/**
 * The app shell with the routed page deliberately suppressed.
 *
 * `AppLayout` renders `children || <Outlet />`, so passing children keeps the
 * chrome visible without mounting the route underneath. That matters before a
 * session exists: pages reached this way call `useCurrentUser`, which throws
 * outside `CurrentUserProvider` — a bare `<AppLayout />` here took down the
 * whole tree on any deep link (`/cases/:id` among them) while auth was still
 * resolving or the sign-in redirect was in flight. Suppressing the page also
 * keeps it from firing authenticated requests that are certain to 401.
 *
 * @returns {JSX.Element} The app shell showing progress in the content region.
 */
function AuthPendingShell(): JSX.Element {
  return (
    // showCaseTabs={false}: there is no CurrentUserProvider in scope here, and
    // AppLayout's own CaseTabsContentHost renders a restored open case tab's
    // page unconditionally (alongside, not instead of, these children) —
    // that page calls useCurrentUser too (via useFindMyOngoingCases), which
    // throws outside the provider. Same crash the children-suppression above
    // already guards against, just via a different render path.
    <AppLayout showCaseTabs={false}>
      <RouteSuspenseFallback />
    </AppLayout>
  );
}

/**
 * Starts sign-in for a signed-out visitor and renders the app shell while the
 * browser navigates to the IdP.
 *
 * This is passed to `ProtectedRoute` as `fallback` rather than driving sign-in
 * from its `onSignIn` prop. In `@asgardeo/react-router` 2.0.0 the `onSignIn`
 * branch does not return: it invokes the handler and then falls straight
 * through to an unconditional `throw new AsgardeoRuntimeError("ProtectedRoute
 * misconfiguration.")`. Sign-in here is asynchronous (a silent attempt first,
 * then the interactive redirect), so the throw always won the race and every
 * signed-out visit — an expired session, a case deep link opened in a cold tab
 * — hit the app error boundary before the redirect could start. Only the
 * `fallback` and `redirectTo` branches return early, so `fallback` is the one
 * place that can both start sign-in and keep the tree alive. Go back to
 * `onSignIn` only once that upstream fall-through is fixed.
 *
 * The intended URL is saved first so the deep link survives the round-trip, and
 * the ref guard keeps a re-render (or StrictMode's double-invoked effect) from
 * firing a second authorize request.
 *
 * @returns {JSX.Element} The app shell, held until the IdP redirect lands.
 */
function SignInRedirect(): JSX.Element {
  const { signIn, signInSilently } = useAsgardeo();
  const location = useLocation();
  const logger = useLogger();
  const started = useRef(false);
  // Only a sign-in that cannot even start belongs on the error boundary. It is
  // stored and rethrown from render rather than thrown from the effect, which
  // React would otherwise leave as an unhandled rejection.
  const [fatal, setFatal] = useState<Error | null>(null);
  if (fatal) throw fatal;

  useEffect(() => {
    if (started.current) return;
    // The silent-re-auth iframe boots the whole app on this same origin and
    // lands here too, signed out. Starting a sign-in from inside it would run a
    // second, nested authorize round-trip that no one is waiting on, and clobber
    // the real page's saved deep link. The SDK drives that frame; leave it be.
    if (!isTopLevelWindow()) return;
    started.current = true;

    const intended = location.pathname + location.search + location.hash;
    if (intended !== "/") {
      sessionStorage.setItem(POST_LOGIN_REDIRECT_KEY, intended);
    }

    void (async () => {
      const recovered = await trySilentSignInOnce(signInSilently, (message) =>
        logger.debug("[auth] silent sign-in failed", message),
      );
      // A silent success flips `isSignedIn`, and ProtectedRoute swaps this
      // fallback out for the real tree — nothing more to do here.
      if (recovered) return;
      try {
        await signIn();
      } catch (error) {
        setFatal(error instanceof Error ? error : new Error(String(error)));
      }
    })();
  }, [
    signIn,
    signInSilently,
    logger,
    location.pathname,
    location.search,
    location.hash,
  ]);

  return <AuthPendingShell />;
}

/**
 * Renders the app shell, unless `/users/me` (via `CurrentUserProvider`)
 * failed with a 401 or 403 that survived `useAuthApiClient`'s own recovery
 * chain — that chain already retries/silently refreshes/gives up (see
 * `useGetUsersMe.ts`'s `skipSignInRedirect`) on a recoverable 401, so an
 * error still reaching here means the caller is genuinely not entitled to
 * use this portal, not just a transient expired token.
 *
 * The routed page is suppressed (same `children` vs. bare `<AppLayout />`
 * mechanic `AuthPendingShell` above uses, for the same reason) for as long
 * as that check is still pending, not just once it comes back positive. The
 * routed page's own components fire their own API calls the moment they
 * mount — a dashboard widget, a case list — regardless of whether this
 * caller turns out to be authorized, so mounting it before `/users/me` has
 * settled let an unauthorized caller see real response data (and use the
 * sidebar to reach other real pages) during that window. A signed-in user
 * only ever sees the routed page once we positively know they're allowed to.
 *
 * Renders through `AppLayout` itself (not a bare error layout) so the real
 * header/branding still shows — this is a signed-in user, just one without
 * (confirmed or yet-to-be-confirmed) access, not someone who has left the
 * portal. The sidebar has nothing to navigate to in either the loading or
 * the not-authorized state, so it's suppressed via `AppLayout`'s
 * `minimalHeader` prop — applied in the very same render as `children`
 * below, not a context flag settled a render later via an effect, so it
 * never flashes on screen for a frame before disappearing.
 *
 * @returns {JSX.Element} AppLayout, showing a loading state, the routed
 * page, or the "not authorized" page in its content area.
 */
function AuthorizedAppShell(): JSX.Element {
  const { isLoading, isError, error } = useCurrentUser();
  const notAuthorized =
    isError && (isUnauthorizedError(error) || isForbiddenError(error));

  if (isLoading) {
    return (
      <AppLayout minimalHeader>
        <RouteSuspenseFallback />
      </AppLayout>
    );
  }

  if (notAuthorized) {
    return (
      <AppLayout minimalHeader>
        <NoPortalAccessPage />
      </AppLayout>
    );
  }

  return (
    <>
      <AppLayout />
      <EngineerAlertNotification />
    </>
  );
}

/**
 * AuthGuard renders AppLayout (header/footer) so loading state is visible
 * and the IdP authentication flow can be observed. Redirects to home only
 * when not signed in and auth check is complete.
 *
 * Preserves the intended URL across the IdP sign-in redirect so that
 * deep-links (e.g. ServiceNow case links) land on the correct page after auth.
 *
 * Note: the customer-portal behaviour of auto-redirecting `/` to the last
 * visited project's dashboard is intentionally NOT replicated here. CSM is
 * engineer-scoped, so the landing route `/` resolves to the ABT dashboard
 * via App.tsx instead.
 *
 * @returns {JSX.Element} AppLayout or redirect to home.
 */
export default function AuthGuard(): JSX.Element {
  const { isSignedIn } = useAsgardeo();
  const location = useLocation();
  const navigate = useNavigate();

  // Latches true the first time `isSignedIn` is observed true, and never
  // resets — see the render branch below for why. Set directly in the render
  // body (React's documented "adjusting state during rendering" pattern, not
  // an effect) so the very same render that first sees `isSignedIn` also
  // switches branches, instead of committing one extra render through
  // `ProtectedRoute` first.
  const [hasSignedInOnce, setHasSignedInOnce] = useState(false);
  // One-way latch, separate from `hasSignedInOnce`: once an explicit
  // sign-out starts, the "bypass ProtectedRoute" branch below must stop
  // unconditionally, and stay stopped, regardless of what `isSignedIn` does
  // afterward. `app:signing-out` (this app's existing signal, dispatched by
  // every manual "Sign out" action in `UserProfile.tsx`/
  // `IdleTimeoutProvider.tsx`) fires *before* the SDK's `signOut()` call —
  // i.e. before `isSignedIn` has necessarily flipped to `false` yet. A
  // reset tied to `isSignedIn` directly would race the render-time
  // `hasSignedInOnce` latch below (still seeing `isSignedIn === true` on
  // the very next render) and get immediately re-latched back to `true` in
  // the same update; `isSigningOut` sidesteps that race entirely by not
  // depending on `isSignedIn` at all once it's set.
  const [isSigningOut, setIsSigningOut] = useState(false);
  if (isSignedIn && !hasSignedInOnce && !isSigningOut) {
    setHasSignedInOnce(true);
  }

  // Without this, an explicit sign-out was indistinguishable from a
  // transient token-clock expiry once `hasSignedInOnce` had latched true —
  // both just flip `isSignedIn` to `false` eventually — and this component
  // would keep `CurrentUserProvider`/`AppLayout` mounted with the
  // just-signed-out user's data during the brief window before the SDK's
  // `signOut()` redirect actually navigates away, instead of falling back
  // to `ProtectedRoute`'s neutral loader.
  useEffect(() => {
    const handleSigningOut = (): void => setIsSigningOut(true);
    window.addEventListener("app:signing-out", handleSigningOut);
    return () => window.removeEventListener("app:signing-out", handleSigningOut);
  }, []);

  // After login, restore the saved deep link so it survives the Asgardeo SDK
  // reloading the page to `afterSignInUrl` ("/") after the callback (which would
  // otherwise drop us on the default landing). The key is consumed by
  // PostLoginRedirectConsumer once we arrive at the target — that consumer runs
  // above <Routes> so it also clears the key for routes AuthGuard never mounts
  // (e.g. the 404 page); clearing here would strand the key on a dead deep link
  // and bounce the next `/` visit back to it. The default `/` landing is
  // deferred to RootLanding in App.tsx while a redirect is pending.
  useEffect(() => {
    if (!isSignedIn) return;
    const redirect = sessionStorage.getItem(POST_LOGIN_REDIRECT_KEY);
    if (!redirect) return;
    // Compare (and restore) the full location including the hash, so anchor
    // permalinks like `/cases/:id#description` are honoured, not stripped.
    const here = location.pathname + location.search + location.hash;
    if (here !== redirect) {
      void navigate(redirect, { replace: true });
    }
  }, [isSignedIn, navigate, location.pathname, location.search, location.hash]);

  // Once a session has been established at least once, never again let
  // `ProtectedRoute` hide the app behind its `loader` for a transient
  // client-side token-clock expiry. `ProtectedRoute` swaps to `loader`
  // (unmounting `children`) for as long as `isSignedIn` is false, however
  // briefly and however recoverable — that is a full React-level unmount of
  // everything under it, indistinguishable in effect from a page reload even
  // though the browser itself never navigates. Live reproduction confirmed
  // this destroys in-progress work (an open case-comment composer and its
  // draft) within the same instant the token's local clock expires, well
  // before any silent-reauth attempt could even begin — a plain in-memory
  // marker on `window` survived the whole cycle while the React tree's own
  // state did not.
  //
  // No background effect proactively re-runs `signInSilently()` here for
  // this case (an earlier version of this fix added one, driven by
  // `isSignedIn` transitions) — it raced `useAuthApiClient.ts`'s own
  // call-level recovery: the shared single-flight guard only dedupes
  // *concurrent* attempts, so a background poller and a real failing
  // request could still each trigger their own attempt back-to-back, and
  // that duplicate cycle was observed to leave an in-flight mutation's own
  // promise chain stuck (a case comment created successfully server-side,
  // but the composer's "Sending…" state never cleared). `useAuthApiClient`
  // already recovers correctly on any actual 401 from real use — once
  // signed in, that is the ONLY recovery trigger this app needs; a token
  // expiring while the user does nothing at all needs no proactive fix.
  if (hasSignedInOnce && !isSigningOut) {
    return (
      <CurrentUserProvider>
        <AuthorizedAppShell />
      </CurrentUserProvider>
    );
  }

  return (
    <ProtectedRoute loader={<AuthPendingShell />} fallback={<SignInRedirect />}>
      <CurrentUserProvider>
        <AuthorizedAppShell />
      </CurrentUserProvider>
    </ProtectedRoute>
  );
}
