import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { FilePlus, GitBranch, Pencil, RotateCcw, Save, Trash2, Upload } from "lucide-react";
import { useCallback, useState, type FormEvent } from "react";
import { ApiError, api, unwrap } from "@/api/client";
import type { components } from "@/api/schema";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DataState } from "@/components/data-state";
import { DiffView } from "@/components/diff-view";
import { CodeEditor, type Issue } from "@/components/editor/code-editor";
import { isValidatedPath } from "@/components/editor/languages";
import { SourceBadges, invalidateNamespace } from "@/components/namespace-source";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, FormError } from "@/components/ui/field";
import { Input, Select } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import { Tabs } from "@/components/ui/tabs";
import { useCurrentUser } from "@/lib/auth";
import { errorMessage } from "@/lib/errors";
import { can } from "@/lib/roles";
import { cn, formatBytes, formatTime } from "@/lib/utils";

type SaveChangesRequest = components["schemas"]["SaveChangesRequest"];
type FileDiff = components["schemas"]["FileDiff"];
type Tab = "files" | "versions";
type NamespaceSearch = { tab?: "versions"; file?: string };

export const Route = createFileRoute("/namespaces/$namespace")({
  validateSearch: (s: Record<string, unknown>): NamespaceSearch => {
    const out: NamespaceSearch = {};
    if (s.tab === "versions") out.tab = "versions";
    if (typeof s.file === "string" && s.file !== "") out.file = s.file;
    return out;
  },
  component: NamespacePage,
});

const tabs: { value: Tab; label: string }[] = [
  { value: "files", label: "Files" },
  { value: "versions", label: "Versions" },
];

function useSaveChanges(namespace: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (body: SaveChangesRequest) =>
      unwrap(await api.POST("/api/v1/namespaces/{namespace}/changes", { params: { path: { namespace } }, body })),
    onSuccess: () => invalidateNamespace(qc, namespace),
  });
}

function NamespacePage() {
  const { namespace } = Route.useParams();
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const me = useCurrentUser();
  const info = useQuery({
    queryKey: ["namespace", namespace, "info"],
    queryFn: async () =>
      unwrap(await api.GET("/api/v1/namespaces/{namespace}", { params: { path: { namespace } } })),
  });
  const tab: Tab = search.tab ?? "files";

  return (
    <div className="flex flex-col gap-4">
      <PageHeader title={namespace} description={info.data?.description || undefined} />
      <DataState query={info}>
        {(ns) => {
          const isGit = ns.source_type === "git" || ns.read_only === true;
          const canEdit = can(me.role, "editor") && ns.source_type === "managed" && !ns.read_only;
          return (
            <>
              <SourceBadges ns={ns} />
              {isGit && (
                <p
                  role="note"
                  className="flex items-center gap-2 rounded-[8px] border bg-panel p-3 text-sm text-muted-foreground"
                >
                  <GitBranch className="h-4 w-4 shrink-0" aria-hidden />
                  This namespace comes from git and is read-only.
                </p>
              )}
              <Tabs
                label="Namespace sections"
                tabs={tabs}
                value={tab}
                onChange={(t) =>
                  void navigate({ search: (prev) => ({ ...prev, tab: t === "versions" ? "versions" : undefined }) })
                }
              />
              {tab === "files" ? (
                <FilesTab
                  namespace={namespace}
                  canEdit={canEdit}
                  selected={search.file}
                  onSelect={(file) => void navigate({ search: (prev) => ({ ...prev, file }) })}
                />
              ) : (
                <VersionsTab namespace={namespace} canEdit={canEdit} head={ns.head_version ?? undefined} />
              )}
            </>
          );
        }}
      </DataState>
    </div>
  );
}

function FilesTab({
  namespace,
  canEdit,
  selected,
  onSelect,
}: {
  namespace: string;
  canEdit: boolean;
  selected?: string;
  onSelect: (path: string | undefined) => void;
}) {
  const qc = useQueryClient();
  const [newOpen, setNewOpen] = useState(false);
  const files = useQuery({
    queryKey: ["namespace", namespace, "files"],
    queryFn: async () =>
      unwrap(await api.GET("/api/v1/namespaces/{namespace}/files", { params: { path: { namespace } } })),
  });
  const upload = useMutation({
    mutationFn: async (file: File) =>
      unwrap(
        await api.PUT("/api/v1/namespaces/{namespace}/file", {
          params: { path: { namespace }, query: { path: file.name, message: `Upload ${file.name}` } },
          body: file as unknown as string,
          bodySerializer: (b: unknown) => b,
          headers: { "Content-Type": "application/octet-stream" },
        }),
      ),
    onSuccess: (_, file) => {
      invalidateNamespace(qc, namespace);
      onSelect(file.name);
    },
  });

  return (
    <DataState query={files}>
      {(list) => (
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
              </div>
            )}
            {list.items.length === 0 ? (
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
                  {list.items.map((f) => (
                    <Tr key={f.path} className={cn(f.path === selected && "bg-accent-soft")}>
                      <Td className="max-w-64">
                        <button
                          type="button"
                          aria-current={f.path === selected ? "true" : undefined}
                          onClick={() => onSelect(f.path)}
                          className="block w-full truncate text-left font-mono text-xs hover:underline"
                          title={f.path}
                        >
                          {f.path}
                        </button>
                      </Td>
                      <Td className="text-right text-xs text-muted-foreground">{formatBytes(f.size)}</Td>
                    </Tr>
                  ))}
                </TBody>
              </Table>
            )}
          </section>
          <section aria-label="Editor" className="min-w-0">
            {selected ? (
              <FileEditor
                key={selected}
                namespace={namespace}
                path={selected}
                baseVersion={list.version ?? undefined}
                canEdit={canEdit}
                onSelect={onSelect}
              />
            ) : (
              <p className="rounded-[8px] border bg-panel p-4 text-sm text-muted-foreground">Select a file to open it.</p>
            )}
          </section>
          {newOpen && (
            <PathDialog
              title="New file"
              submitLabel="Create"
              initial=""
              namespace={namespace}
              onClose={() => setNewOpen(false)}
              build={(path) => ({
                message: `Create ${path}`,
                base_version: list.version ?? undefined,
                changes: [{ op: "put", path, content: "" }],
              })}
              onDone={(path) => {
                setNewOpen(false);
                onSelect(path);
              }}
            />
          )}
        </div>
      )}
    </DataState>
  );
}

function FileEditor({
  namespace,
  path,
  baseVersion,
  canEdit,
  onSelect,
}: {
  namespace: string;
  path: string;
  baseVersion?: number;
  canEdit: boolean;
  onSelect: (path: string | undefined) => void;
}) {
  const qc = useQueryClient();
  const contentKey = ["namespace", namespace, "file", path];
  const content = useQuery({
    queryKey: contentKey,
    queryFn: async () =>
      unwrap(
        await api.GET("/api/v1/namespaces/{namespace}/file", {
          params: { path: { namespace }, query: { path } },
          parseAs: "text",
        }),
      ) as string,
  });
  const [draft, setDraft] = useState<string | null>(null);
  const [issues, setIssues] = useState<Issue[]>([]);
  const [dialog, setDialog] = useState<"save" | "rename" | "delete" | null>(null);
  const del = useSaveChanges(namespace);
  const validated = isValidatedPath(path);

  const validate = useCallback(
    async (text: string) =>
      unwrap(
        await api.POST("/api/v1/namespaces/{namespace}/validate", {
          params: { path: { namespace } },
          body: { path, content: text },
        }),
      ).errors,
    [namespace, path],
  );

  return (
    <DataState query={content}>
      {(saved) => {
        const value = draft ?? saved;
        const dirty = draft !== null && draft !== saved;
        return (
          <div className="flex flex-col gap-2">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="flex min-w-0 items-center gap-2">
                <h2 id="editor-label" className="truncate font-mono text-sm" title={path}>
                  {path}
                </h2>
                {dirty && <Badge tone="warning">Unsaved changes</Badge>}
              </div>
              {canEdit && (
                <div className="flex flex-wrap gap-2">
                  <Button size="sm" variant="ghost" onClick={() => setDialog("rename")}>
                    <Pencil className="h-3.5 w-3.5" aria-hidden />
                    Rename
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => setDialog("delete")}>
                    <Trash2 className="h-3.5 w-3.5" aria-hidden />
                    Delete
                  </Button>
                  <Button size="sm" disabled={!dirty} onClick={() => setDialog("save")}>
                    <Save className="h-3.5 w-3.5" aria-hidden />
                    Save
                  </Button>
                </div>
              )}
            </div>
            <CodeEditor
              className="h-[60vh] min-h-80"
              value={value}
              path={path}
              label={`Content of ${path}`}
              readOnly={!canEdit}
              onChange={setDraft}
              validate={validated ? validate : undefined}
              onIssues={setIssues}
            />
            {validated && issues.length > 0 && (
              <div role="alert" aria-label="Validation errors" className="rounded-[8px] border bg-panel">
                <div className="border-b px-3 py-2 text-sm font-medium text-state-failed">
                  {issues.length === 1 ? "1 validation error" : `${issues.length} validation errors`}
                </div>
                <ul className="flex flex-col text-sm">
                  {issues.map((i, n) => (
                    <li key={n} className="flex flex-wrap gap-x-3 border-b px-3 py-1.5 last:border-0">
                      <span className="font-mono text-xs text-muted-foreground">
                        {i.line}:{i.column}
                      </span>
                      <span className="font-mono text-xs">{i.code}</span>
                      <span>{i.message}</span>
                    </li>
                  ))}
                </ul>
              </div>
            )}
            {dialog === "save" && (
              <SaveDialog
                namespace={namespace}
                path={path}
                content={value}
                baseVersion={baseVersion}
                onClose={() => setDialog(null)}
                onSaved={() => {
                  qc.setQueryData(contentKey, value);
                  setDraft(null);
                  setDialog(null);
                }}
                onReload={() => {
                  setDraft(null);
                  setDialog(null);
                  invalidateNamespace(qc, namespace);
                }}
              />
            )}
            {dialog === "rename" && (
              <PathDialog
                title="Rename file"
                submitLabel="Rename"
                initial={path}
                namespace={namespace}
                onClose={() => setDialog(null)}
                build={(newPath) => ({
                  message: `Rename ${path} to ${newPath}`,
                  base_version: baseVersion,
                  changes: [{ op: "rename", path, new_path: newPath }],
                })}
                onDone={(newPath) => onSelect(newPath)}
              />
            )}
            {dialog === "delete" && (
              <ConfirmDialog
                open
                title="Delete file"
                confirmLabel="Delete"
                destructive
                pending={del.isPending}
                error={del.isError ? errorMessage(del.error) : undefined}
                onClose={() => setDialog(null)}
                onConfirm={() =>
                  del.mutate(
                    { message: `Delete ${path}`, base_version: baseVersion, changes: [{ op: "delete", path }] },
                    { onSuccess: () => onSelect(undefined) },
                  )
                }
              >
                Delete <span className="font-mono">{path}</span>? This creates a new version.
              </ConfirmDialog>
            )}
          </div>
        );
      }}
    </DataState>
  );
}

function SaveDialog({
  namespace,
  path,
  content,
  baseVersion,
  onClose,
  onSaved,
  onReload,
}: {
  namespace: string;
  path: string;
  content: string;
  baseVersion?: number;
  onClose: () => void;
  onSaved: () => void;
  onReload: () => void;
}) {
  const [message, setMessage] = useState(`Update ${path}`);
  const save = useSaveChanges(namespace);
  const conflict = save.error instanceof ApiError && save.error.code === "version_conflict";

  const submit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate(
      { message: message.trim(), base_version: baseVersion, changes: [{ op: "put", path, content }] },
      { onSuccess: onSaved },
    );
  };

  return (
    <Dialog open onClose={onClose} title="Save file">
      <form onSubmit={submit} className="flex flex-col gap-4">
        <Field id="save-message" label="Commit message">
          <Input value={message} onChange={(e) => setMessage(e.target.value)} required maxLength={500} autoFocus />
        </Field>
        {save.isError && <FormError>{errorMessage(save.error)}</FormError>}
        <div className="flex justify-end gap-2">
          {conflict ? (
            <Button variant="secondary" onClick={onReload}>
              Reload
            </Button>
          ) : (
            <Button variant="secondary" onClick={onClose}>
              Cancel
            </Button>
          )}
          <Button type="submit" disabled={save.isPending || message.trim() === ""}>
            Save
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

function PathDialog({
  title,
  submitLabel,
  initial,
  namespace,
  build,
  onClose,
  onDone,
}: {
  title: string;
  submitLabel: string;
  initial: string;
  namespace: string;
  build: (path: string) => SaveChangesRequest;
  onClose: () => void;
  onDone: (path: string) => void;
}) {
  const [path, setPath] = useState(initial);
  const save = useSaveChanges(namespace);
  const trimmed = path.trim().replace(/^\/+/, "");

  const submit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate(build(trimmed), { onSuccess: () => onDone(trimmed) });
  };

  return (
    <Dialog open onClose={onClose} title={title}>
      <form onSubmit={submit} className="flex flex-col gap-4">
        <Field id="file-path" label="Path" hint="Relative to the namespace root, for example flows/etl.flow.yaml.">
          <Input
            className="font-mono"
            value={path}
            onChange={(e) => setPath(e.target.value)}
            required
            maxLength={512}
            autoFocus
          />
        </Field>
        {save.isError && <FormError>{errorMessage(save.error)}</FormError>}
        <div className="flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" disabled={save.isPending || trimmed === "" || trimmed === initial}>
            {submitLabel}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

function VersionsTab({ namespace, canEdit, head }: { namespace: string; canEdit: boolean; head?: number }) {
  const [revert, setRevert] = useState<number | null>(null);
  const versions = useQuery({
    queryKey: ["namespace", namespace, "versions"],
    queryFn: async () =>
      unwrap(
        await api.GET("/api/v1/namespaces/{namespace}/versions", {
          params: { path: { namespace }, query: { limit: 100 } },
        }),
      ),
  });

  return (
    <DataState query={versions} empty={(d) => d.items.length === 0} emptyText="This namespace has no versions.">
      {(d) => (
        <div className="flex flex-col gap-6">
          <Table>
            <THead>
              <Tr>
                <Th>Version</Th>
                <Th>Author</Th>
                <Th>Message</Th>
                <Th>Time</Th>
                {canEdit && (
                  <Th>
                    <span className="sr-only">Actions</span>
                  </Th>
                )}
              </Tr>
            </THead>
            <TBody>
              {d.items.map((s) => (
                <Tr key={s.id}>
                  <Td className="font-mono text-xs">
                    {s.version != null ? `v${s.version}` : (s.git_sha ?? "").slice(0, 12)}
                    {s.version != null && s.version === head && (
                      <span className="ml-2 font-sans text-muted-foreground">Head</span>
                    )}
                  </Td>
                  <Td>{s.author}</Td>
                  <Td className="max-w-96 truncate" title={s.message}>
                    {s.message}
                  </Td>
                  <Td>{formatTime(s.created_at)}</Td>
                  {canEdit && (
                    <Td className="text-right">
                      {s.version != null && s.version !== head && (
                        <Button size="sm" variant="secondary" onClick={() => setRevert(s.version ?? null)}>
                          <RotateCcw className="h-3.5 w-3.5" aria-hidden />
                          Revert to this version
                        </Button>
                      )}
                    </Td>
                  )}
                </Tr>
              ))}
            </TBody>
          </Table>
          <VersionDiff
            namespace={namespace}
            versions={d.items.flatMap((s) => (s.version == null ? [] : [s.version]))}
          />
          {revert !== null && <RevertDialog namespace={namespace} version={revert} onClose={() => setRevert(null)} />}
        </div>
      )}
    </DataState>
  );
}

function RevertDialog({ namespace, version, onClose }: { namespace: string; version: number; onClose: () => void }) {
  const qc = useQueryClient();
  const revert = useMutation({
    mutationFn: async () =>
      unwrap(
        await api.POST("/api/v1/namespaces/{namespace}/revert", {
          params: { path: { namespace } },
          body: { version },
        }),
      ),
    onSuccess: () => {
      invalidateNamespace(qc, namespace);
      onClose();
    },
  });
  return (
    <ConfirmDialog
      open
      title="Revert namespace"
      confirmLabel="Revert"
      pending={revert.isPending}
      error={revert.isError ? errorMessage(revert.error) : undefined}
      onConfirm={() => revert.mutate()}
      onClose={onClose}
    >
      Create a new version with the content of version {version}?
    </ConfirmDialog>
  );
}

const fileStatus: Record<FileDiff["status"], { label: string; tone: "success" | "failed" | "accent" }> = {
  added: { label: "Added", tone: "success" },
  removed: { label: "Removed", tone: "failed" },
  modified: { label: "Modified", tone: "accent" },
};

function VersionDiff({ namespace, versions }: { namespace: string; versions: number[] }) {
  const [from, setFrom] = useState<number | undefined>(versions[1]);
  const [to, setTo] = useState<number | undefined>(versions[0]);
  const enabled = from !== undefined && to !== undefined && from !== to;
  const diff = useQuery({
    queryKey: ["namespace", namespace, "diff", from, to],
    enabled,
    queryFn: async () =>
      unwrap(
        await api.GET("/api/v1/namespaces/{namespace}/diff", {
          params: { path: { namespace }, query: { from: from ?? 1, to: to ?? 1 } },
        }),
      ),
  });

  return (
    <section aria-labelledby="diff-heading" className="flex flex-col gap-3">
      <h2 id="diff-heading" className="text-base font-semibold">
        Compare versions
      </h2>
      {versions.length < 2 ? (
        <p className="text-sm text-muted-foreground">Two versions are necessary to show a diff.</p>
      ) : (
        <>
          <div className="grid max-w-md grid-cols-2 gap-3">
            <Field id="diff-from" label="From">
              <Select value={from ?? ""} onChange={(e) => setFrom(Number(e.target.value))}>
                {versions.map((v) => (
                  <option key={v} value={v}>
                    v{v}
                  </option>
                ))}
              </Select>
            </Field>
            <Field id="diff-to" label="To">
              <Select value={to ?? ""} onChange={(e) => setTo(Number(e.target.value))}>
                {versions.map((v) => (
                  <option key={v} value={v}>
                    v{v}
                  </option>
                ))}
              </Select>
            </Field>
          </div>
          {!enabled ? (
            <p className="text-sm text-muted-foreground">Select two different versions.</p>
          ) : (
            <DataState query={diff} empty={(d) => d.files.length === 0} emptyText="The versions have the same files.">
              {(d) => (
                <div className="flex flex-col gap-4">
                  {d.files.map((f) => (
                    <div key={f.path} className="flex min-w-0 flex-col gap-1.5">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="font-mono text-sm break-all">{f.path}</span>
                        <Badge tone={fileStatus[f.status].tone}>{fileStatus[f.status].label}</Badge>
                      </div>
                      {f.binary ? (
                        <p className="text-sm text-muted-foreground">Binary file. The diff is not shown.</p>
                      ) : (
                        <DiffView text={f.diff} label={`Diff of ${f.path}`} />
                      )}
                    </div>
                  ))}
                </div>
              )}
            </DataState>
          )}
        </>
      )}
    </section>
  );
}
