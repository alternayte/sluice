/** triggerSummary returns a short text for a trigger config, for example "0 * * * * (Europe/Berlin)". */
export function triggerSummary(type: string, config: Record<string, unknown>): string {
  if (type === "schedule") {
    const cron = typeof config.cron === "string" ? config.cron : "";
    const tz = typeof config.timezone === "string" && config.timezone ? config.timezone : "UTC";
    if (cron) return `${cron} (${tz})`;
  }
  const parts: string[] = [];
  for (const [k, v] of Object.entries(config)) {
    if (v === null || v === undefined) continue;
    if (typeof v === "string" || typeof v === "number" || typeof v === "boolean") parts.push(`${k}: ${String(v)}`);
    else if (Array.isArray(v) && v.every((x) => typeof x === "string" || typeof x === "number")) {
      parts.push(`${k}: ${v.join(", ")}`);
    }
  }
  return parts.join("; ");
}

/** StateTone is the color group of an execution state. */
export type StateTone = "success" | "failed" | "warning" | "accent" | "neutral";

/** executionStateTone returns the color group of an execution state. */
export function executionStateTone(state: string): StateTone {
  switch (state) {
    case "success":
    case "succeeded":
      return "success";
    case "failed":
      return "failed";
    case "timed_out":
    case "warning":
      return "warning";
    case "running":
    case "retrying":
      return "accent";
    default:
      return "neutral";
  }
}

/** stateLabel turns a state such as "timed_out" into "Timed out". */
export function stateLabel(state: string): string {
  const s = state.replace(/_/g, " ");
  return s.charAt(0).toUpperCase() + s.slice(1);
}
