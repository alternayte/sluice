import { describe, expect, it } from "vitest";
import { ancestors, buildTree, visibleNodes } from "./file-tree";

describe("buildTree", () => {
  const tree = buildTree([
    { path: "README.md" },
    { path: "scripts/load/b.py" },
    { path: "flows/etl.flow.yaml" },
    { path: "scripts/a.py", dirty: true },
    { path: "a.txt", isNew: true },
    { path: "scripts/load/helpers/x.py" },
  ]);

  it("lists folders first, then files, sorted by name", () => {
    expect(visibleNodes(tree, new Set(["scripts", "scripts/load"])).map((n) => `${n.depth}:${n.node.path}`)).toEqual([
      "0:flows",
      "0:scripts",
      "1:scripts/load",
      "2:scripts/load/helpers",
      "2:scripts/load/b.py",
      "1:scripts/a.py",
      "0:a.txt",
      "0:README.md",
    ]);
  });

  it("marks a folder dirty when a file below it has unsaved changes", () => {
    const dirty = Object.fromEntries(
      tree.filter((n) => n.kind === "dir").map((n) => [n.path, n.kind === "dir" && n.dirty]),
    );
    expect(dirty).toEqual({ flows: false, scripts: true });
  });

  it("returns the ancestors of a path", () => {
    expect(ancestors("a/b/c.txt")).toEqual(["a", "a/b"]);
    expect(ancestors("c.txt")).toEqual([]);
  });
});
