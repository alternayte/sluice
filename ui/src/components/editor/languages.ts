/** LanguageId names a syntax highlighting mode of the code editor. */
export type LanguageId = "yaml" | "python" | "shell" | "javascript" | "typescript" | "jsx" | "tsx" | "sql" | "plain";

const byExtension: Record<string, LanguageId> = {
  yaml: "yaml",
  yml: "yaml",
  py: "python",
  sh: "shell",
  bash: "shell",
  zsh: "shell",
  js: "javascript",
  mjs: "javascript",
  cjs: "javascript",
  jsx: "jsx",
  ts: "typescript",
  mts: "typescript",
  cts: "typescript",
  tsx: "tsx",
  sql: "sql",
};

function baseName(path: string): string {
  const i = path.lastIndexOf("/");
  return (i >= 0 ? path.slice(i + 1) : path).toLowerCase();
}

/** languageForPath returns the highlighting mode for a file path, chosen by the file extension. */
export function languageForPath(path: string): LanguageId {
  const name = baseName(path);
  const dot = name.lastIndexOf(".");
  if (dot <= 0) return "plain";
  return byExtension[name.slice(dot + 1)] ?? "plain";
}

/** isValidatedPath reports whether the server validates the file: flow files and namespace.yaml. */
export function isValidatedPath(path: string): boolean {
  const name = baseName(path);
  return name.endsWith(".flow.yaml") || name.endsWith(".flow.yml") || name === "namespace.yaml";
}
