import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { FilePlus, Save, Upload } from "lucide-react";
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type FormEvent,
  type KeyboardEvent,
  type PointerEvent,
} from "react";
import { listFilesOptions } from "@/api/@tanstack/react-query.gen";
import { ApiError, rawFetch } from "@/api-client";
import { DataState } from "@/components/data-state";
import { FileEditorPanel, fileContentKey, useSaveChanges } from "@/features/namespaces/FileEditorPanel";
import { FileTree } from "@/features/namespaces/FileTree";
import { invalidateNamespace } from "@/features/namespaces/namespace-source";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, FormError } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { errorMessage } from "@/lib/errors";
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
}: {
  namespace: string;
  canEdit: boolean;
  /** canPush lets an editor of a git namespace push edits to a new branch. */
  canPush?: boolean;
  selected?: string;
  onSelect: (path: string | undefined) => void;
}) {
  const qc = useQueryClient();
  const [newOpen, setNewOpen] = useState(false);
  const [saveOpen, setSaveOpen] = useState(false);
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
      onSelect(file.name);
    },
  });

  const clear = (paths: string[]) => {
    setStaged((s) => Object.fromEntries(Object.entries(s).filter(([p]) => !paths.includes(p))));
    setCreated((c) => c.filter((p) => !paths.includes(p)));
  };
  const stagedPaths = Object.keys(staged).sort();

  return (
    <DataState query={files}>
      {(list) => {
        const existing = new Set(list.items.map((f) => f.path));
        const newPaths = created.filter((p) => !existing.has(p));
        const isNew = (p: string) => newPaths.includes(p);
        return (
          <div ref={split.container} style={split.style} className="flex flex-col gap-4 lg:flex-row lg:gap-0">
            <section
              aria-label="File browser"
              className="flex min-w-0 shrink-0 flex-col gap-2 lg:w-[var(--tree-width)]"
            >
              {canEdit && (
                <div className="flex flex-col gap-2">
                  <div className="flex flex-wrap gap-2">
                    <Button size="sm" variant="secondary" onClick={() => setNewOpen(true)}>
                      <FilePlus className="h-3.5 w-3.5" aria-hidden />
                      New file
                    </Button>
                    <label
                      className={cn(
                        "inline-flex h-7 cursor-pointer items-center gap-1.5 rounded-[6px] border border-input bg-panel px-2.5 text-xs font-medium hover:bg-muted focus-within:outline-2 focus-within:outline-ring",
                        upload.isPending && "pointer-events-none opacity-50",
                      )}
                    >
                      <Upload className="h-3.5 w-3.5" aria-hidden />
                      {upload.isPending ? "Uploading" : "Upload"}
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
                  {upload.isError && <FormError>{errorMessage(upload.error)}</FormError>}
                  {stagedPaths.length > 0 && (
                    <div className="flex flex-wrap items-center justify-between gap-2 rounded-[6px] border bg-panel px-3 py-2">
                      <span role="status" className="text-sm">
                        {stagedPaths.length === 1 ? "1 unsaved file" : `${stagedPaths.length} unsaved files`}
                      </span>
                      <Button size="sm" onClick={() => setSaveOpen(true)}>
                        <Save className="h-3.5 w-3.5" aria-hidden />
                        Save changes
                      </Button>
                    </div>
                  )}
                </div>
              )}
              {list.items.length === 0 && newPaths.length === 0 ? (
                <p className="py-6 text-sm text-muted-foreground">This namespace has no files.</p>
              ) : (
                <div className="max-h-80 min-h-0 flex-1 overflow-auto rounded-[8px] border bg-panel lg:max-h-none">
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
              className="group hidden w-4 shrink-0 cursor-col-resize touch-none justify-center outline-none lg:flex"
            >
              <div className="h-full w-px bg-border group-hover:w-0.5 group-hover:bg-accent group-focus-visible:w-0.5 group-focus-visible:bg-accent" />
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
                <p className="flex flex-1 items-center justify-center rounded-[8px] border bg-panel p-4 text-sm text-muted-foreground">
                  Select a file to open it.
                </p>
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
            {saveOpen && (
              <SaveChangesDialog
                namespace={namespace}
                baseVersion={list.version ?? undefined}
                staged={staged}
                onClose={() => setSaveOpen(false)}
                onSaved={(paths) => {
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

const splitMin = 224;
const splitMax = 576;
const splitDefault = 288;
/** editorMin is the smallest editor width that the splitter leaves. */
const editorMin = 420;
const splitStorageKey = "sluice-file-tree-width";
/** pageBottom is the bottom padding of the main element at lg (md:p-6). */
const pageBottom = 24;

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
  onSaved: (paths: string[]) => void;
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
      { onSuccess: () => onSaved(paths) },
    );
  };
  return (
    <Dialog open onClose={onClose} title="Save changes">
      <form onSubmit={submit} className="flex flex-col gap-4">
        <ul aria-label="Files to save" className="flex flex-col gap-0.5 font-mono text-xs">
          {paths.map((p) => (
            <li key={p} className="truncate" title={p}>
              {p}
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
