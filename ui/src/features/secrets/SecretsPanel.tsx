import { useMutation, useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  CheckCircle2,
  CornerLeftUp,
  Eye,
  EyeOff,
  KeyRound,
  Pencil,
  Plus,
  ShieldCheck,
  Trash2,
  XCircle,
} from "lucide-react";
import { useState, type FormEvent, type ReactNode } from "react";
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
import { Input, Select, Textarea } from "@/components/ui/input";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import { toast } from "@/components/ui/toast";
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

  const isEmpty = secrets.data !== undefined && secrets.data.length === 0;
  const addButton = (
    <Button size="sm" onClick={() => setEditing("new")}>
      <Plus className="h-3.5 w-3.5" aria-hidden />
      Add secret
    </Button>
  );

  return (
    <div className="flex flex-col gap-3">
      {secrets.data !== undefined && !isEmpty && (
        <div className="flex min-h-8 flex-wrap items-center justify-between gap-2">
          <p className="text-xs text-muted-foreground">
            {secrets.data.length === 1 ? "1 secret" : `${secrets.data.length} secrets`}. Values are never shown.
          </p>
          {canEdit && addButton}
        </div>
      )}
      <DataState
        query={secrets}
        empty={(d) => d.length === 0}
        emptyText="No secrets exist in this scope."
        emptyIcon={KeyRound}
        emptyAction={canEdit && addButton}
      >
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
                <Tr key={s.key} className="group/row">
                  <Td className="font-mono text-xs font-medium">{s.key}</Td>
                  <Td>
                    {s.inherited ? (
                      <Badge icon={CornerLeftUp}>Inherited from {s.scope}</Badge>
                    ) : (
                      <span className="text-sm">{s.scope}</span>
                    )}
                  </Td>
                  <Td>
                    <span className="rounded-inner bg-muted px-1.5 py-0.5 font-mono text-xs">{s.provider}</span>
                  </Td>
                  <Td className="max-w-64 truncate font-mono text-xs text-muted-foreground" title={s.ref || undefined}>
                    {s.ref ?? ""}
                  </Td>
                  <Td className="tabular-nums">
                    {formatTime(s.updated_at)}
                    {s.updated_by && <span className="text-muted-foreground"> by {s.updated_by}</span>}
                  </Td>
                  <Td className={cn("tabular-nums", !s.last_resolved_at && "text-muted-foreground")}>
                    {s.last_resolved_at ? formatTime(s.last_resolved_at) : "Never"}
                  </Td>
                  <Td className="w-0 text-right">
                    {canEdit && !s.inherited && (
                      <span className="inline-flex items-center gap-0.5">
                        <RowAction label={`Check ${s.key}`} title="Check" onClick={() => setChecking(s)}>
                          <ShieldCheck className="h-3.5 w-3.5" aria-hidden />
                        </RowAction>
                        <RowAction label={`Edit ${s.key}`} title="Edit" onClick={() => setEditing(s)}>
                          <Pencil className="h-3.5 w-3.5" aria-hidden />
                        </RowAction>
                        <RowAction label={`Delete ${s.key}`} title="Delete" destructive onClick={() => setDeleting(s)}>
                          <Trash2 className="h-3.5 w-3.5" aria-hidden />
                        </RowAction>
                      </span>
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
    toast({ title: secret ? `Updated ${key}` : `Added ${key}` });
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
    <form onSubmit={submit} className="flex flex-col gap-4">
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
        <p
          role="alert"
          className="flex animate-enter items-start gap-2 rounded-control bg-state-timed-out/10 px-2.5 py-2 text-xs"
        >
          <AlertTriangle className="h-3.5 w-3.5 shrink-0 text-state-timed-out" aria-hidden />
          Values shorter than {MinMaskedLength} characters are not masked in logs.
        </p>
      )}
      <Field id="secret-description" label="Description" error={fields.description}>
        <Input maxLength={500} value={description} onChange={(e) => setDescription(e.target.value)} />
      </Field>
      {mutation.isError && <FormError>{errorMessage(mutation.error)}</FormError>}
      <div className="flex justify-end gap-2 pt-1">
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
        <div role="status" className="flex animate-enter flex-col gap-1 rounded-control bg-muted/60 px-3 py-2">
          <Badge
            tone={result.status === "ok" ? "success" : "failed"}
            icon={result.status === "ok" ? CheckCircle2 : XCircle}
          >
            {checkLabels[result.status]}
          </Badge>
          {result.message && <p className="text-xs break-words text-muted-foreground">{result.message}</p>}
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
    toast({ title: `Deleted ${secret.key}` });
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
      <Textarea
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
          "block max-h-80 min-h-20 [field-sizing:content] resize-y overflow-y-auto py-2 pr-10 font-mono text-xs leading-5 break-all",
          !shown && "[-webkit-text-security:disc]",
        )}
      />
      <button
        type="button"
        aria-label={shown ? "Hide secret" : "Show secret"}
        aria-pressed={shown}
        title={shown ? "Hide secret" : "Show secret"}
        onClick={() => setShown(!shown)}
        className="pressable absolute top-1.5 right-1.5 flex h-7 w-7 items-center justify-center rounded-inner text-muted-foreground hover:bg-muted hover:text-foreground aria-pressed:text-accent"
      >
        {shown ? (
          <EyeOff key="hide" className="h-4 w-4 animate-enter" aria-hidden />
        ) : (
          <Eye key="show" className="h-4 w-4 animate-enter" aria-hidden />
        )}
      </button>
    </div>
  );
}

/** RowAction is an icon button in a table row. Its label names the row, for example "Edit KEY". */
function RowAction({
  label,
  title,
  destructive,
  onClick,
  children,
}: {
  label: string;
  title: string;
  destructive?: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <Button
      variant="ghost"
      size="icon"
      className={cn("h-7 w-7", destructive && "hover:text-destructive")}
      aria-label={label}
      title={title}
      onClick={onClick}
    >
      {children}
    </Button>
  );
}
