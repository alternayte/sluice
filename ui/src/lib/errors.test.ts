import { describe, expect, it } from "vitest";
import { ApiError } from "@/api/client";
import { errorMessage, fieldErrors } from "./errors";

describe("errorMessage", () => {
  it("maps last_admin and 429", () => {
    expect(errorMessage(new ApiError(409, "last_admin", "x"))).toBe("At least one enabled admin must remain.");
    expect(errorMessage(new ApiError(429, "rate_limited", "x"))).toContain("Too many attempts");
  });

  it("maps namespace conflicts", () => {
    expect(errorMessage(new ApiError(409, "executions_running", "x"))).toBe("Executions of this namespace are running.");
    expect(errorMessage(new ApiError(409, "version_conflict", "x"))).toBe(
      "The namespace changed. Reload to see the latest version.",
    );
    expect(errorMessage(new ApiError(413, "too_large", "File is larger than 10 MiB"))).toBe("File is larger than 10 MiB");
  });

  it("returns the API message", () => {
    expect(errorMessage(new ApiError(400, "bad", "Bad input"))).toBe("Bad input");
  });
});

describe("fieldErrors", () => {
  it("reads validation details", () => {
    const err = new ApiError(422, "validation_failed", "Invalid", [
      { field: "new_password", message: "Too short" },
      { field: 3, message: "ignored" },
    ]);
    expect(fieldErrors(err)).toEqual({ new_password: "Too short" });
    expect(fieldErrors(new Error("x"))).toEqual({});
  });
});
