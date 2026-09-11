import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { GitBranch, Pencil, Play, Save, Trash2 } from "lucide-react";
import { useCallback, useState, type FormEvent } from "react";
import { pushNamespaceBranchMutation, saveChangesMutation } from "@/api/@tanstack/react-query.gen";
import { validateFile } from "@/api/sdk.gen";
import type { SaveChangesRequest } from "@/api/types.gen";
import { ApiError, rawFetch } from "@/api-client";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DataState } from "@/components/data-state";
import { CodeEditor, type Issue } from "@/components/editor/code-editor";
import { isValidatedPath } from "@/components/editor/languages";
import { invalidateNamespace } from "@/features/namespaces/namespace-source";
import { RunFileDialog } from "@/components/run-dialogs";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, FormError } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { useCurrentUser } from "@/lib/auth";
import { errorMessage } from "@/lib/errors";
import { runnableFile } from "@/lib/executions";
import { can } from "@/lib/roles";

export function useSaveChanges(namespace: string) {
  const qc = useQueryClient();
  const mutation = useMutation(saveChangesMutation());
  return {
    ...mutation,
    mutate: (body: SaveChangesRequest, options?: { onSuccess?: () => void }) =>
      mutation.mutate(
        { path: { namespace }, body },
        {
          onSuccess: () => {
            invalidateNamespace(qc, namespace);
            options?.onSuccess?.();
          },
        },
      ),
  };
}

/** fileContentKey is the query key of the head content of a file. */
export const fileContentKey = (namespace: string, path: string) => ["namespace", namespace, "file", path];

export function FileEditorPanel({
  namespace,
  path,
  baseVersion,
  canEdit,
  gitPush = false,
  onSelect,
  draft,
  onDraft,
  isNew = false,
  onDiscard,
}: {
  namespace: string;
  path: string;
  baseVersion?: number;
  canEdit: boolean;
  /** gitPush makes the editor editable for a git namespace: changes go to a new branch (REQ-GIT-005). */
  gitPush?: boolean;
  onSelect: (path: string | undefined) => void;
  /** draft is the staged content of the file. The parent keeps it, so that drafts of several files are saved together. */
  draft?: string;
  /** onDraft stages new content, or clears the draft with null. */
  onDraft: (content: string | null) => void;
  /** isNew marks a staged file that no version has yet. */
  isNew?: boolean;
  onDiscard?: () => void;
}) {
  const qc = useQueryClient();
  const contentKey = fileContentKey(namespace, path);
  const content = useQuery({
    queryKey: contentKey,
    queryFn: async () => {
      const res = await rawFetch(
        `/api/v1/namespaces/${encodeURIComponent(namespace)}/file?${new URLSearchParams({ path }).toString()}`,
      );
      return res.text();
    },
    enabled: !isNew,
  });
  const [issues, setIssues] = useState<Issue[]>([]);
  const [dialog, setDialog] = useState<"save" | "rename" | "delete" | "run" | "push" | null>(null);
  const del = useSaveChanges(namespace);
  const me = useCurrentUser();
  const canRun = !isNew && can(me.role, "operator") && runnableFile(path);
  const validated = isValidatedPath(path);

  const validate = useCallback(
    async (text: string) => {
      const { data } = await validateFile({
        path: { namespace },
        body: { path, content: text },
        throwOnError: true,
      });
      return data.errors;
    },
    [namespace, path],
  );

  const body = (saved: string) => {
        const value = draft ?? saved;
        const dirty = isNew || (draft !== undefined && draft !== saved);
        return (
          <div className="flex flex-col gap-2">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="flex min-w-0 items-center gap-2">
                <h2 id="editor-label" className="truncate font-mono text-sm" title={path}>
                  {path}
                </h2>
                {dirty && <Badge tone="warning">Unsaved changes</Badge>}
              </div>
              {(canEdit || canRun || gitPush) && (
                <div className="flex flex-wrap gap-2">
                  {canRun && (
                    <Button size="sm" variant="secondary" onClick={() => setDialog("run")}>
                      <Play className="h-3.5 w-3.5" aria-hidden />
                      Run
                    </Button>
                  )}
                  {gitPush && (
                    <Button size="sm" disabled={!dirty} onClick={() => setDialog("push")}>
                      <GitBranch className="h-3.5 w-3.5" aria-hidden />
                      Push to branch
                    </Button>
                  )}
                  {canEdit && (
                    <>
                      {isNew ? (
                        <Button size="sm" variant="ghost" onClick={onDiscard}>
                          <Trash2 className="h-3.5 w-3.5" aria-hidden />
                          Discard
                        </Button>
                      ) : (
                        <>
                          <Button size="sm" variant="ghost" onClick={() => setDialog("rename")}>
                            <Pencil className="h-3.5 w-3.5" aria-hidden />
                            Rename
                          </Button>
                          <Button size="sm" variant="ghost" onClick={() => setDialog("delete")}>
                            <Trash2 className="h-3.5 w-3.5" aria-hidden />
                            Delete
                          </Button>
                        </>
                      )}
                      <Button size="sm" disabled={!dirty} onClick={() => setDialog("save")}>
                        <Save className="h-3.5 w-3.5" aria-hidden />
                        Save
                      </Button>
                    </>
                  )}
                </div>
              )}
            </div>
            <CodeEditor
              className="h-[60vh] min-h-80"
              value={value}
              path={path}
              label={`Content of ${path}`}
              readOnly={!canEdit && !gitPush}
              onChange={(text) => onDraft(!isNew && text === saved ? null : text)}
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
                isNew={isNew}
                baseVersion={baseVersion}
                onClose={() => setDialog(null)}
                onSaved={() => {
                  qc.setQueryData(contentKey, value);
                  onDraft(null);
                  setDialog(null);
                }}
                onReload={() => {
                  onDraft(null);
                  setDialog(null);
                  invalidateNamespace(qc, namespace);
                }}
              />
            )}
            {dialog === "push" && (
              <PushDialog namespace={namespace} path={path} content={value} onClose={() => setDialog(null)} onPushed={() => onDraft(null)} />
            )}
            {dialog === "run" && (
              <RunFileDialog namespace={namespace} path={path} onClose={() => setDialog(null)} />
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
  };

  return isNew ? body("") : <DataState query={content}>{body}</DataState>;
}

function SaveDialog({
  namespace,
  path,
  content,
  isNew,
  baseVersion,
  onClose,
  onSaved,
  onReload,
}: {
  namespace: string;
  path: string;
  content: string;
  isNew: boolean;
  baseVersion?: number;
  onClose: () => void;
  onSaved: () => void;
  onReload: () => void;
}) {
  const [message, setMessage] = useState(`${isNew ? "Create" : "Update"} ${path}`);
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

/** PushDialog commits the edited file to a new branch of the git source (REQ-GIT-005). */
function PushDialog({
  namespace,
  path,
  content,
  onClose,
  onPushed,
}: {
  namespace: string;
  path: string;
  content: string;
  onClose: () => void;
  onPushed: () => void;
}) {
  const [message, setMessage] = useState(`Update ${path}`);
  const push = useMutation({ ...pushNamespaceBranchMutation(), onSuccess: onPushed });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    push.mutate({ path: { namespace }, body: { message: message.trim(), changes: [{ op: "put", path, content }] } });
  };
  return (
    <Dialog open onClose={onClose} title="Push to branch">
      {push.data ? (
        <div className="flex flex-col gap-4">
          <p role="status" className="text-sm">
            Pushed to branch <span className="font-mono">{push.data.branch}</span>. The namespace changes when the branch is merged
            and synced.
          </p>
          <div className="flex justify-end">
            <Button onClick={onClose}>Done</Button>
          </div>
        </div>
      ) : (
        <form onSubmit={submit} className="flex flex-col gap-4">
          <p className="text-sm text-muted-foreground">
            The change goes to a new branch from the last synced commit. The tracked branch does not change.
          </p>
          <Field id="push-message" label="Commit message">
            <Input value={message} onChange={(e) => setMessage(e.target.value)} required maxLength={2000} autoFocus />
          </Field>
          {push.isError && <FormError>{errorMessage(push.error)}</FormError>}
          <div className="flex justify-end gap-2">
            <Button variant="secondary" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={push.isPending || message.trim() === ""}>
              Push
            </Button>
          </div>
        </form>
      )}
    </Dialog>
  );
}

export function PathDialog({
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
