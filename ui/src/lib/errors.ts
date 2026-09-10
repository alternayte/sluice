import { ApiError } from "@/api/client";

/** errorMessage returns a message for the user from an API or other error. */
export function errorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === "last_admin") return "At least one enabled admin must remain.";
    if (err.status === 429) return "Too many attempts. Wait and try again.";
    return err.message || "Request failed.";
  }
  if (err instanceof Error) return err.message;
  return "Request failed.";
}

/** fieldErrors maps 422 validation_failed details to field messages. */
export function fieldErrors(err: unknown): Record<string, string> {
  const out: Record<string, string> = {};
  if (!(err instanceof ApiError) || err.code !== "validation_failed" || !Array.isArray(err.details)) return out;
  for (const d of err.details as unknown[]) {
    if (d && typeof d === "object") {
      const { field, message } = d as { field?: unknown; message?: unknown };
      if (typeof field === "string" && typeof message === "string" && !out[field]) out[field] = message;
    }
  }
  return out;
}
