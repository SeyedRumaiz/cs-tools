import { describe, expect, it } from "vitest";
import { errorFromResponse, isAbortError, parseErrorBody } from "./errors.js";

describe("parseErrorBody", () => {
  it("uses the server's message field when present", () => {
    expect(parseErrorBody('{"message":"bad request"}', "fallback")).toEqual({
      message: "bad request",
      details: { message: "bad request" }
    });
  });

  it("falls back for an empty body", () => {
    expect(parseErrorBody("", "fallback")).toEqual({ message: "fallback" });
  });

  it("falls back for non-JSON HTML content", () => {
    expect(parseErrorBody("<html>error</html>", "fallback")).toEqual({ message: "fallback" });
  });

  it("falls back when JSON has no usable message field", () => {
    expect(parseErrorBody('{"foo":"bar"}', "fallback")).toEqual({ message: "fallback", details: { foo: "bar" } });
  });
});

describe("errorFromResponse", () => {
  it("sets status and message", () => {
    const err = errorFromResponse(403, '{"message":"forbidden"}');
    expect(err.status).toBe(403);
    expect(err.message).toBe("forbidden");
    expect(err.code).toBeUndefined();
  });
});

describe("isAbortError", () => {
  it("recognizes a DOMException named AbortError", () => {
    expect(isAbortError(new DOMException("aborted", "AbortError"))).toBe(true);
  });

  it("rejects any other error", () => {
    expect(isAbortError(new Error("network down"))).toBe(false);
    expect(isAbortError("not an error")).toBe(false);
  });
});
