import {
  ChevronRight,
  File,
  FileCode,
  FileCog,
  FileJson,
  FileText,
  Folder,
  FolderOpen,
  Workflow,
  type LucideIcon,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { Badge } from "@/components/ui/badge";
import { ancestors, buildTree, visibleNodes, type FileEntry, type TreeNode } from "@/lib/file-tree";
import { cn, formatBytes } from "@/lib/utils";

const codeExt = new Set(["py", "ts", "tsx", "js", "mjs", "sh", "bash", "sql", "go", "rb"]);
const textExt = new Set(["md", "txt", "csv", "rst"]);
const configExt = new Set(["yaml", "yml", "toml", "ini", "cfg", "env"]);

function fileIcon(name: string): LucideIcon {
  if (name.endsWith(".flow.yaml") || name.endsWith(".flow.yml")) return Workflow;
  const ext = name.includes(".") ? name.slice(name.lastIndexOf(".") + 1).toLowerCase() : "";
  if (codeExt.has(ext)) return FileCode;
  if (ext === "json") return FileJson;
  if (configExt.has(ext)) return FileCog;
  if (textExt.has(ext)) return FileText;
  return File;
}

function readExpanded(key: string): Set<string> {
  try {
    const v: unknown = JSON.parse(localStorage.getItem(key) ?? "[]");
    return new Set(Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : []);
  } catch {
    return new Set();
  }
}

/**
 * FileTree shows the files of a namespace as folders and files. It follows the ARIA tree
 * pattern: one item is in the tab order, and the arrow keys move and open folders. Each item
 * has its full path as the accessible name. The open folders are kept per namespace.
 */
export function FileTree({
  namespace,
  files,
  selected,
  onSelect,
  onFolder,
}: {
  namespace: string;
  files: FileEntry[];
  selected?: string;
  onSelect: (path: string) => void;
  /** onFolder reports the folder that the user last clicked or opened a file in. */
  onFolder: (path: string) => void;
}) {
  const storageKey = `sluice-file-tree:${namespace}`;
  const [expanded, setExpandedState] = useState<Set<string>>(() => readExpanded(storageKey));
  const [focused, setFocused] = useState<string | undefined>(selected);
  const tree = useMemo(() => buildTree(files), [files]);
  const refs = useRef(new Map<string, HTMLDivElement>());

  const setExpanded = (next: Set<string>) => {
    setExpandedState(next);
    try {
      localStorage.setItem(storageKey, JSON.stringify([...next]));
    } catch {
      // Storage is not available. The state lasts for this page load.
    }
  };

  // Open the folders above the selected file, for example when a link opens a deep file.
  useEffect(() => {
    if (!selected) return;
    setExpandedState((cur) => {
      const missing = ancestors(selected).filter((a) => !cur.has(a));
      return missing.length === 0 ? cur : new Set([...cur, ...missing]);
    });
    setFocused(selected);
  }, [selected]);

  const visible = visibleNodes(tree, expanded);
  const tabStop = visible.some((v) => v.node.path === focused) ? focused : visible[0]?.node.path;

  const focusPath = (path: string) => {
    setFocused(path);
    refs.current.get(path)?.focus();
  };

  const toggle = (path: string, open?: boolean) => {
    const next = new Set(expanded);
    const willOpen = open ?? !next.has(path);
    if (willOpen) next.add(path);
    else next.delete(path);
    setExpanded(next);
  };

  const activate = (node: TreeNode) => {
    if (node.kind === "dir") {
      toggle(node.path);
      onFolder(node.path);
    } else {
      onSelect(node.path);
      onFolder(node.path.includes("/") ? node.path.slice(0, node.path.lastIndexOf("/")) : "");
    }
    setFocused(node.path);
  };

  const onKeyDown = (e: KeyboardEvent) => {
    const i = visible.findIndex((v) => v.node.path === tabStop);
    const node = visible[i]?.node;
    if (!node) return;
    const parent = ancestors(node.path).at(-1);
    const focusAt = (j: number) => {
      const target = visible[j];
      if (target) focusPath(target.node.path);
    };
    switch (e.key) {
      case "ArrowDown":
        focusAt(i + 1);
        break;
      case "ArrowUp":
        focusAt(i - 1);
        break;
      case "Home":
        focusAt(0);
        break;
      case "End":
        focusAt(visible.length - 1);
        break;
      case "ArrowRight":
        if (node.kind !== "dir") return;
        if (!expanded.has(node.path)) toggle(node.path, true);
        else if (node.children[0]) focusPath(node.children[0].path);
        break;
      case "ArrowLeft":
        if (node.kind === "dir" && expanded.has(node.path)) toggle(node.path, false);
        else if (parent) focusPath(parent);
        break;
      case "Enter":
      case " ":
        activate(node);
        break;
      default:
        return;
    }
    e.preventDefault();
  };

  return (
    <div role="tree" aria-label="Files" onKeyDown={onKeyDown} className="flex flex-col py-1">
      {visible.map(({ node, depth }) => {
        const open = node.kind === "dir" && expanded.has(node.path);
        const isSelected = node.kind === "file" && node.path === selected;
        const Icon = node.kind === "dir" ? (open ? FolderOpen : Folder) : fileIcon(node.name);
        const dirty = node.kind === "dir" ? node.dirty && !open : node.file.dirty && !node.file.isNew;
        return (
          <div
            key={node.path}
            ref={(el) => {
              if (el) refs.current.set(node.path, el);
              else refs.current.delete(node.path);
            }}
            role="treeitem"
            aria-label={node.path}
            aria-level={depth + 1}
            aria-expanded={node.kind === "dir" ? open : undefined}
            aria-selected={node.kind === "file" ? isSelected : undefined}
            tabIndex={node.path === tabStop ? 0 : -1}
            title={node.path}
            onClick={() => activate(node)}
            onFocus={() => setFocused(node.path)}
            style={{ paddingLeft: `${depth * 14 + 6}px` }}
            className={cn(
              "group flex h-7 cursor-pointer items-center gap-1.5 pr-2 text-sm outline-none select-none hover:bg-muted focus-visible:bg-muted focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset",
              isSelected && "bg-accent-soft text-accent-text hover:bg-accent-soft",
            )}
          >
            <ChevronRight
              aria-hidden
              className={cn(
                "h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform",
                node.kind !== "dir" && "invisible",
                open && "rotate-90",
              )}
            />
            <Icon
              aria-hidden
              className={cn("h-4 w-4 shrink-0", isSelected ? "text-accent" : "text-muted-foreground")}
            />
            <span className={cn("min-w-0 flex-1 truncate", node.kind === "file" && "font-mono text-xs")}>
              {node.name}
            </span>
            {node.kind === "file" && node.file.isNew && <Badge tone="accent">New</Badge>}
            {dirty && (
              <span className="text-xs text-muted-foreground" title="Unsaved changes">
                ●<span className="sr-only">Unsaved changes</span>
              </span>
            )}
            {node.kind === "file" && node.file.size !== undefined && (
              <span className="shrink-0 text-xs text-muted-foreground tabular-nums">{formatBytes(node.file.size)}</span>
            )}
          </div>
        );
      })}
    </div>
  );
}
