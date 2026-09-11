import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { FilePlus, Upload } from "lucide-react";
import { useState } from "react";
import { listFilesOptions } from "@/api/@tanstack/react-query.gen";
import { rawFetch } from "@/api-client";
import { DataState } from "@/components/data-state";
import { FileEditorPanel, PathDialog } from "@/features/namespaces/FileEditorPanel";
import { invalidateNamespace } from "@/features/namespaces/namespace-source";
import { Button } from "@/components/ui/button";
import { FormError } from "@/components/ui/field";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import { errorMessage } from "@/lib/errors";
import { cn, formatBytes } from "@/lib/utils";

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
              <FileEditorPanel
                key={selected}
                namespace={namespace}
                path={selected}
                baseVersion={list.version ?? undefined}
                canEdit={canEdit}
                gitPush={canPush}
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
