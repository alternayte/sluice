/** stateColors maps execution states to the state tokens of the visual system (REQ-UI-010). */
const stateColors: Record<string, string> = {
  success: "var(--state-success)",
  failed: "var(--state-failed)",
  timed_out: "var(--state-timed-out)",
  cancelled: "var(--state-cancelled)",
  skipped: "var(--state-skipped)",
  running: "var(--state-running)",
  cancelling: "var(--state-running)",
  queued: "var(--state-queued)",
};

/** stateColor returns the CSS color of an execution state. */
export function stateColor(state: string): string {
  return stateColors[state.toLowerCase()] ?? "var(--state-queued)";
}

/** formatMs formats a duration in milliseconds: 340 ms, 1.2 s, 2 min 5 s. */
export function formatMs(ms: number | null | undefined): string {
  if (ms === null || ms === undefined) return "—";
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  const min = Math.floor(ms / 60_000);
  const sec = Math.round((ms % 60_000) / 1000);
  return `${min} min ${sec} s`;
}

/** formatRate formats a success rate from 0 to 1 as a percentage with one decimal. */
export function formatRate(rate: number | null | undefined): string {
  if (rate === null || rate === undefined) return "—";
  return `${(rate * 100).toFixed(1)}%`;
}

/** pointCount returns the number of numeric values of key in rows. */
export function pointCount(rows: Record<string, unknown>[], key: string): number {
  return rows.filter((r) => typeof r[key] === "number" && Number.isFinite(r[key])).length;
}

/**
 * needsDots reports whether a line series needs dots to be visible. A line with one
 * point draws no segment, so a series with fewer than two points shows dots.
 */
export function needsDots(rows: Record<string, unknown>[], key: string): boolean {
  return pointCount(rows, key) < 2;
}
