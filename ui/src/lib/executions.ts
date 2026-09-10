/** Execution helpers without React: filters, run form coercion, Gantt geometry and log filtering. */

export const executionStates = [
  "QUEUED",
  "RUNNING",
  "CANCELLING",
  "SUCCESS",
  "FAILED",
  "TIMED_OUT",
  "CANCELLED",
  "SKIPPED",
] as const;

export const triggerTypes = ["manual", "schedule", "webhook", "flow", "file", "subflow", "rerun", "restart"] as const;
export type TriggerType = (typeof triggerTypes)[number];

const terminal = new Set(["SUCCESS", "FAILED", "TIMED_OUT", "CANCELLED", "SKIPPED"]);

/** isTerminal reports whether an execution or task run state is final. */
export function isTerminal(state: string): boolean {
  return terminal.has(state.toUpperCase());
}

/** canCancel reports whether an execution in this state can be cancelled. */
export function canCancel(state: string): boolean {
  return ["QUEUED", "RUNNING", "CANCELLING"].includes(state.toUpperCase());
}

/** canRestart reports whether "Restart from failed" applies to this state. */
export function canRestart(state: string): boolean {
  return ["FAILED", "TIMED_OUT", "CANCELLED"].includes(state.toUpperCase());
}

/** stateBarClass returns the background token class for a state bar. */
export function stateBarClass(state: string): string {
  switch (state.toUpperCase()) {
    case "SUCCESS":
      return "bg-state-success";
    case "FAILED":
      return "bg-state-failed";
    case "RUNNING":
    case "CANCELLING":
      return "bg-state-running";
    case "TIMED_OUT":
      return "bg-state-timed-out";
    case "CANCELLED":
      return "bg-state-cancelled";
    case "SKIPPED":
      return "bg-state-skipped";
    default:
      return "bg-state-queued";
  }
}

/** splitList splits text at spaces and commas and drops empty parts. */
export function splitList(text: string | undefined): string[] {
  if (!text) return [];
  return text
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}

/** parseLabelFilter returns the valid key=value parts of a label filter text. */
export function parseLabelFilter(text: string | undefined): string[] {
  return splitList(text).filter((s) => {
    const i = s.indexOf("=");
    return i > 0 && i < s.length - 1;
  });
}

/** parseLabelLines parses "key=value" lines into labels. It returns an error for a bad line. */
export function parseLabelLines(text: string): { labels: Record<string, string>; error?: string } {
  const labels: Record<string, string> = {};
  const lines = text.split("\n");
  for (let n = 0; n < lines.length; n++) {
    const line = (lines[n] ?? "").trim();
    if (!line) continue;
    const i = line.indexOf("=");
    if (i <= 0) return { labels, error: `Line ${n + 1} must have the form key=value.` };
    labels[line.slice(0, i).trim()] = line.slice(i + 1).trim();
  }
  return { labels };
}

/** parseArgs returns one argument per non-empty line. */
export function parseArgs(text: string): string[] {
  return text
    .split("\n")
    .map((s) => s.replace(/\r$/, ""))
    .filter((s) => s.trim() !== "");
}

/** localToIso converts a datetime-local value to an ISO time. It returns undefined for bad values. */
export function localToIso(value: string | undefined): string | undefined {
  if (!value) return undefined;
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString();
}

/** ExecutionsSearch is the URL state of the executions list. */
export type ExecutionsSearch = {
  state?: string;
  namespace?: string;
  flow?: string;
  trigger_type?: TriggerType;
  label?: string;
  from?: string;
  to?: string;
  sort?: "duration";
};

/** validateExecutionsSearch keeps only known and valid search params. */
export function validateExecutionsSearch(s: Record<string, unknown>): ExecutionsSearch {
  const out: ExecutionsSearch = {};
  const str = (v: unknown) => (typeof v === "string" && v.trim() !== "" ? v : undefined);
  const states = splitList(str(s.state)).filter((x) => (executionStates as readonly string[]).includes(x));
  if (states.length) out.state = states.join(",");
  if (str(s.namespace)) out.namespace = str(s.namespace);
  if (str(s.flow)) out.flow = str(s.flow);
  if (typeof s.trigger_type === "string" && (triggerTypes as readonly string[]).includes(s.trigger_type)) {
    out.trigger_type = s.trigger_type as TriggerType;
  }
  if (str(s.label)) out.label = str(s.label);
  if (str(s.from)) out.from = str(s.from);
  if (str(s.to)) out.to = str(s.to);
  if (s.sort === "duration") out.sort = "duration";
  return out;
}

/** listQuery turns the URL state into query params of GET /api/v1/executions. */
export function listQuery(s: ExecutionsSearch) {
  const labels = parseLabelFilter(s.label);
  return {
    state: s.state,
    namespace: s.namespace,
    flow: s.flow,
    trigger_type: s.trigger_type,
    label: labels.length ? labels : undefined,
    from: localToIso(s.from),
    to: localToIso(s.to),
    sort: s.sort ?? ("created" as const),
  };
}

/** FlowInput is one input of a flow definition. */
export type FlowInput = {
  id: string;
  type: "string" | "int" | "number" | "boolean" | "select" | "json";
  required?: boolean;
  default?: unknown;
  values?: unknown[];
  description?: string;
};

const inputTypes = new Set(["string", "int", "number", "boolean", "select", "json"]);

/** flowInputs reads the inputs of a revision definition. */
export function flowInputs(definition: Record<string, unknown> | null | undefined): FlowInput[] {
  const raw = definition?.inputs;
  if (!Array.isArray(raw)) return [];
  return raw.flatMap((x): FlowInput[] => {
    if (!x || typeof x !== "object") return [];
    const o = x as Record<string, unknown>;
    if (typeof o.id !== "string") return [];
    const type = typeof o.type === "string" && inputTypes.has(o.type) ? (o.type as FlowInput["type"]) : "string";
    return [
      {
        id: o.id,
        type,
        required: o.required === true,
        default: o.default,
        values: Array.isArray(o.values) ? o.values : undefined,
        description: typeof o.description === "string" ? o.description : undefined,
      },
    ];
  });
}

/** initialFormValue returns the form value of an input from its default. */
export function initialFormValue(input: FlowInput): string | boolean {
  const d = input.default;
  if (input.type === "boolean") return d === true;
  if (d === undefined || d === null) return "";
  if (input.type === "json") return JSON.stringify(d, null, 2);
  return String(d);
}

/** coerceInputs converts form values to typed inputs. Empty optional fields are left out. */
export function coerceInputs(
  inputs: FlowInput[],
  values: Record<string, string | boolean | undefined>,
): { inputs: Record<string, unknown>; errors: Record<string, string> } {
  const out: Record<string, unknown> = {};
  const errors: Record<string, string> = {};
  for (const input of inputs) {
    const v = values[input.id];
    if (input.type === "boolean") {
      out[input.id] = v === true;
      continue;
    }
    const text = typeof v === "string" ? v : "";
    if (text.trim() === "") {
      if (input.required && (input.default === undefined || input.default === null)) {
        errors[input.id] = "This input is required.";
      }
      continue;
    }
    switch (input.type) {
      case "int": {
        const n = Number(text);
        if (!Number.isInteger(n)) errors[input.id] = "Enter a whole number.";
        else out[input.id] = n;
        break;
      }
      case "number": {
        const n = Number(text);
        if (!Number.isFinite(n)) errors[input.id] = "Enter a number.";
        else out[input.id] = n;
        break;
      }
      case "json":
        try {
          out[input.id] = JSON.parse(text);
        } catch (e) {
          errors[input.id] = `Invalid JSON: ${e instanceof Error ? e.message : "parse error"}`;
        }
        break;
      case "select": {
        const match = (input.values ?? []).find((x) => String(x) === text);
        out[input.id] = match === undefined ? text : match;
        break;
      }
      default:
        out[input.id] = text;
    }
  }
  return { inputs: out, errors };
}

/** inputFieldErrors maps server field names such as "inputs.count" to input ids. */
export function inputFieldErrors(fields: Record<string, string>): { byInput: Record<string, string>; other: string[] } {
  const byInput: Record<string, string> = {};
  const other: string[] = [];
  for (const [k, v] of Object.entries(fields)) {
    if (k.startsWith("inputs.")) byInput[k.slice("inputs.".length)] = v;
    else other.push(k ? `${k}: ${v}` : v);
  }
  return { byInput, other };
}

/** GanttRun is the part of a task run that the Gantt uses. */
export type GanttRun = {
  id: string;
  task_key: string;
  attempt: number;
  state: string;
  queued_at?: string | null;
  started_at?: string | null;
  ended_at?: string | null;
  duration_ms?: number | null;
  reused_from_id?: string | null;
};

export type GanttRow = {
  id: string;
  label: string;
  state: string;
  left: number;
  width: number;
  durationMs: number | null;
  reused: boolean;
  started: boolean;
};

const ms = (iso: string | null | undefined) => {
  if (!iso) return undefined;
  const t = Date.parse(iso);
  return Number.isNaN(t) ? undefined : t;
};

/**
 * ganttLayout computes bar positions as percent of a shared axis.
 * The axis starts at the earliest queued or start time and ends at the latest end (or now while running).
 */
export function ganttLayout(runs: GanttRun[], now: number): { rows: GanttRow[]; start: number; end: number } {
  let start = Infinity;
  let end = -Infinity;
  for (const r of runs) {
    for (const t of [ms(r.queued_at), ms(r.started_at)]) if (t !== undefined) start = Math.min(start, t);
    const s = ms(r.started_at);
    const e = ms(r.ended_at) ?? (s !== undefined && !isTerminal(r.state) ? now : s);
    if (e !== undefined) end = Math.max(end, e);
  }
  if (!Number.isFinite(start)) start = now;
  if (!Number.isFinite(end) || end < start) end = start;
  const span = Math.max(end - start, 1);
  const rows = runs.map((r): GanttRow => {
    const s = ms(r.started_at);
    const e = ms(r.ended_at) ?? (s !== undefined && !isTerminal(r.state) ? now : s);
    const left = s === undefined ? 0 : ((s - start) / span) * 100;
    const width = s === undefined || e === undefined ? 0 : ((Math.max(e, s) - s) / span) * 100;
    const durationMs = r.duration_ms ?? (s !== undefined && e !== undefined ? Math.max(e - s, 0) : null);
    return {
      id: r.id,
      label: `${r.task_key} #${r.attempt}`,
      state: r.state,
      left: clamp(left),
      width: clamp(width),
      durationMs,
      reused: !!r.reused_from_id,
      started: s !== undefined,
    };
  });
  return { rows, start, end };
}

function clamp(n: number): number {
  return Math.min(100, Math.max(0, n));
}

/** LogLine is the part of a log entry that the log filter uses. */
export type LogLine = { task_key: string; text: string };

/** filterLogs keeps lines of one task (or all) that contain the search text, case-insensitive. */
export function filterLogs<T extends LogLine>(lines: T[], task: string, search: string): T[] {
  const q = search.trim().toLowerCase();
  if (!task && !q) return lines;
  return lines.filter((l) => (!task || l.task_key === task) && (!q || l.text.toLowerCase().includes(q)));
}

/** compactJson returns JSON on one line. */
export function compactJson(v: unknown): string {
  try {
    return JSON.stringify(v) ?? "null";
  } catch {
    return String(v);
  }
}

/** executionTitle returns the flow as namespace/flow_id, or "File run" for file runs. */
export function executionTitle(e: { flow_id?: string | null; trigger_payload?: Record<string, unknown> }): {
  title: string;
  path?: string;
} {
  if (e.flow_id) return { title: e.flow_id };
  const p = e.trigger_payload?.path;
  return { title: "File run", path: typeof p === "string" ? p : undefined };
}

/** runnableFile reports whether a namespace file can run directly. */
export function runnableFile(path: string): boolean {
  return /\.(py|sh|ts|js)$/i.test(path);
}
