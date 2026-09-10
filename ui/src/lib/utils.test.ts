import { describe, expect, it } from "vitest";
import { cn, formatBytes, formatDuration } from "./utils";

describe("formatBytes", () => {
  it("formats bytes with binary units", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(1023)).toBe("1023 B");
    expect(formatBytes(1536)).toBe("1.5 KiB");
    expect(formatBytes(20 * 1024 * 1024)).toBe("20 MiB");
  });
});

describe("formatDuration", () => {
  it("formats milliseconds, seconds, minutes and hours", () => {
    expect(formatDuration(null)).toBe("—");
    expect(formatDuration(250)).toBe("250 ms");
    expect(formatDuration(1500)).toBe("1.5 s");
    expect(formatDuration(125_000)).toBe("2m 5s");
    expect(formatDuration(3_720_000)).toBe("1h 2m");
  });
});

describe("cn", () => {
  it("merges conflicting Tailwind classes", () => {
    expect(cn("p-2", false, "p-4")).toBe("p-4");
  });
});
