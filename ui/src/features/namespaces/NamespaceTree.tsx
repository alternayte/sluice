import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { FilePlus, Save, Upload } from "lucide-react";
import { useState, type FormEvent } from "react";
import { listFilesOptions } from "@/api/@tanstack/react-query.gen";
import { ApiError, rawFetch } from "@/api-client";
import { DataState } from "@/components/data-state";
import { FileEditorPanel, fileContentKey, useSaveChanges } from "@/features/namespaces/FileEditorPanel";
import { invalidateNamespace } from "@/features/namespaces/namespace-source";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, FormError } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import { errorMessage } from "@/lib/errors";
import { cn, formatBytes } from "@/lib/utils";

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
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,20rem)_minmax(0,1fr)]">
            <section aria-label="Files" className="flex min-w-0 flex-col gap-2">
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
                <Table>
                  <THead>
                    <Tr>
                      <Th>Path</Th>
                      <Th className="text-right">Size</Th>
                    </Tr>
                  </THead>
                  <TBody>
                    {[...list.items.map((f) => ({ path: f.path, size: f.size as number | undefined })), ...newPaths.map((p) => ({ path: p, size: undefined }))]
                      .sort((a, b) => a.path.localeCompare(b.path))
                      .map((f) => (
                        <Tr key={f.path} className={cn(f.path === selected && "bg-accent-soft")}>
                          <Td className="max-w-64">
                            <div className="flex min-w-0 items-center gap-1.5">
                              <button
                                type="button"
                                aria-current={f.path === selected ? "true" : undefined}
                                onClick={() => onSelect(f.path)}
                                className="block min-w-0 truncate text-left font-mono text-xs hover:underline"
                                title={f.path}
                              >
                                {f.path}
                              </button>
                              {isNew(f.path) && <Badge tone="accent">New</Badge>}
                              {!isNew(f.path) && staged[f.path] !== undefined && (
                                <span className="text-xs text-muted-foreground" title="Unsaved changes">
                                  ●<span className="sr-only">Unsaved changes</span>
                                </span>
                              )}
                            </div>
                          </Td>
                          <Td className="text-right text-xs text-muted-foreground">{f.size === undefined ? "—" : formatBytes(f.size)}</Td>
                        </Tr>
                      ))}
                  </TBody>
                </Table>
              )}
            </section>
            <section aria-label="Editor" className="min-w-0">
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
                <p className="rounded-[8px] border bg-panel p-4 text-sm text-muted-foreground">Select a file to open it.</p>
              )}
            </section>
            {newOpen && (
              <NewFileDialog
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

/** NewFileDialog stages an empty file. The server checks the path when the file is saved. */
function NewFileDialog({ exists, onClose, onCreate }: { exists: (path: string) => boolean; onClose: () => void; onCreate: (path: string) => void }) {
  const [path, setPath] = useState("");
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
          <Input className="font-mono" value={path} onChange={(e) => setPath(e.target.value)} required maxLength={512} autoFocus />
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
      { message: message.trim(), base_version: baseVersion, changes: paths.map((p) => ({ op: "put", path: p, content: staged[p] })) },
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
          <FormError>{conflict ? "The namespace changed. Reload the page; the staged files stay in this dialog until then." : errorMessage(save.error)}</FormError>
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
