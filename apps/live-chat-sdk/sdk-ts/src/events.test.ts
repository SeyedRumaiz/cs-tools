import { describe, expect, it } from "vitest";
import { normalizeWireEvent } from "./events.js";

describe("normalizeWireEvent", () => {
  it("maps queued", () => {
    expect(
      normalizeWireEvent({
        type: "queued",
        caseId: "c1",
        conversationId: "conv1",
        message: "You're #2 in the queue.",
        timestamp: "2026-01-01T00:00:00Z"
      })
    ).toEqual({ type: "queued", message: "You're #2 in the queue." });
  });

  it("maps engineer_assigned -> assigned", () => {
    expect(normalizeWireEvent({ type: "engineer_assigned", engineerEmail: "eng@example.com" })).toEqual({
      type: "assigned",
      engineerEmail: "eng@example.com"
    });
  });

  it("maps engineer_message -> message", () => {
    expect(
      normalizeWireEvent({ type: "engineer_message", engineerEmail: "eng@example.com", message: "hi there" })
    ).toEqual({ type: "message", content: "hi there", engineerEmail: "eng@example.com" });
  });

  it("maps engineer_disconnected -> disconnected", () => {
    expect(normalizeWireEvent({ type: "engineer_disconnected", engineerEmail: "eng@example.com" })).toEqual({
      type: "disconnected",
      engineerEmail: "eng@example.com"
    });
  });

  it("maps converted_to_case -> converted", () => {
    expect(
      normalizeWireEvent({
        type: "converted_to_case",
        engineerEmail: "eng@example.com",
        entityCaseId: "entity-1"
      })
    ).toEqual({ type: "converted", engineerEmail: "eng@example.com", entityCaseId: "entity-1" });
  });

  it("maps chat_abandoned -> expired", () => {
    expect(normalizeWireEvent({ type: "chat_abandoned", message: "No engineer was available." })).toEqual({
      type: "expired",
      message: "No engineer was available."
    });
  });

  it("ignores an unrecognized event type (forward compatibility)", () => {
    expect(normalizeWireEvent({ type: "some_future_event", message: "hi" })).toBeNull();
  });

  it("ignores a CSM-internal-only event type that should never reach here", () => {
    expect(normalizeWireEvent({ type: "session_accepted", engineerEmail: "eng@example.com" })).toBeNull();
  });

  it("ignores a recognized type missing a required field", () => {
    expect(normalizeWireEvent({ type: "engineer_message", engineerEmail: "eng@example.com" })).toBeNull();
  });

  it("ignores a recognized type with a wrong-typed field", () => {
    expect(normalizeWireEvent({ type: "queued", message: 42 })).toBeNull();
  });

  it("ignores non-object input", () => {
    expect(normalizeWireEvent("queued")).toBeNull();
    expect(normalizeWireEvent(null)).toBeNull();
    expect(normalizeWireEvent(42)).toBeNull();
    expect(normalizeWireEvent(["queued"])).toBeNull();
  });

  it("ignores an object with no type field", () => {
    expect(normalizeWireEvent({ message: "hi" })).toBeNull();
  });
});
