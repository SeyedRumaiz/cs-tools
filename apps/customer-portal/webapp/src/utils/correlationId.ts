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

/**
 * HTTP header used to carry a request's correlation ID end-to-end. The FE
 * originates the value on every backend call; customer-portal-backend-v2
 * honours the inbound header (only generating its own when absent) and
 * stamps it onto every log line, so a single ID ties the FE action to its
 * backend + entity-service log trail.
 *
 * Must match `correlationIDHeader` in
 * `apps/customer-portal/backend-v2/internal/middleware/correlation.go`.
 */
export const CORRELATION_ID_HEADER = "X-CSM-Correlation-ID";

/**
 * Prefix the backend stamps onto every correlation ID (adding it if the
 * inbound value doesn't already have one) so an ID can be told apart from
 * one originating in another portal/service sharing the same downstream
 * entity-service. Baking it in here too means the ID this client generates
 * and displays is byte-for-byte identical to what the backend actually logs
 * — not just "the backend will have added a prefix to this" — even when the
 * response header isn't readable back (see `getErrorReferenceId` below).
 *
 * Must match `correlationIDPrefix` in the same backend file.
 */
const CORRELATION_ID_PREFIX = "cp-";

/**
 * Returns a fresh, "cp-"-prefixed UUID v4 to use as a request correlation ID.
 *
 * Prefers the platform `crypto.randomUUID()` (available in every secure context
 * we ship to: HTTPS in production and `http://localhost` in dev, both of which
 * the spec treats as secure). Falls back to a `crypto.getRandomValues`-seeded
 * UUID v4, and finally to a non-cryptographic generator, so a correlation ID is
 * always produced rather than letting a missing API break a request.
 */
export function newCorrelationId(): string {
  const c = globalThis.crypto;
  if (c?.randomUUID) {
    return CORRELATION_ID_PREFIX + c.randomUUID();
  }
  if (c?.getRandomValues) {
    const b = c.getRandomValues(new Uint8Array(16));
    b[6] = (b[6] & 0x0f) | 0x40; // version 4
    b[8] = (b[8] & 0x3f) | 0x80; // variant
    const hex = Array.from(b, (n) => n.toString(16).padStart(2, "0"));
    return (
      CORRELATION_ID_PREFIX +
      hex.slice(0, 4).join("") +
      "-" +
      hex.slice(4, 6).join("") +
      "-" +
      hex.slice(6, 8).join("") +
      "-" +
      hex.slice(8, 10).join("") +
      "-" +
      hex.slice(10, 16).join("")
    );
  }
  // Last-resort fallback (non-cryptographic); only reached when the Web Crypto
  // API is entirely unavailable, which should not happen in supported runtimes.
  return (
    CORRELATION_ID_PREFIX +
    "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx".replace(/[xy]/g, (ch) => {
      const r = (Math.random() * 16) | 0;
      const v = ch === "x" ? r : (r & 0x3) | 0x8;
      return v.toString(16);
    })
  );
}

/**
 * Returns the correlation ID carried by an error (e.g. `ApiError`), or
 * `undefined`. Duck-typed on a `correlationId: string` field to avoid
 * coupling this util to the API-client module. Use to show users a support
 * "Tracking ID" alongside an error message.
 */
export function getErrorReferenceId(error: unknown): string | undefined {
  if (error && typeof error === "object" && "correlationId" in error) {
    const id = (error as { correlationId?: unknown }).correlationId;
    if (typeof id === "string" && id.length > 0) return id;
  }
  return undefined;
}
