import { useMutation, useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { CornerLeftUp, Plus } from "lucide-react";
import { useState, type FormEvent } from "react";
import {
  deleteGlobalVariableMutation,
  deleteNamespaceVariableMutation,
  listGlobalVariablesOptions,
  listNamespaceVariablesOptions,
  putGlobalVariableMutation,
  putNamespaceVariableMutation,
} from "@/api/@tanstack/react-query.gen";
import type { VariableInfo } from "@/api/types.gen";
import { DataState } from "@/components/data-state";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, FormError } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import { useCurrentUser } from "@/lib/auth";
import { errorMessage, fieldErrors } from "@/lib/errors";
import { can } from "@/lib/roles";
import { formatTime } from "@/lib/utils";

function invalidateVariables(qc: QueryClient) {
  return qc.invalidateQueries({
    predicate: (q) => {
      const id = (q.queryKey[0] as { _id?: string } | undefined)?._id;
      return id === "listGlobalVariables" || id === "listNamespaceVariables";
    },
  });
}

/**
 * VariablesPanel lists the global variables, or the effective variables of a namespace
 * with the scope that defines each key (REQ-SEC-007, REQ-UI-008).
 */
export function VariablesPanel({ namespace }: { namespace?: string }) {
  const me = useCurrentUser();
  const canEdit = namespace ? can(me.role, "editor") : can(me.role, "admin");
  const globalVariables = useQuery({ ...listGlobalVariablesOptions(), enabled: !namespace, select: (d) => d.items });
  const namespaceVariables = useQuery({
    ...listNamespaceVariablesOptions({ path: { namespace: namespace ?? "" } }),
    enabled: !!namespace,
    select: (d) => d.items,
  });
  const variables = namespace ? namespaceVariables : globalVariables;
  const [editing, setEditing] = useState<VariableInfo | "new" | null>(null);
  const [deleting, setDeleting] = useState<VariableInfo | null>(null);

  return (
    <div className="flex flex-col gap-3">
      {canEdit && (
        <div className="flex justify-end">
          <Button onClick={() => setEditing("new")}>
            <Plus className="h-4 w-4" aria-hidden />
            Add variable
          </Button>
        </div>
      )}
      <DataState query={variables} empty={(d) => d.length === 0} emptyText="No variables exist in this scope.">
        {(items) => (
          <Table>
            <THead>
              <Tr>
                <Th>Key</Th>
                <Th>Value</Th>
                <Th>Scope</Th>
                <Th>Updated</Th>
                <Th>
                  <span className="sr-only">Actions</span>
                </Th>
              </Tr>
            </THead>
            <TBody>
              {items.map((v) => (
                <Tr key={v.key}>
                  <Td className="font-mono text-xs">{v.key}</Td>
                  <Td className="max-w-[24rem] truncate font-mono text-xs" title={v.value}>
                    {v.value}
                  </Td>
                  <Td>
                    {v.inherited ? <Badge icon={CornerLeftUp}>Inherited from {v.scope}</Badge> : <span className="text-sm">{v.scope}</span>}
                  </Td>
                  <Td>
                    {formatTime(v.updated_at)}
                    {v.updated_by && <span className="text-muted-foreground"> by {v.updated_by}</span>}
                  </Td>
                  <Td className="text-right whitespace-nowrap">
                    {canEdit && !v.inherited && (
                      <>
                        <Button variant="ghost" size="sm" onClick={() => setEditing(v)} aria-label={`Edit ${v.key}`}>
                          Edit
                        </Button>
                        <Button variant="ghost" size="sm" onClick={() => setDeleting(v)} aria-label={`Delete ${v.key}`}>
                          Delete
                        </Button>
                      </>
                    )}
                  </Td>
                </Tr>
              ))}
            </TBody>
          </Table>
        )}
      </DataState>
      <Dialog open={editing !== null} onClose={() => setEditing(null)} title={editing === "new" ? "Add variable" : "Edit variable"}>
        {editing !== null && (
          <VariableForm namespace={namespace} variable={editing === "new" ? undefined : editing} onDone={() => setEditing(null)} />
        )}
      </Dialog>
      <Dialog open={deleting !== null} onClose={() => setDeleting(null)} title="Delete variable">
        {deleting && <VariableDelete namespace={namespace} variable={deleting} onDone={() => setDeleting(null)} />}
      </Dialog>
    </div>
  );
}

function VariableForm({ namespace, variable, onDone }: { namespace?: string; variable?: VariableInfo; onDone: () => void }) {
  const qc = useQueryClient();
  const [key, setKey] = useState(variable?.key ?? "");
  const [value, setValue] = useState(variable?.value ?? "");
  const onSuccess = () => {
    void invalidateVariables(qc);
    onDone();
  };
  const putGlobal = useMutation({ ...putGlobalVariableMutation(), onSuccess });
  const putNamespace = useMutation({ ...putNamespaceVariableMutation(), onSuccess });
  const mutation = namespace ? putNamespace : putGlobal;
  const fields = fieldErrors(mutation.error);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (namespace) putNamespace.mutate({ path: { namespace, key }, body: { value } });
    else putGlobal.mutate({ path: { key }, body: { value } });
  };
  return (
    <form onSubmit={submit} className="flex flex-col gap-3">
      <Field id="variable-key" label="Key" hint="Letters, digits and underscores. Flows read it as vars.KEY." error={fields.key}>
        <Input
          required
          disabled={variable !== undefined}
          pattern="[A-Za-z_][A-Za-z0-9_]*"
          maxLength={128}
          value={key}
          onChange={(e) => setKey(e.target.value)}
          className="font-mono"
        />
      </Field>
      <Field id="variable-value" label="Value" error={fields.value}>
        <Input value={value} maxLength={65536} onChange={(e) => setValue(e.target.value)} className="font-mono" />
      </Field>
      {mutation.isError && <FormError>{errorMessage(mutation.error)}</FormError>}
      <div className="flex justify-end gap-2">
        <Button variant="secondary" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={mutation.isPending}>
          Save
        </Button>
      </div>
    </form>
  );
}

function VariableDelete({ namespace, variable, onDone }: { namespace?: string; variable: VariableInfo; onDone: () => void }) {
  const qc = useQueryClient();
  const onSuccess = () => {
    void invalidateVariables(qc);
    onDone();
  };
  const delGlobal = useMutation({ ...deleteGlobalVariableMutation(), onSuccess });
  const delNamespace = useMutation({ ...deleteNamespaceVariableMutation(), onSuccess });
  const mutation = namespace ? delNamespace : delGlobal;
  const run = () => {
    if (namespace) delNamespace.mutate({ path: { namespace, key: variable.key } });
    else delGlobal.mutate({ path: { key: variable.key } });
  };
  return (
    <div className="flex flex-col gap-3">
      <p className="text-sm">
        Delete <span className="font-mono">{variable.key}</span> from {variable.scope}?
      </p>
      {mutation.isError && <FormError>{errorMessage(mutation.error)}</FormError>}
      <div className="flex justify-end gap-2">
        <Button variant="secondary" onClick={onDone}>
          Cancel
        </Button>
        <Button variant="destructive" onClick={run} disabled={mutation.isPending}>
          Delete
        </Button>
      </div>
    </div>
  );
}

/** VariablesPage shows the global variables. Namespaces inherit them. */
export function VariablesPage() {
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Variables"
        description="Global variables. Flows read them as vars.KEY. A namespace, a parent or the flow can define the same key with a higher precedence."
      />
      <VariablesPanel />
    </div>
  );
}
