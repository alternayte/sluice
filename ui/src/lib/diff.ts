/** DiffLineKind is the kind of one line of a unified diff. */
export type DiffLineKind = "add" | "remove" | "hunk" | "meta" | "context";

/** classifyDiffLine returns the kind of one line of a unified diff. */
export function classifyDiffLine(line: string): DiffLineKind {
  if (line.startsWith("+++ ") || line.startsWith("--- ") || line === "+++" || line === "---") return "meta";
  if (line.startsWith("diff ") || line.startsWith("index ") || line.startsWith("\\ ")) return "meta";
  if (line.startsWith("@@")) return "hunk";
  if (line.startsWith("+")) return "add";
  if (line.startsWith("-")) return "remove";
  return "context";
}

/** diffLines splits a unified diff into classified lines. A final newline does not add an empty line. */
export function diffLines(text: string): { kind: DiffLineKind; text: string }[] {
  if (text === "") return [];
  const lines = text.split("\n");
  if (lines[lines.length - 1] === "") lines.pop();
  return lines.map((l) => ({ kind: classifyDiffLine(l), text: l }));
}
