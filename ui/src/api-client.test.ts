import { describe, expect, it } from "vitest";
import { ApiError, toApiError } from "./api-client";

describe("toApiError", () => {
  it("returns the original error unchanged when there is no response (a network error)", () => {
    const networkError = new TypeError("Failed to fetch");
    expect(toApiError(networkError, undefined)).toBe(networkError);
  });

  it("builds an ApiError from the error envelope when there is a response", () => {
    const response = new Response(null, { status: 422, statusText: "Unprocessable" });
    const result = toApiError({ error: { code: "validation_failed", message: "Bad input" } }, response);
    expect(result).toBeInstanceOf(ApiError);
    const err = result as ApiError;
    expect(err.status).toBe(422);
    expect(err.code).toBe("validation_failed");
    expect(err.message).toBe("Bad input");
  });

  it("falls back to http_error and the status text when the body has no envelope", () => {
    const response = new Response(null, { status: 500, statusText: "Server Error" });
    const result = toApiError(undefined, response) as ApiError;
    expect(result.status).toBe(500);
    expect(result.code).toBe("http_error");
    expect(result.message).toBe("Server Error");
  });
});
