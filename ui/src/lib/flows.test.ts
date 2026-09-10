import { describe, expect, it } from "vitest";
import { executionStateTone, stateLabel, triggerSummary } from "./flows";

describe("triggerSummary", () => {
  it("shows cron and timezone for schedules", () => {
    expect(triggerSummary("schedule", { cron: "0 * * * *", timezone: "Europe/Berlin" })).toBe(
      "0 * * * * (Europe/Berlin)",
    );
    expect(triggerSummary("schedule", { cron: "@daily" })).toBe("@daily (UTC)");
  });

  it("lists scalar config values for other types", () => {
    expect(triggerSummary("flow", { flow: "a.b/etl", states: ["failed", "success"], nested: { x: 1 } })).toBe(
      "flow: a.b/etl; states: failed, success",
    );
    expect(triggerSummary("webhook", {})).toBe("");
  });
});

describe("execution states", () => {
  it("maps states to tones and labels", () => {
    expect(executionStateTone("success")).toBe("success");
    expect(executionStateTone("failed")).toBe("failed");
    expect(executionStateTone("timed_out")).toBe("warning");
    expect(executionStateTone("running")).toBe("accent");
    expect(executionStateTone("queued")).toBe("neutral");
    expect(stateLabel("timed_out")).toBe("Timed out");
  });
});
