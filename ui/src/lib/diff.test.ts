import { describe, expect, it } from "vitest";
import { classifyDiffLine, diffLines } from "./diff";

describe("classifyDiffLine", () => {
  it("classifies unified diff lines", () => {
    expect(classifyDiffLine("--- a/x.yaml")).toBe("meta");
    expect(classifyDiffLine("+++ b/x.yaml")).toBe("meta");
    expect(classifyDiffLine("diff --git a/x b/x")).toBe("meta");
    expect(classifyDiffLine("\\ No newline at end of file")).toBe("meta");
    expect(classifyDiffLine("@@ -1,3 +1,4 @@")).toBe("hunk");
    expect(classifyDiffLine("+new line")).toBe("add");
    expect(classifyDiffLine("-old line")).toBe("remove");
    expect(classifyDiffLine("--flag removed")).toBe("remove");
    expect(classifyDiffLine(" same")).toBe("context");
    expect(classifyDiffLine("")).toBe("context");
  });
});

describe("diffLines", () => {
  it("splits text and drops the final empty line", () => {
    expect(diffLines("")).toEqual([]);
    expect(diffLines("@@ -1 +1 @@\n-a\n+b\n")).toEqual([
      { kind: "hunk", text: "@@ -1 +1 @@" },
      { kind: "remove", text: "-a" },
      { kind: "add", text: "+b" },
    ]);
  });
});
