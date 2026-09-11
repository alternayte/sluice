import { client } from "@/api/client.gen";
import type { ErrorEnvelope } from "@/api/types.gen";

/** ApiError carries the error envelope {"error":{"code","message","details"}}. */
export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public details?: unknown,
  ) {
    super(message);
  }
}

const unauthorized = new Set<() => void>();

/** onUnauthorized registers a callback for 401 responses. */
export function onUnauthorized(fn: () => void) {
  unauthorized.add(fn);
  return () => unauthorized.delete(fn);
}

client.setConfig({ baseUrl: "", credentials: "same-origin", throwOnError: true });

client.interceptors.response.use((response) => {
  if (response.status === 401) unauthorized.forEach((fn) => fn());
  return response;
});

/**
 * toApiError converts a failed response body into an ApiError. It returns the original
 * error unchanged when there is no response, for example a network error from fetch itself.
 * TanStack Query retries a plain error but not an ApiError with a status below 500, so a
 * network error must stay a plain error to get the same retries as before.
 */
export function toApiError(error: unknown, response: Response | undefined): unknown {
  if (!response) return error;
  const env = (error ?? {}) as Partial<ErrorEnvelope>;
  return new ApiError(
    response.status,
    env.error?.code ?? "http_error",
    env.error?.message ?? response.statusText ?? "request failed",
    env.error?.details,
  );
}

client.interceptors.error.use(toApiError);

/**
 * rawFetch runs a fetch request outside the generated client, for hand-written streamed
 * endpoints (file content, file upload). It applies the same credentials, 401 callback and
 * ApiError behaviour as the generated client.
 */
export async function rawFetch(input: string, init?: RequestInit): Promise<Response> {
  const response = await fetch(input, { credentials: "same-origin", ...init });
  if (response.status === 401) unauthorized.forEach((fn) => fn());
  if (!response.ok) {
    let body: unknown;
    try {
      body = await response.json();
    } catch {
      body = undefined;
    }
    const env = (body ?? {}) as Partial<ErrorEnvelope>;
    throw new ApiError(
      response.status,
      env.error?.code ?? "http_error",
      env.error?.message ?? response.statusText,
      env.error?.details,
    );
  }
  return response;
}
