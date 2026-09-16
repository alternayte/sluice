import { describe, expect, it } from "vitest";
import {
  canCancel,
  canRestart,
  coerceInputs,
  executionTitle,
  filterLogs,
  flowInputs,
  ganttLayout,
  initialFormValue,
  inputFieldErrors,
  listQuery,
  parseArgs,
  parseLabelFilter,
  parseLabelLines,
  runnableFile,
  stateBarClass,
  validateExecutionsSearch,
  type FlowInput,
} from "./executions";

describe("label parsing", () => {
  it("splits filters at spaces and commas and drops bad parts", () => {
    expect(parseLabelFilter("team=data, env=prod  bad =x")).toEqual(["team=data", "env=prod"]);
    expect(parseLabelFilter(undefined)).toEqual([]);
  });

  it("parses key=value lines", () => {
    expect(parseLabelLines("a=1\n\n b = two \n")).toEqual({ labels: { a: "1", b: "two" } });
    expect(parseLabelLines("a=1\nnope").error).toBe("Line 2 must have the form key=value.");
  });

  it("parses one argument per line", () => {
    expect(parseArgs("--name\nAda Lovelace\n\n")).toEqual(["--name", "Ada Lovelace"]);
  });
});

describe("executions search", () => {
  it("keeps valid params only", () => {
    expect(
      validateExecutionsSearch({ state: "FAILED,nope,RUNNING", trigger_type: "cron", sort: "duration", flow: "a/b" }),
    ).toEqual({ state: "FAILED,RUNNING", sort: "duration", flow: "a/b" });
  });

  it("builds repeated label params and ISO times", () => {
    const q = listQuery({ label: "a=1 b=2", from: "2026-01-02T03:04" });
    expect(q.label).toEqual(["a=1", "b=2"]);
    expect(q.from).toBe(new Date("2026-01-02T03:04").toISOString());
    expect(q.sort).toBe("created");
  });
});

describe("run form coercion", () => {
  const inputs: FlowInput[] = [
    { id: "name", type: "string", required: true },
    { id: "count", type: "int" },
    { id: "ratio", type: "number" },
    { id: "dry", type: "boolean" },
    { id: "size", type: "select", values: [1, 2, 3] },
    { id: "cfg", type: "json" },
    { id: "env", type: "string", required: true, default: "dev" },
  ];

  it("types values", () => {
    const r = coerceInputs(inputs, {
      name: "x",
      count: "3",
      ratio: "0.5",
      dry: true,
      size: "2",
      cfg: '{"a":[1]}',
      env: "",
    });
    expect(r.errors).toEqual({});
    expect(r.inputs).toEqual({ name: "x", count: 3, ratio: 0.5, dry: true, size: 2, cfg: { a: [1] } });
  });

  it("reports required, integer and JSON errors", () => {
    const r = coerceInputs(inputs, { name: " ", count: "1.5", cfg: "{" });
    expect(r.errors.name).toBe("This input is required.");
    expect(r.errors.count).toBe("Enter a whole number.");
    expect(r.errors.cfg).toMatch(/^Invalid JSON/);
    expect(r.errors.env).toBeUndefined();
  });

  it("reads inputs and defaults from a definition", () => {
    const list = flowInputs({ inputs: [{ id: "n", type: "int", default: 4 }, { type: "string" }, "x"] });
    expect(list).toHaveLength(1);
    expect(initialFormValue(list[0]!)).toBe("4");
    expect(initialFormValue({ id: "j", type: "json", default: { a: 1 } })).toBe('{\n  "a": 1\n}');
    expect(initialFormValue({ id: "b", type: "boolean" })).toBe(false);
  });

  it("maps server field errors", () => {
    expect(inputFieldErrors({ "inputs.count": "bad", labels: "too many" })).toEqual({
      byInput: { count: "bad" },
      other: ["labels: too many"],
    });
  });
});

describe("gantt geometry", () => {
  const t = (s: number) => new Date(Date.UTC(2026, 0, 1, 0, 0, s)).toISOString();

  it("places bars on a shared axis", () => {
    const { rows, start, end } = ganttLayout(
      [
        { id: "1", task_key: "a", attempt: 1, state: "SUCCESS", queued_at: t(0), started_at: t(10), ended_at: t(30) },
        {
          id: "2",
          task_key: "b",
          attempt: 2,
          state: "FAILED",
          started_at: t(30),
          ended_at: t(40),
          reused_from_id: "x",
        },
      ],
      0,
    );
    expect(end - start).toBe(40_000);
    expect(rows[0]).toMatchObject({ label: "a #1", left: 25, width: 50, durationMs: 20_000, reused: false });
    expect(rows[1]).toMatchObject({ label: "b #2", left: 75, width: 25, reused: true });
  });

  it("extends running bars to now and keeps queued runs at zero width", () => {
    const now = Date.parse(t(20));
    const { rows } = ganttLayout(
      [
        { id: "1", task_key: "a", attempt: 1, state: "RUNNING", started_at: t(0) },
        { id: "2", task_key: "b", attempt: 1, state: "QUEUED", queued_at: t(5) },
      ],
      now,
    );
    expect(rows[0]).toMatchObject({ left: 0, width: 100, durationMs: 20_000, started: true });
    expect(rows[1]).toMatchObject({ width: 0, started: false, durationMs: null });
  });

  it("handles an empty list", () => {
    expect(ganttLayout([], 5).rows).toEqual([]);
  });
});

describe("log filter", () => {
  const lines = [
    { task_key: "a", text: "Hello World" },
    { task_key: "b", text: "hello there" },
    { task_key: "a", text: "bye" },
  ];

  it("filters by task and case-insensitive text", () => {
    expect(filterLogs(lines, "", "HELLO")).toHaveLength(2);
    expect(filterLogs(lines, "a", "hello")).toEqual([lines[0]]);
    expect(filterLogs(lines, "", "")).toBe(lines);
  });
});

describe("state helpers", () => {
  it("gates actions by state", () => {
    expect(canCancel("RUNNING")).toBe(true);
    expect(canCancel("SUCCESS")).toBe(false);
    expect(canRestart("TIMED_OUT")).toBe(true);
    expect(canRestart("RUNNING")).toBe(false);
    expect(stateBarClass("FAILED")).toBe("bg-state-failed");
  });

  it("names file runs and runnable files", () => {
    expect(executionTitle({ flow_id: null, trigger_payload: { path: "hello.py" } })).toEqual({
      title: "File run",
      path: "hello.py",
    });
    expect(executionTitle({ flow_id: "etl" }).title).toBe("etl");
    expect(runnableFile("a/b.py")).toBe(true);
    expect(runnableFile("flow.yaml")).toBe(false);
  });
});
