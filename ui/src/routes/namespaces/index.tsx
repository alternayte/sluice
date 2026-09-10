import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { Plus, Trash2 } from "lucide-react";
import { useState, type FormEvent } from "react";
import { api, unwrap } from "@/api/client";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DataState } from "@/components/data-state";
import { SourceBadges, invalidateNamespace, type Namespace } from "@/components/namespace-source";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, FormError } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import { useCurrentUser } from "@/lib/auth";
import { errorMessage, fieldErrors } from "@/lib/errors";
import { namespaceTree } from "@/lib/namespaces";
import { can } from "@/lib/roles";

export const Route = createFileRoute("/namespaces/")({
  component: NamespacesPage,
});

const indent = ["pl-3", "pl-7", "pl-11", "pl-15", "pl-19", "pl-23"];

function NamespacesPage() {
  const me = useCurrentUser();
  const [createOpen, setCreateOpen] = useState(false);
  const [deleting, setDeleting] = useState<string | null>(null);
  const namespaces = useQuery({
    queryKey: ["namespaces"],
    queryFn: async () => unwrap(await api.GET("/api/v1/namespaces")),
  });

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Namespaces"
        description="Namespaces group flows and files. Child namespaces use dots in the name."
        actions={
          can(me.role, "editor") && (
            <Button onClick={() => setCreateOpen(true)}>
              <Plus className="h-4 w-4" aria-hidden />
              Create namespace
            </Button>
          )
        }
      />
      <DataState query={namespaces} empty={(d) => d.items.length === 0} emptyText="There are no namespaces yet.">
        {(d) => (
          <Table>
            <THead>
              <Tr>
                <Th>Name</Th>
                <Th>Source</Th>
                <Th>Description</Th>
                {can(me.role, "admin") && (
                  <Th>
                    <span className="sr-only">Actions</span>
                  </Th>
                )}
              </Tr>
            </THead>
            <TBody>
              {namespaceTree(d.items).map(({ item, depth }) => (
                <Tr key={item.name}>
                  <Td className={indent[Math.min(depth, indent.length - 1)]}>
                    {item.implicit ? (
                      <span className="text-muted-foreground">{item.name}</span>
                    ) : (
                      <Link
                        to="/namespaces/$namespace"
                        params={{ namespace: item.name }}
                        className="font-medium text-accent hover:underline"
                      >
                        {item.name}
                      </Link>
                    )}
                  </Td>
                  <Td>
                    <SourceBadges ns={item} />
                  </Td>
                  <Td className="max-w-96 truncate text-muted-foreground" title={item.description}>
                    {item.description || "—"}
                  </Td>
                  {can(me.role, "admin") && (
                    <Td className="text-right">
                      {item.source_type === "managed" && !item.implicit && (
                        <Button size="sm" variant="ghost" onClick={() => setDeleting(item.name)}>
                          <Trash2 className="h-3.5 w-3.5" aria-hidden />
                          Delete
                        </Button>
                      )}
                    </Td>
                  )}
                </Tr>
              ))}
            </TBody>
          </Table>
        )}
      </DataState>
      {createOpen && <CreateNamespaceDialog onClose={() => setCreateOpen(false)} />}
      {deleting !== null && <DeleteNamespaceDialog name={deleting} onClose={() => setDeleting(null)} />}
    </div>
  );
}

function CreateNamespaceDialog({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const create = useMutation({
    mutationFn: async () =>
      unwrap(
        await api.POST("/api/v1/namespaces", {
          body: { name: name.trim(), description: description.trim() || undefined },
        }),
      ),
    onSuccess: (ns: Namespace) => {
      invalidateNamespace(qc, ns.name);
      onClose();
    },
  });
  const fields = fieldErrors(create.error);
  const hasFieldErrors = Object.keys(fields).length > 0;

  const submit = (e: FormEvent) => {
    e.preventDefault();
    create.mutate();
  };

  return (
    <Dialog open onClose={onClose} title="Create namespace">
      <form onSubmit={submit} className="flex flex-col gap-4">
        <Field
          id="ns-name"
          label="Name"
          hint="Lowercase letters, digits and hyphens. Use dots for child namespaces."
          error={fields.name}
        >
          <Input value={name} onChange={(e) => setName(e.target.value)} required autoFocus maxLength={128} />
        </Field>
        <Field id="ns-description" label="Description" error={fields.description}>
          <Input value={description} onChange={(e) => setDescription(e.target.value)} maxLength={2000} />
        </Field>
        {create.isError && !hasFieldErrors && <FormError>{errorMessage(create.error)}</FormError>}
        <div className="flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" disabled={create.isPending || name.trim() === ""}>
            Create
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

function DeleteNamespaceDialog({ name, onClose }: { name: string; onClose: () => void }) {
  const qc = useQueryClient();
  const del = useMutation({
    mutationFn: async () =>
      unwrap(await api.DELETE("/api/v1/namespaces/{namespace}", { params: { path: { namespace: name } } })),
    onSuccess: () => {
      invalidateNamespace(qc, name);
      onClose();
    },
  });
  return (
    <ConfirmDialog
      open
      title="Delete namespace"
      confirmLabel="Delete"
      destructive
      pending={del.isPending}
      error={del.isError ? errorMessage(del.error) : undefined}
      onConfirm={() => del.mutate()}
      onClose={onClose}
    >
      Delete the namespace <span className="font-medium">{name}</span> with all its files and versions? You cannot
      undo this.
    </ConfirmDialog>
  );
}
