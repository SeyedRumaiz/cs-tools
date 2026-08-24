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

import { useCallback } from "react";
import { apiConfig } from "@config/apiConfig";
import { useAuthTokens } from "@hooks/useAuthTokens";
import { useLogger } from "@hooks/useLogger";
import { CORRELATION_ID_HEADER, newCorrelationId } from "@utils/correlationId";

// Origin we are willing to attach the bearer token to. Computed once at module
// load so we don't accidentally send credentials anywhere else.
const trustedBackendOrigin = (() => {
  try {
    return new URL(apiConfig.backendUrl).origin;
  } catch {
    return "";
  }
})();

function resolveRequestUrl(input: RequestInfo | URL): URL {
  if (input instanceof Request) return new URL(input.url, window.location.origin);
  if (input instanceof URL) return input;
  return new URL(input.toString(), window.location.origin);
}

function buildRequestHeaders(
  input: RequestInfo | URL,
  options: RequestInit | undefined,
  token: string,
  idToken: string,
  correlationId: string,
): Headers {
  // When `input` is a Request, `init.headers` on the outer fetch call REPLACES
  // the request's headers wholesale — it does not merge. Seed the headers from
  // the Request and let any explicit option-level headers override.
  const headers =
    input instanceof Request ? new Headers(input.headers) : new Headers();
  if (options?.headers) {
    new Headers(options.headers).forEach((value, key) => {
      headers.set(key, value);
    });
  }

  // headers.set("Authorization", `Bearer ${token}`);
  // The ID token travels alongside the access token (same convention as the
  // customer portal): the gateway validates the bearer, while the backend
  // reads the user's identity claims from `x-user-id-token`.
  headers.set("x-jwt-assertion", token);
  headers.set("x-user-id-token", idToken);
  // Correlation ID for end-to-end tracing. The backend honours an inbound value
  // and only generates its own when absent, so a caller-supplied header (rare:
  // a retry that wants to reuse an ID) is preserved; otherwise we stamp a fresh
  // per-request UUID.
  if (!headers.has(CORRELATION_ID_HEADER)) {
    headers.set(CORRELATION_ID_HEADER, correlationId);
  }
  if (!headers.has("Accept")) {
    headers.set("Accept", "application/json");
  }

  // Inherit method/body from the Request when callers omit them in `options`.
  const method =
    options?.method?.toUpperCase() ||
    (input instanceof Request ? input.method.toUpperCase() : "GET");
  const body =
    options?.body ?? (input instanceof Request ? input.body : undefined);

  if (["POST", "PUT", "PATCH"].includes(method) && body) {
    const isNonJsonType =
      body instanceof FormData ||
      body instanceof Blob ||
      body instanceof ArrayBuffer ||
      (typeof URLSearchParams !== "undefined" && body instanceof URLSearchParams) ||
      (typeof ReadableStream !== "undefined" && body instanceof ReadableStream) ||
      ArrayBuffer.isView(body);

    if (!isNonJsonType && !headers.has("Content-Type")) {
      headers.set("Content-Type", "application/json");
    }
  }

  return headers;
}

// Fetch wrapper that attaches a fresh IdP access token as the bearer and the
// ID token as `x-user-id-token` (the customer portal's convention). The
// Choreo gateway validates the access token and forwards it upstream as
// `x-jwt-assertion`, which csm-portal-backend reads in its auth middleware;
// `x-user-id-token` passes through to the backend untouched.
// The tokens are only attached when the request origin matches the configured
// backend; calls to any other origin are refused so credentials can't be
// leaked to third-party hosts.
export function useAuthApiClient() {
  const getTokens = useAuthTokens();
  const logger = useLogger();

  return useCallback(
    async (input: RequestInfo | URL, options?: RequestInit): Promise<Response> => {
      const url = resolveRequestUrl(input);
      if (!trustedBackendOrigin || url.origin !== trustedBackendOrigin) {
        throw new Error(
          `Refusing to send access token to untrusted origin ${url.origin}`,
        );
      }

      // Recovers from a dead refresh token internally — retry, then a
      // silent hidden-iframe re-auth, then a full sign-in redirect as the
      // last resort — see useAuthTokens. Anything that still escapes here
      // is not a token problem (auth-not-ready, or an unexpected error).
      const { token, idToken } = await getTokens();

      // One correlation ID per physical request (React Query retries each get a
      // distinct one, matching the backend's per-request unit). A caller that
      // pre-set the header keeps its value; we log whichever ID actually ships.
      const headers = buildRequestHeaders(
        input,
        options,
        token,
        idToken,
        newCorrelationId(),
      );
      const correlationId = headers.get(CORRELATION_ID_HEADER) ?? "";
      const method = (
        options?.method ??
        (input instanceof Request ? input.method : "GET")
      ).toUpperCase();

      // Centralised FE access log, mirroring the backend's request-logging
      // middleware: every backend call is logged once here with the same
      // correlation ID that backend + entity-service stamp on their log lines.
      try {
        const response = await fetch(input, { ...options, headers });
        const line = `[api] ${method} ${url.pathname} -> ${response.status} correlationID=${correlationId}`;
        if (response.ok) {
          logger.debug(line);
        } else {
          logger.error(line);
        }
        return response;
      } catch (error) {
        logger.error(
          `[api] ${method} ${url.pathname} -> network error correlationID=${correlationId}`,
          error,
        );
        throw error;
      }
    },
    [getTokens, logger],
  );
}
