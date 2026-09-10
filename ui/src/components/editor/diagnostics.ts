import type { Diagnostic } from "@codemirror/lint";
import type { Text } from "@codemirror/state";

/** Issue is one validation error from the server. Line and column start at 1. */
export type Issue = { line: number; column: number; message: string; code: string };

/** issuesToDiagnostics maps server issues to error diagnostics at their lines. */
export function issuesToDiagnostics(doc: Text, issues: Issue[]): Diagnostic[] {
  return issues.map((issue) => {
    const lineNo = Math.min(Math.max(1, issue.line || 1), doc.lines);
    const line = doc.line(lineNo);
    const from = Math.min(line.from + Math.max(0, (issue.column || 1) - 1), line.to);
    return {
      from,
      to: line.to > from ? line.to : from,
      severity: "error",
      message: `${issue.code}: ${issue.message}`,
    };
  });
}
