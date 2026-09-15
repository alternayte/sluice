import { useMutation, useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { AlertTriangle, CheckCircle2, CornerLeftUp, Eye, EyeOff, Plus, XCircle } from "lucide-react";
import { useState, type FormEvent } from "react";
import {
  checkGlobalSecretMutation,
  checkNamespaceSecretMutation,
  deleteGlobalSecretMutation,
  deleteNamespaceSecretMutation,
  listGlobalSecretsOptions,
  listNamespaceSecretsOptions,
  listSecretProvidersOptions,
  putGlobalSecretMutation,
  putNamespaceSecretMutation,
} from "@/api/@tanstack/react-query.gen";
import type { CheckResult, SecretInfo, SecretPutWritable } from "@/api/types.gen";
import { DataState } from "@/components/data-state";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, FormError } from "@/components/ui/field";
import { Input, Select } from "@/components/ui/input";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import { useCurrentUser } from "@/lib/auth";
import { errorMessage, fieldErrors } from "@/lib/errors";
import { can } from "@/lib/roles";
import { cn, formatTime } from "@/lib/utils";

/** MinMaskedLength is the shortest value that the server masks in logs (SI-10). */
const MinMaskedLength = 4;

function invalidateSecrets(qc: QueryClient) {
  return qc.invalidateQueries({
    predicate: (q) => {
      const id = (q.queryKey[0] as { _id?: string } | undefined)?._id;
      return id === "listGlobalSecrets" || id === "listNamespaceSecrets";
    },
  });
}

/**
 * SecretsPanel lists the secrets of the global scope, or the effective secrets of a
 * namespace with the scope that defines each key. Values are never shown (REQ-UI-008).
 */
export function SecretsPanel({ namespace }: { namespace?: string }) {
  const me = useCurrentUser();
  const canEdit = namespace ? can(me.role, "editor") : can(me.role, "admin");
  const globalSecrets = useQuery({
    ...listGlobalSecretsOptions(),
    enabled: !namespace,
    select: (d) => d.items,
  });
  const namespaceSecrets = useQuery({
    ...listNamespaceSecretsOptions({ path: { namespace: namespace ?? "" } }),
    enabled: !!namespace,
    select: (d) => d.items,
  });
  const secrets = namespace ? namespaceSecrets : globalSecrets;
  const [editing, setEditing] = useState<SecretInfo | "new" | null>(null);
  const [checking, setChecking] = useState<SecretInfo | null>(null);
  const [deleting, setDeleting] = useState<SecretInfo | null>(null);

  return (
    <div className="flex flex-col gap-3">
      {canEdit && (
        <div className="flex justify-end">
          <Button onClick={() => setEditing("new")}>
            <Plus className="h-4 w-4" aria-hidden />
            Add secret
          </Button>
        </div>
      )}
      <DataState query={secrets} empty={(d) => d.length === 0} emptyText="No secrets exist in this scope.">
        {(items) => (
          <Table>
            <THead>
              <Tr>
                <Th>Key</Th>
                <Th>Scope</Th>
                <Th>Provider</Th>
                <Th>Reference</Th>
                <Th>Updated</Th>
                <Th>Last used</Th>
                <Th>
                  <span className="sr-only">Actions</span>
                </Th>
              </Tr>
            </THead>
            <TBody>
              {items.map((s) => (
                <Tr key={s.key}>
                  <Td className="font-mono text-xs">{s.key}</Td>
                  <Td>
                    {s.inherited ? (
                      <Badge icon={CornerLeftUp}>Inherited from {s.scope}</Badge>
                    ) : (
                      <span className="text-sm">{s.scope}</span>
                    )}
                  </Td>
                  <Td>{s.provider}</Td>
                  <Td className="font-mono text-xs">{s.ref ?? ""}</Td>
                  <Td>
                    {formatTime(s.updated_at)}
                    {s.updated_by && <span className="text-muted-foreground"> by {s.updated_by}</span>}
                  </Td>
                  <Td>{s.last_resolved_at ? formatTime(s.last_resolved_at) : "Never"}</Td>
                  <Td className="text-right whitespace-nowrap">
                    {canEdit && !s.inherited && (
                      <>
                        <Button variant="ghost" size="sm" onClick={() => setChecking(s)} aria-label={`Check ${s.key}`}>
                          Check
                        </Button>
                        <Button variant="ghost" size="sm" onClick={() => setEditing(s)} aria-label={`Edit ${s.key}`}>
                          Edit
                        </Button>
                        <Button variant="ghost" size="sm" onClick={() => setDeleting(s)} aria-label={`Delete ${s.key}`}>
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
      <Dialog
        open={editing !== null}
        onClose={() => setEditing(null)}
        title={editing === "new" ? "Add secret" : "Edit secret"}
      >
        {editing !== null && (
          <SecretForm
            namespace={namespace}
            secret={editing === "new" ? undefined : editing}
            onDone={() => setEditing(null)}
          />
        )}
      </Dialog>
      <Dialog open={checking !== null} onClose={() => setChecking(null)} title="Check secret">
        {checking && <SecretCheck namespace={namespace} secret={checking} onDone={() => setChecking(null)} />}
      </Dialog>
      <Dialog open={deleting !== null} onClose={() => setDeleting(null)} title="Delete secret">
        {deleting && <SecretDelete namespace={namespace} secret={deleting} onDone={() => setDeleting(null)} />}
      </Dialog>
    </div>
  );
}

function SecretForm({ namespace, secret, onDone }: { namespace?: string; secret?: SecretInfo; onDone: () => void }) {
  const qc = useQueryClient();
  const providers = useQuery({
    ...listSecretProvidersOptions(),
    select: (d) => d.items,
  });
  const [key, setKey] = useState(secret?.key ?? "");
  const [provider, setProvider] = useState(secret?.provider ?? "builtin");
  const [value, setValue] = useState("");
  const [ref, setRef] = useState(secret?.ref ?? "");
  const [description, setDescription] = useState(secret?.description ?? "");
  const type = providers.data?.find((p) => p.name === provider)?.type ?? (provider === "builtin" ? "builtin" : "");
  const isBuiltin = type === "builtin";
  const onSuccess = () => {
    void invalidateSecrets(qc);
    onDone();
  };
  const putGlobal = useMutation({ ...putGlobalSecretMutation(), onSuccess });
  const putNamespace = useMutation({
    ...putNamespaceSecretMutation(),
    onSuccess,
  });
  const mutation = namespace ? putNamespace : putGlobal;
  const fields = fieldErrors(mutation.error);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const body: SecretPutWritable = { provider, description };
    if (isBuiltin) {
      if (value !== "") body.value = value;
    } else if (type !== "env" || ref !== "") {
      body.ref = ref;
    }
    if (namespace) putNamespace.mutate({ path: { namespace, key }, body });
    else putGlobal.mutate({ path: { key }, body });
  };

  return (
    <form onSubmit={submit} className="flex flex-col gap-3">
      <Field id="secret-key" label="Key" hint="Letters, digits and underscores." error={fields.key}>
        <Input
          required
          disabled={secret !== undefined}
          pattern="[A-Za-z_][A-Za-z0-9_]*"
          maxLength={128}
          value={key}
          onChange={(e) => setKey(e.target.value)}
          className="font-mono"
        />
      </Field>
      <Field id="secret-provider" label="Provider" error={fields.provider}>
        <Select value={provider} onChange={(e) => setProvider(e.target.value)}>
          {(providers.data ?? [{ name: "builtin", type: "builtin" }]).map((p) => (
            <option key={p.name} value={p.name}>
              {p.name === p.type ? p.name : `${p.name} (${p.type})`}
            </option>
          ))}
        </Select>
      </Field>
      {isBuiltin ? (
        <Field
          id="secret-value"
          label="Value"
          hint={
            secret
              ? "Leave empty to keep the stored value. The value is never shown again."
              : "The value is never shown again."
          }
          error={fields.value}
        >
          <SecretValueInput
            required={secret === undefined || secret.provider_type !== "builtin"}
            value={value}
            onChange={setValue}
          />
        </Field>
      ) : (
        <Field
          id="secret-ref"
          label="Reference"
          hint={
            type === "env"
              ? "Name after SLUICE_SECRET_. Empty uses the key."
              : type === "vault"
                ? "path#field"
                : type === "kubernetes"
                  ? "secret-name/key"
                  : "name or name/version"
          }
          error={fields.ref}
        >
          <Input
            required={type !== "env"}
            maxLength={512}
            value={ref}
            onChange={(e) => setRef(e.target.value)}
            className="font-mono"
          />
        </Field>
      )}
      {isBuiltin && value.length > 0 && value.length < MinMaskedLength && (
        <p role="alert" className="flex items-center gap-2 text-sm text-state-timed-out">
          <AlertTriangle className="h-4 w-4 shrink-0" aria-hidden />
          Values shorter than {MinMaskedLength} characters are not masked in logs.
        </p>
      )}
      <Field id="secret-description" label="Description" error={fields.description}>
        <Input maxLength={500} value={description} onChange={(e) => setDescription(e.target.value)} />
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

const checkLabels: Record<CheckResult["status"], string> = {
  ok: "The value resolves.",
  not_found: "The provider has no value for this reference.",
  access_denied: "The provider denied access.",
  provider_error: "The provider returned an error.",
};

function SecretCheck({ namespace, secret, onDone }: { namespace?: string; secret: SecretInfo; onDone: () => void }) {
  const checkGlobal = useMutation(checkGlobalSecretMutation());
  const checkNamespace = useMutation(checkNamespaceSecretMutation());
  const mutation = namespace ? checkNamespace : checkGlobal;
  const run = () => {
    if (namespace) checkNamespace.mutate({ path: { namespace, key: secret.key } });
    else checkGlobal.mutate({ path: { key: secret.key } });
  };
  const result = mutation.data;
  return (
    <div className="flex flex-col gap-3">
      <p className="text-sm">
        Resolve <span className="font-mono">{secret.key}</span> with provider {secret.provider}. The value is not shown.
      </p>
      {result && (
        <div role="status" className="flex flex-col gap-1">
          <Badge
            tone={result.status === "ok" ? "success" : "failed"}
            icon={result.status === "ok" ? CheckCircle2 : XCircle}
          >
            {checkLabels[result.status]}
          </Badge>
          {result.message && <p className="text-xs text-muted-foreground">{result.message}</p>}
        </div>
      )}
      {mutation.isError && <FormError>{errorMessage(mutation.error)}</FormError>}
      <div className="flex justify-end gap-2">
        <Button variant="secondary" onClick={onDone}>
          Close
        </Button>
        <Button onClick={run} disabled={mutation.isPending}>
          Check
        </Button>
      </div>
    </div>
  );
}

function SecretDelete({ namespace, secret, onDone }: { namespace?: string; secret: SecretInfo; onDone: () => void }) {
  const qc = useQueryClient();
  const onSuccess = () => {
    void invalidateSecrets(qc);
    onDone();
  };
  const delGlobal = useMutation({ ...deleteGlobalSecretMutation(), onSuccess });
  const delNamespace = useMutation({
    ...deleteNamespaceSecretMutation(),
    onSuccess,
  });
  const mutation = namespace ? delNamespace : delGlobal;
  const run = () => {
    if (namespace) delNamespace.mutate({ path: { namespace, key: secret.key } });
    else delGlobal.mutate({ path: { key: secret.key } });
  };
  return (
    <div className="flex flex-col gap-3">
      <p className="text-sm">
        Delete <span className="font-mono">{secret.key}</span> from {secret.scope}? Flows then get the value of a parent
        scope, or fail with secret_not_found.
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

/**
 * SecretValueInput is a resizable textarea for multi-line values such as keys and
 * certificates. The value is masked until the eye button reveals it. Field passes
 * id and the aria attributes.
 */
function SecretValueInput({
  value,
  onChange,
  required,
  ...aria
}: {
  value: string;
  onChange: (v: string) => void;
  required?: boolean;
  id?: string;
  "aria-invalid"?: boolean;
  "aria-describedby"?: string;
}) {
  const [shown, setShown] = useState(false);
  return (
    <div className="relative">
      <textarea
        {...aria}
        required={required}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        rows={3}
        spellCheck={false}
        autoComplete="off"
        autoCapitalize="off"
        autoCorrect="off"
        data-1p-ignore
        className={cn(
          "block max-h-80 min-h-20 w-full [field-sizing:content] min-w-0 resize-y overflow-y-auto rounded-[6px] border border-input bg-background py-2 pr-10 pl-3 font-mono text-sm break-all text-foreground aria-[invalid=true]:border-destructive",
          !shown && "[-webkit-text-security:disc]",
        )}
      />
      <button
        type="button"
        aria-label={shown ? "Hide secret" : "Show secret"}
        aria-pressed={shown}
        title={shown ? "Hide secret" : "Show secret"}
        onClick={() => setShown(!shown)}
        className="absolute top-1.5 right-1.5 flex h-7 w-7 items-center justify-center rounded-[4px] text-muted-foreground hover:bg-muted hover:text-foreground"
      >
        {shown ? <EyeOff className="h-4 w-4" aria-hidden /> : <Eye className="h-4 w-4" aria-hidden />}
      </button>
    </div>
  );
}
