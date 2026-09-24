import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { FileCode, FilePlus, FolderOpen, Play, Save, Upload } from "lucide-react";
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type FormEvent,
  type KeyboardEvent,
  type PointerEvent,
} from "react";
import { listFilesOptions } from "@/api/@tanstack/react-query.gen";
import type { Snapshot } from "@/api/types.gen";
import { ApiError, rawFetch } from "@/api-client";
import { DataState, EmptyState } from "@/components/data-state";
import { RunFileDialog } from "@/components/run-dialogs";
import { FileEditorPanel, fileContentKey, savedToast, useSaveChanges } from "@/features/namespaces/FileEditorPanel";
import { FileTree } from "@/features/namespaces/FileTree";
import { invalidateNamespace } from "@/features/namespaces/namespace-source";
import { Button, buttonClass } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, FormError } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { toast } from "@/components/ui/toast";
import { useCurrentUser } from "@/lib/auth";
import { useCommands, type Command } from "@/lib/commands";
import { errorMessage } from "@/lib/errors";
import { runnableFile } from "@/lib/executions";
import { can } from "@/lib/roles";
import { cn } from "@/lib/utils";

/**
 * NamespaceTree is the file list and the editor of a namespace. New files and edits stay
 * staged in the browser until a save, so that several files go into one version with one
 * message (REQ-NS-002, REQ-UI-007).
 */
export function NamespaceTree({
  namespace,
  canEdit,
  canPush = false,
  selected,
  onSelect,
  openNew = false,
  onNewOpened,
}: {
  namespace: string;
  canEdit: boolean;
  /** canPush lets an editor of a git namespace push edits to a new branch. */
  canPush?: boolean;
  selected?: string;
  onSelect: (path: string | undefined) => void;
  /** openNew opens the new file dialog once, for example from the command palette. */
  openNew?: boolean;
  /** onNewOpened clears the request of openNew. */
  onNewOpened?: () => void;
}) {
  const qc = useQueryClient();
  const me = useCurrentUser();
  const [newOpen, setNewOpen] = useState(false);
  const [saveOpen, setSaveOpen] = useState(false);
  const [runOpen, setRunOpen] = useState(false);
  /** staged maps a path to its unsaved content. created lists the staged paths that no version has. */
  const [staged, setStaged] = useState<Record<string, string>>({});
  const [created, setCreated] = useState<string[]>([]);
  const [folder, setFolder] = useState(() =>
    selected?.includes("/") ? selected.slice(0, selected.lastIndexOf("/")) : "",
  );
  const split = useSplit();
  const files = useQuery(listFilesOptions({ path: { namespace } }));
  const upload = useMutation({
    mutationFn: async (file: File) => {
      const res = await rawFetch(
        `/api/v1/namespaces/${encodeURIComponent(namespace)}/file?${new URLSearchParams({
          path: file.name,
          message: `Upload ${file.name}`,
        }).toString()}`,
        { method: "PUT", body: file, headers: { "Content-Type": "application/octet-stream" } },
      );
      await res.text();
    },
    onSuccess: (_, file) => {
      invalidateNamespace(qc, namespace);
      toast({ title: `Uploaded ${file.name}` });
      onSelect(file.name);
    },
  });

  useEffect(() => {
    if (!openNew) return;
    if (canEdit) setNewOpen(true);
    onNewOpened?.();
  }, [openNew, canEdit, onNewOpened]);

  const clear = (paths: string[]) => {
    setStaged((s) => Object.fromEntries(Object.entries(s).filter(([p]) => !paths.includes(p))));
    setCreated((c) => c.filter((p) => !paths.includes(p)));
  };
  const stagedPaths = Object.keys(staged).sort();
  const hasStaged = stagedPaths.length > 0;
  const savedFile = selected !== undefined && (files.data?.items.some((f) => f.path === selected) ?? false);
  const canRun = savedFile && can(me.role, "operator") && runnableFile(selected);

  const commands = useMemo(() => {
    const out: Command[] = [];
    if (canEdit) {
      out.push({
        id: "ns-new-file",
        group: "This namespace",
        label: "New file",
        icon: FilePlus,
        hint: namespace,
        keywords: "create add file",
        run: () => setNewOpen(true),
      });
    }
    if (canRun && selected) {
      out.push({
        id: "ns-run-file",
        group: "This namespace",
        label: `Run ${selected}`,
        icon: Play,
        hint: `${isMacPlatform ? "⌘" : "Ctrl+"}Enter`,
        keywords: "run execute start",
        run: () => setRunOpen(true),
      });
    }
    if (canEdit && hasStaged) {
      out.push({
        id: "ns-save-changes",
        group: "This namespace",
        label: "Save changes",
        icon: Save,
        hint: namespace,
        keywords: "save commit version",
        run: () => setSaveOpen(true),
      });
    }
    return out;
  }, [canEdit, canRun, selected, hasStaged, namespace]);
  useCommands(commands);

  return (
    <DataState query={files} skeleton="panel">
      {(list) => {
        const existing = new Set(list.items.map((f) => f.path));
        const newPaths = created.filter((p) => !existing.has(p));
        const isNew = (p: string) => newPaths.includes(p);
        return (
          <div
            ref={split.container}
            style={split.style}
            className="flex animate-enter flex-col overflow-hidden rounded-panel border bg-panel shadow-panel lg:flex-row"
          >
            <section
              aria-label="File browser"
              className="flex max-h-80 min-h-0 min-w-0 shrink-0 flex-col border-b bg-muted/40 lg:max-h-none lg:w-[var(--tree-width)] lg:border-b-0"
            >
              <div className="flex h-10 shrink-0 items-center justify-between gap-2 border-b pr-1.5 pl-3">
                <span className="text-xs font-semibold text-muted-foreground">
                  {list.items.length === 1 ? "1 file" : `${list.items.length} files`}
                </span>
                {canEdit && (
                  <div className="flex items-center gap-0.5">
                    <Button
                      size="icon"
                      variant="ghost"
                      className="h-7 w-7"
                      aria-label="New file"
                      title="New file"
                      onClick={() => setNewOpen(true)}
                    >
                      <FilePlus className="h-4 w-4" aria-hidden />
                    </Button>
                    <label
                      title="Upload a file"
                      className={cn(
                        buttonClass("ghost", "icon"),
                        "h-7 w-7 cursor-default focus-within:outline-3 focus-within:outline-ring",
                        upload.isPending && "pointer-events-none opacity-50",
                      )}
                    >
                      <Upload className={cn("h-4 w-4", upload.isPending && "animate-breathe")} aria-hidden />
                      <span className="sr-only">{upload.isPending ? "Uploading" : "Upload"}</span>
                      <input
                        type="file"
                        className="sr-only"
                        disabled={upload.isPending}
                        onChange={(e) => {
                          const f = e.target.files?.[0];
                          e.target.value = "";
                          if (f) upload.mutate(f);
                        }}
                      />
                    </label>
                  </div>
                )}
              </div>
              {upload.isError && (
                <div className="border-b px-3 py-2">
                  <FormError>{errorMessage(upload.error)}</FormError>
                </div>
              )}
              <div className="min-h-0 flex-1 overflow-auto">
                {list.items.length === 0 && newPaths.length === 0 ? (
                  <EmptyState
                    icon={FolderOpen}
                    text="This namespace has no files."
                    className="m-3 border-0 py-8"
                    action={
                      canEdit && (
                        <Button size="sm" variant="secondary" onClick={() => setNewOpen(true)}>
                          Create a file
                        </Button>
                      )
                    }
                  />
                ) : (
                  <FileTree
                    namespace={namespace}
                    files={[
                      ...list.items.map((f) => ({ path: f.path, size: f.size, dirty: staged[f.path] !== undefined })),
                      ...newPaths.map((p) => ({ path: p, isNew: true })),
                    ]}
                    selected={selected}
                    onSelect={onSelect}
                    onFolder={setFolder}
                  />
                )}
              </div>
              {canEdit && hasStaged && (
                <div className="flex shrink-0 animate-enter flex-wrap items-center justify-between gap-2 border-t py-2 pr-2 pl-3">
                  <span role="status" className="flex items-center gap-1.5 text-xs font-medium">
                    <span aria-hidden className="h-1.5 w-1.5 rounded-full bg-accent" />
                    {stagedPaths.length === 1 ? "1 unsaved file" : `${stagedPaths.length} unsaved files`}
                  </span>
                  <Button size="sm" onClick={() => setSaveOpen(true)}>
                    <Save className="h-3.5 w-3.5" aria-hidden />
                    Save changes
                  </Button>
                </div>
              )}
            </section>
            <div
              role="separator"
              aria-orientation="vertical"
              aria-label="Resize file browser"
              aria-valuemin={split.min}
              aria-valuemax={split.max}
              aria-valuenow={split.width}
              tabIndex={0}
              onPointerDown={split.onPointerDown}
              onPointerMove={split.onPointerMove}
              onPointerUp={split.onPointerUp}
              onKeyDown={split.onKeyDown}
              onDoubleClick={split.reset}
              title="Drag to resize. Double-click to reset."
              className="group relative z-10 -mx-1.5 hidden w-3 shrink-0 cursor-col-resize touch-none justify-center outline-none lg:flex"
            >
              <div className="h-full w-px bg-border transition-[width,background-color] duration-150 group-hover:w-0.5 group-hover:bg-accent group-focus-visible:w-0.5 group-focus-visible:bg-accent" />
            </div>
            <section
              aria-label="Editor"
              className="flex h-[75vh] min-h-96 min-w-0 flex-1 flex-col lg:h-auto lg:min-h-0"
            >
              {selected && (existing.has(selected) || isNew(selected)) ? (
                <FileEditorPanel
                  key={selected}
                  namespace={namespace}
                  path={selected}
                  baseVersion={list.version ?? undefined}
                  canEdit={canEdit}
                  gitPush={canPush}
                  onSelect={onSelect}
                  draft={staged[selected]}
                  isNew={isNew(selected)}
                  onDraft={(content) => {
                    if (content === null) clear([selected]);
                    else setStaged((s) => ({ ...s, [selected]: content }));
                  }}
                  onDiscard={() => {
                    clear([selected]);
                    onSelect(undefined);
                  }}
                />
              ) : (
                <EmptyState icon={FileCode} text="Select a file to open it." className="flex-1 rounded-none border-0" />
              )}
            </section>
            {newOpen && (
              <NewFileDialog
                folder={folder}
                exists={(p) => existing.has(p) || newPaths.includes(p)}
                onClose={() => setNewOpen(false)}
                onCreate={(p) => {
                  setCreated((c) => [...c, p]);
                  setStaged((s) => ({ ...s, [p]: "" }));
                  setNewOpen(false);
                  onSelect(p);
                }}
              />
            )}
            {runOpen && selected && (
              <RunFileDialog namespace={namespace} path={selected} onClose={() => setRunOpen(false)} />
            )}
            {saveOpen && (
              <SaveChangesDialog
                namespace={namespace}
                baseVersion={list.version ?? undefined}
                staged={staged}
                onClose={() => setSaveOpen(false)}
                onSaved={(paths, snapshot) => {
                  savedToast(paths.length === 1 ? `Saved ${paths[0]}` : `Saved ${paths.length} files`, snapshot);
                  for (const p of paths) qc.setQueryData(fileContentKey(namespace, p), staged[p]);
                  clear(paths);
                  setSaveOpen(false);
                }}
              />
            )}
          </div>
        );
      }}
    </DataState>
  );
}

const isMacPlatform = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform);

const splitMin = 224;
const splitMax = 576;
const splitDefault = 288;
/** editorMin is the smallest editor width that the splitter leaves. */
const editorMin = 420;
const splitStorageKey = "sluice-file-tree-width";
/** pageBottom is the bottom padding of the main element at lg (md:py-7). */
const pageBottom = 28;

/**
 * useSplit keeps the width of the file browser and, at lg and wider, sets the height of the
 * layout so that it fills the window below its top. The file browser and the editor scroll
 * inside that height, so the page does not scroll.
 */
function useSplit() {
  const [el, container] = useState<HTMLDivElement | null>(null);
  const [width, setWidthState] = useState(() => {
    try {
      const v = Number(localStorage.getItem(splitStorageKey));
      return Number.isFinite(v) && v >= splitMin ? v : splitDefault;
    } catch {
      return splitDefault;
    }
  });
  const [height, setHeight] = useState<number | undefined>(undefined);
  const [containerWidth, setContainerWidth] = useState(0);
  const drag = useRef<{ x: number; w: number } | null>(null);

  const max = containerWidth > 0 ? Math.max(splitMin, Math.min(splitMax, containerWidth - editorMin)) : splitMax;
  const clamp = useCallback((w: number) => Math.round(Math.min(max, Math.max(splitMin, w))), [max]);

  const setWidth = (w: number) => {
    const v = clamp(w);
    setWidthState(v);
    try {
      localStorage.setItem(splitStorageKey, String(v));
    } catch {
      // Storage is not available. The width lasts for this page load.
    }
  };

  // The layout mounts after the file list loads, so the effect runs when the element exists.
  useLayoutEffect(() => {
    if (!el) return;
    const lg = window.matchMedia("(min-width: 1024px)");
    const measure = () => {
      setContainerWidth(el.clientWidth);
      if (!lg.matches) {
        setHeight(undefined);
        return;
      }
      const top = el.getBoundingClientRect().top + window.scrollY;
      setHeight(Math.max(480, Math.floor(window.innerHeight - top - pageBottom)));
    };
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    if (el.parentElement) ro.observe(el.parentElement);
    window.addEventListener("resize", measure);
    lg.addEventListener("change", measure);
    return () => {
      ro.disconnect();
      window.removeEventListener("resize", measure);
      lg.removeEventListener("change", measure);
    };
  }, [el]);

  // Keep the width inside the limits when the window gets narrower.
  useEffect(() => {
    if (containerWidth > 0) setWidthState((w) => clamp(w));
  }, [containerWidth, clamp]);

  return {
    container,
    width,
    min: splitMin,
    max,
    style: { "--tree-width": `${width}px`, height } as CSSProperties,
    onPointerDown: (e: PointerEvent<HTMLDivElement>) => {
      e.currentTarget.setPointerCapture(e.pointerId);
      drag.current = { x: e.clientX, w: width };
      document.body.style.cursor = "col-resize";
      document.body.style.userSelect = "none";
    },
    onPointerMove: (e: PointerEvent<HTMLDivElement>) => {
      if (drag.current) setWidth(drag.current.w + e.clientX - drag.current.x);
    },
    onPointerUp: (e: PointerEvent<HTMLDivElement>) => {
      if (e.currentTarget.hasPointerCapture(e.pointerId)) e.currentTarget.releasePointerCapture(e.pointerId);
      drag.current = null;
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
    },
    onKeyDown: (e: KeyboardEvent<HTMLDivElement>) => {
      const step = e.shiftKey ? 64 : 16;
      if (e.key === "ArrowLeft") setWidth(width - step);
      else if (e.key === "ArrowRight") setWidth(width + step);
      else if (e.key === "Home") setWidth(splitMin);
      else if (e.key === "End") setWidth(max);
      else return;
      e.preventDefault();
    },
    reset: () => setWidth(splitDefault),
  };
}

/** NewFileDialog stages an empty file. The server checks the path when the file is saved. */
function NewFileDialog({
  folder,
  exists,
  onClose,
  onCreate,
}: {
  folder: string;
  exists: (path: string) => boolean;
  onClose: () => void;
  onCreate: (path: string) => void;
}) {
  const [path, setPath] = useState(folder ? `${folder}/` : "");
  const trimmed = path.trim().replace(/^\/+/, "");
  const taken = trimmed !== "" && exists(trimmed);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (trimmed !== "" && !taken) onCreate(trimmed);
  };
  return (
    <Dialog open onClose={onClose} title="New file">
      <form onSubmit={submit} className="flex flex-col gap-4">
        <Field
          id="file-path"
          label="Path"
          hint="Relative to the namespace root, for example flows/etl.flow.yaml. The file is saved with the next save."
          error={taken ? "A file with this path exists." : undefined}
        >
          <Input
            className="font-mono"
            value={path}
            onChange={(e) => setPath(e.target.value)}
            required
            maxLength={512}
            autoFocus
          />
        </Field>
        <div className="flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" disabled={trimmed === "" || taken}>
            Create
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

/** SaveChangesDialog saves all staged files as one version with one message. */
function SaveChangesDialog({
  namespace,
  baseVersion,
  staged,
  onClose,
  onSaved,
}: {
  namespace: string;
  baseVersion?: number;
  staged: Record<string, string>;
  onClose: () => void;
  onSaved: (paths: string[], snapshot: Snapshot) => void;
}) {
  const paths = Object.keys(staged).sort();
  const [message, setMessage] = useState(paths.length === 1 ? `Update ${paths[0]}` : `Update ${paths.length} files`);
  const save = useSaveChanges(namespace);
  const conflict = save.error instanceof ApiError && save.error.code === "version_conflict";
  const submit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate(
      {
        message: message.trim(),
        base_version: baseVersion,
        changes: paths.map((p) => ({ op: "put", path: p, content: staged[p] })),
      },
      { onSuccess: (snapshot) => onSaved(paths, snapshot) },
    );
  };
  return (
    <Dialog open onClose={onClose} title="Save changes">
      <form onSubmit={submit} className="flex flex-col gap-4">
        <ul
          aria-label="Files to save"
          className="flex max-h-48 flex-col overflow-y-auto rounded-control border bg-muted/40 py-1 font-mono text-xs"
        >
          {paths.map((p) => (
            <li key={p} className="flex items-center gap-2 px-2.5 py-1" title={p}>
              <span aria-hidden className="h-1.5 w-1.5 shrink-0 rounded-full bg-accent" />
              <span className="truncate">{p}</span>
            </li>
          ))}
        </ul>
        <Field id="save-all-message" label="Commit message">
          <Input value={message} onChange={(e) => setMessage(e.target.value)} required maxLength={500} autoFocus />
        </Field>
        {save.isError && (
          <FormError>
            {conflict
              ? "The namespace changed. Reload the page; the staged files stay in this dialog until then."
              : errorMessage(save.error)}
          </FormError>
        )}
        <div className="flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" disabled={save.isPending || message.trim() === ""}>
            Save
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
