import type { LiveChatEvent } from "./types.js";

/**
 * Normalizes one decoded SSE data payload (untrusted JSON from the
 * network) into a public {@link LiveChatEvent}, or null when it should not
 * be surfaced to the consumer at all.
 *
 * The wire -> public mapping (verified against the actual code paths that
 * push each event, not just the design doc -- see
 * apps/csm-portal/backend/internal/handler/chat.go's notifyOrigin call
 * sites and chat_timeout_sweeper.go):
 *
 *   queued               -> { type: "queued", message }
 *   engineer_assigned    -> { type: "assigned", engineerEmail }
 *   engineer_message     -> { type: "message", content, engineerEmail }
 *   engineer_disconnected -> { type: "disconnected", engineerEmail }
 *   converted_to_case    -> { type: "converted", engineerEmail, entityCaseId }
 *   chat_abandoned       -> { type: "expired", message }
 *
 * Every field above is unconditionally set by the backend at every call
 * site that emits that event type (confirmed by reading each one, not
 * assumed) -- this normalizer validates that at runtime anyway (raw JSON
 * over the wire is untrusted input regardless of what the server is
 * supposed to guarantee) and returns null if a required field is missing
 * or the wrong type, rather than constructing a LiveChatEvent with a
 * silently-wrong shape.
 *
 * Every other wire type (case_timed_out, customer_escalation,
 * customer_message, session_accepted, session_closed) is CSM-internal
 * only and is never pushed to a case's origin/SDK stream in the first
 * place -- but if one somehow arrived here (a future backend change, a
 * misconfigured relay), or a type this SDK version simply doesn't know
 * about yet arrives, the default case below ignores it silently rather
 * than surfacing a public "error" event or throwing. This SDK's "error"
 * event is reserved for transport/connection failures (see sse.ts) --
 * an unrecognized *application* event is a forward-compatibility
 * situation, not a connection problem, and a consumer's UI should not be
 * interrupted by an event type it doesn't understand yet.
 */
export function normalizeWireEvent(raw: unknown): LiveChatEvent | null {
  if (!isRecord(raw) || !isNonEmptyString(raw.type)) {
    return null;
  }

  switch (raw.type) {
    case "queued":
      return isNonEmptyString(raw.message) ? { type: "queued", message: raw.message } : null;

    case "engineer_assigned":
      return isNonEmptyString(raw.engineerEmail)
        ? { type: "assigned", engineerEmail: raw.engineerEmail }
        : null;

    case "engineer_message":
      return isNonEmptyString(raw.message) && isNonEmptyString(raw.engineerEmail)
        ? { type: "message", content: raw.message, engineerEmail: raw.engineerEmail }
        : null;

    case "engineer_disconnected":
      return isNonEmptyString(raw.engineerEmail)
        ? { type: "disconnected", engineerEmail: raw.engineerEmail }
        : null;

    case "converted_to_case":
      return isNonEmptyString(raw.engineerEmail) && isNonEmptyString(raw.entityCaseId)
        ? { type: "converted", engineerEmail: raw.engineerEmail, entityCaseId: raw.entityCaseId }
        : null;

    case "chat_abandoned":
      return isNonEmptyString(raw.message) ? { type: "expired", message: raw.message } : null;

    default:
      return null;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isNonEmptyString(value: unknown): value is string {
  return typeof value === "string" && value.length > 0;
}
