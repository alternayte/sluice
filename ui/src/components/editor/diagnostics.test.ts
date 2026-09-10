import { Text } from "@codemirror/state";
import { describe, expect, it } from "vitest";
import { issuesToDiagnostics } from "./diagnostics";

describe("issuesToDiagnostics", () => {
  const doc = Text.of(["id: a", "tasks:", "  - bad"]);

  it("puts each issue at its line and column", () => {
    const out = issuesToDiagnostics(doc, [{ line: 3, column: 5, code: "unknown_task", message: "bad task" }]);
    expect(out).toEqual([
      { from: doc.line(3).from + 4, to: doc.line(3).to, severity: "error", message: "unknown_task: bad task" },
    ]);
  });

  it("clamps lines and columns into the document", () => {
    const out = issuesToDiagnostics(doc, [
      { line: 0, column: 0, code: "c", message: "m" },
      { line: 99, column: 99, code: "c", message: "m" },
    ]);
    expect(out.map((d) => [d.from, d.to])).toEqual([
      [0, doc.line(1).to],
      [doc.length, doc.length],
    ]);
  });
});
