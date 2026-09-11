import { describe, expect, it } from "vitest";
import { needsDots, pointCount } from "./charts";

describe("needsDots", () => {
  it("shows dots when a series has one point", () => {
    const rows = [{ label: "10:00", p50: 1200, p95: 3400 }];
    expect(pointCount(rows, "p50")).toBe(1);
    expect(needsDots(rows, "p50")).toBe(true);
  });

  it("counts only numeric values", () => {
    const rows = [
      { label: "a", v: null },
      { label: "b", v: 4 },
      { label: "c", v: "x" },
      { label: "d", v: Number.NaN },
    ];
    expect(pointCount(rows, "v")).toBe(1);
    expect(needsDots(rows, "v")).toBe(true);
    expect(needsDots([], "v")).toBe(true);
  });

  it("draws no dots when a series has two points or more", () => {
    const rows = [
      { label: "a", v: 1 },
      { label: "b", v: null },
      { label: "c", v: 2 },
    ];
    expect(needsDots(rows, "v")).toBe(false);
  });
});
