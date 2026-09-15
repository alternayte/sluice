/** FileEntry is one file of a namespace as the tree shows it. */
export type FileEntry = {
  path: string;
  size?: number;
  isNew?: boolean;
  dirty?: boolean;
};

export type TreeNode =
  | {
      kind: "dir";
      name: string;
      path: string;
      children: TreeNode[];
      dirty: boolean;
    }
  | { kind: "file"; name: string; path: string; file: FileEntry };

/**
 * buildTree turns flat paths into folders and files. Each level lists folders first,
 * then files, both sorted by name. A folder is dirty when a file below it is new or
 * has unsaved changes.
 */
export function buildTree(files: FileEntry[]): TreeNode[] {
  type Dir = { dirs: Map<string, Dir>; files: FileEntry[] };
  const root: Dir = { dirs: new Map(), files: [] };
  for (const f of files) {
    const parts = f.path.split("/");
    let d = root;
    for (const part of parts.slice(0, -1)) {
      let next = d.dirs.get(part);
      if (!next) {
        next = { dirs: new Map(), files: [] };
        d.dirs.set(part, next);
      }
      d = next;
    }
    d.files.push(f);
  }
  const byName = (a: string, b: string) => a.localeCompare(b, undefined, { sensitivity: "base", numeric: true });
  const convert = (d: Dir, prefix: string): TreeNode[] => {
    const dirs: TreeNode[] = [...d.dirs.entries()]
      .sort(([a], [b]) => byName(a, b))
      .map(([name, child]) => {
        const path = prefix + name;
        const children = convert(child, path + "/");
        const dirty = children.some((c) => (c.kind === "dir" ? c.dirty : c.file.isNew || c.file.dirty));
        return { kind: "dir", name, path, children, dirty };
      });
    const leaves: TreeNode[] = [...d.files]
      .sort((a, b) => byName(baseName(a.path), baseName(b.path)))
      .map((f) => ({
        kind: "file",
        name: baseName(f.path),
        path: f.path,
        file: f,
      }));
    return [...dirs, ...leaves];
  };
  return convert(root, "");
}

export function baseName(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1);
}

/** ancestors returns the folder paths above a file path, outermost first. */
export function ancestors(path: string): string[] {
  const parts = path.split("/").slice(0, -1);
  return parts.map((_, i) => parts.slice(0, i + 1).join("/"));
}

/** visibleNodes lists the nodes that show with the expanded folders, in display order, with their depth. */
export function visibleNodes(nodes: TreeNode[], expanded: Set<string>, depth = 0): { node: TreeNode; depth: number }[] {
  const out: { node: TreeNode; depth: number }[] = [];
  for (const node of nodes) {
    out.push({ node, depth });
    if (node.kind === "dir" && expanded.has(node.path)) out.push(...visibleNodes(node.children, expanded, depth + 1));
  }
  return out;
}
