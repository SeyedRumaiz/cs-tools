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

// Base URL for the API service.
export const BACKEND_BASE_URL = window.config?.CUSTOMER_PORTAL_BACKEND_BASE_URL;

if (!BACKEND_BASE_URL) {
  throw new Error(
    "Missing required configuration: CUSTOMER_PORTAL_BACKEND_BASE_URL",
  );
}

/**
 * Rejects a stream URL that isn't safe to send credentials to.
 * useCaseActivityStream puts the caller's ID token in three request headers,
 * so a `http://` URL would carry it across the network in cleartext — and
 * this value is deployment config rather than something the app controls, so
 * a typo is the realistic way that happens.
 *
 * An unusable value is treated as unset (the feature is simply off) rather
 * than thrown, unlike BACKEND_BASE_URL below: the stream is optional to begin
 * with, so a bad value here must not take the whole portal down with it.
 * Loopback over http is allowed so the stream service can be run locally.
 */
function secureStreamUrl(url: string | undefined): string | undefined {
  if (!url) return undefined;
  let parsed: URL;
  try {
    parsed = new URL(url);
  } catch {
    return undefined;
  }
  if (parsed.protocol === "https:") return url;
  const isLoopback = ["localhost", "127.0.0.1", "[::1]", "::1"].includes(
    parsed.hostname,
  );
  return parsed.protocol === "http:" && isLoopback ? url : undefined;
}

// Base URL for the case-activity SSE stream (customer-portal-activity-stream-service,
// its own Choreo component — not a path under backendUrl). Optional: that
// service only stands the stream listener up when Event Hub is configured,
// so useCaseActivityStream checks for this and no-ops rather than throwing,
// unlike BACKEND_BASE_URL above.
const STREAM_BASE_URL = secureStreamUrl(
  window.config?.CUSTOMER_PORTAL_STREAM_BASE_URL,
);

// Master on/off switch for the case-activity SSE stream, independent of
// whether STREAM_BASE_URL is set. Strict `=== true` (rather than the usual
// `?? false`) so only the literal boolean turns it on — any config predating
// this key evaluates to false, which is what makes it safe by default.
const STREAM_ENABLED = window.config?.CUSTOMER_PORTAL_STREAM_ENABLED === true;

// Interface for the API configuration.
interface ApiConfig {
  backendUrl: string;
  streamUrl?: string;
  streamEnabled: boolean;
}

// Configuration for the API service.
export const apiConfig: ApiConfig = {
  backendUrl: BACKEND_BASE_URL,
  streamUrl: STREAM_BASE_URL,
  streamEnabled: STREAM_ENABLED,
};
