import createClient, { type Middleware } from "openapi-fetch";
import type { paths } from "./schema";

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

const authMiddleware: Middleware = {
  async onResponse({ response }) {
    if (response.status === 401) unauthorized.forEach((fn) => fn());
    return response;
  },
};

export const api = createClient<paths>({ baseUrl: "/", credentials: "same-origin" });
api.use(authMiddleware);

type Envelope = { error?: { code?: string; message?: string; details?: unknown } };

/** unwrap returns data or throws ApiError. */
export function unwrap<T>(res: { data?: T; error?: unknown; response: Response }): T {
  if (res.error !== undefined || !res.response.ok) {
    const env = (res.error ?? {}) as Envelope;
    throw new ApiError(
      res.response.status,
      env.error?.code ?? "http_error",
      env.error?.message ?? res.response.statusText,
      env.error?.details,
    );
  }
  return res.data as T;
}
