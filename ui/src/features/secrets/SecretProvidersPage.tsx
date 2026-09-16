import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, Plus, XCircle } from "lucide-react";
import { useState, type FormEvent } from "react";
import {
  checkSecretProviderMutation,
  createSecretProviderMutation,
  deleteSecretProviderMutation,
  listSecretProvidersOptions,
} from "@/api/@tanstack/react-query.gen";
import type { ProviderOut } from "@/api/types.gen";
import { DataState } from "@/components/data-state";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, FormError } from "@/components/ui/field";
import { Input, Select } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import { errorMessage, fieldErrors } from "@/lib/errors";
import { formatTime } from "@/lib/utils";

type ExternalType = "kubernetes" | "azure_key_vault" | "vault";

/** configFields lists the configuration fields of each provider type. None holds a credential. */
const configFields: Record<ExternalType, { key: string; label: string; hint: string; required?: boolean }[]> = {
  kubernetes: [{ key: "namespace", label: "Kubernetes namespace", hint: "Empty uses the namespace of the server." }],
  azure_key_vault: [{ key: "vault_url", label: "Vault URL", hint: "https://<name>.vault.azure.net", required: true }],
  vault: [
    { key: "mount", label: "KV v2 mount", hint: "Default secret." },
    { key: "address", label: "Address", hint: "Empty uses SLUICE_VAULT_ADDR." },
  ],
};

const typeLabels: Record<ProviderOut["type"], string> = {
  builtin: "Builtin",
  env: "Environment",
  kubernetes: "Kubernetes",
  azure_key_vault: "Azure Key Vault",
  vault: "HashiCorp Vault",
};

const providersKey = [{ _id: "listSecretProviders" }];

/** SecretProvidersPage manages the external secret providers (REQ-UI-009, REQ-SEC-001). */
export function SecretProvidersPage() {
  const providers = useQuery({ ...listSecretProvidersOptions(), select: (d) => d.items });
  const [creating, setCreating] = useState(false);
  const [checking, setChecking] = useState<ProviderOut | null>(null);
  const [deleting, setDeleting] = useState<ProviderOut | null>(null);
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Secret providers"
        description="Secrets resolve from these providers. Credentials come from the server environment, not from this page."
        actions={
          <Button onClick={() => setCreating(true)}>
            <Plus className="h-4 w-4" aria-hidden />
            Add provider
          </Button>
        }
      />
      <DataState query={providers} empty={(d) => d.length === 0} emptyText="No secret providers exist.">
        {(items) => (
          <Table>
            <THead>
              <Tr>
                <Th>Name</Th>
                <Th>Type</Th>
                <Th>Configuration</Th>
                <Th>Updated</Th>
                <Th>
                  <span className="sr-only">Actions</span>
                </Th>
              </Tr>
            </THead>
            <TBody>
              {items.map((p) => {
                const fixed = p.type === "builtin" || p.type === "env";
                return (
                  <Tr key={p.name}>
                    <Td className="font-medium">{p.name}</Td>
                    <Td>{typeLabels[p.type]}</Td>
                    <Td className="font-mono text-xs">
                      {Object.entries(p.config)
                        .map(([k, v]) => `${k}=${String(v)}`)
                        .join(", ")}
                    </Td>
                    <Td>{formatTime(p.updated_at)}</Td>
                    <Td className="text-right whitespace-nowrap">
                      {p.type !== "builtin" && (
                        <Button variant="ghost" size="sm" onClick={() => setChecking(p)} aria-label={`Check ${p.name}`}>
                          Check
                        </Button>
                      )}
                      {!fixed && (
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => setDeleting(p)}
                          aria-label={`Delete ${p.name}`}
                        >
                          Delete
                        </Button>
                      )}
                    </Td>
                  </Tr>
                );
              })}
            </TBody>
          </Table>
        )}
      </DataState>
      <Dialog open={creating} onClose={() => setCreating(false)} title="Add provider">
        {creating && <ProviderForm onDone={() => setCreating(false)} />}
      </Dialog>
      <Dialog open={checking !== null} onClose={() => setChecking(null)} title="Check provider">
        {checking && <ProviderCheck provider={checking} onDone={() => setChecking(null)} />}
      </Dialog>
      <Dialog open={deleting !== null} onClose={() => setDeleting(null)} title="Delete provider">
        {deleting && <ProviderDelete provider={deleting} onDone={() => setDeleting(null)} />}
      </Dialog>
    </div>
  );
}

function ProviderForm({ onDone }: { onDone: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [type, setType] = useState<ExternalType>("vault");
  const [config, setConfig] = useState<Record<string, string>>({});
  const mutation = useMutation({
    ...createSecretProviderMutation(),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: providersKey });
      onDone();
    },
  });
  const fields = fieldErrors(mutation.error);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    const cfg: Record<string, string> = {};
    for (const f of configFields[type]) {
      const v = config[f.key];
      if (v) cfg[f.key] = v;
    }
    mutation.mutate({ body: { name, type, config: cfg } });
  };
  return (
    <form onSubmit={submit} className="flex flex-col gap-3">
      <Field id="provider-name" label="Name" hint="Lower-case letters, digits, - and _." error={fields.name}>
        <Input
          required
          pattern="[a-z0-9][a-z0-9_\-]*"
          maxLength={63}
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
      </Field>
      <Field id="provider-type" label="Type" error={fields.type}>
        <Select
          value={type}
          onChange={(e) => {
            setType(e.target.value as ExternalType);
            setConfig({});
          }}
        >
          <option value="vault">HashiCorp Vault</option>
          <option value="azure_key_vault">Azure Key Vault</option>
          <option value="kubernetes">Kubernetes</option>
        </Select>
      </Field>
      {configFields[type].map((f) => (
        <Field
          key={f.key}
          id={`provider-${f.key}`}
          label={f.label}
          hint={f.hint}
          error={fields[`config.${f.key}`] ?? fields.config}
        >
          <Input
            required={f.required}
            value={config[f.key] ?? ""}
            onChange={(e) => setConfig({ ...config, [f.key]: e.target.value })}
          />
        </Field>
      ))}
      {mutation.isError && <FormError>{errorMessage(mutation.error)}</FormError>}
      <div className="flex justify-end gap-2">
        <Button variant="secondary" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={mutation.isPending}>
          Add provider
        </Button>
      </div>
    </form>
  );
}

function ProviderCheck({ provider, onDone }: { provider: ProviderOut; onDone: () => void }) {
  const [ref, setRef] = useState("");
  const mutation = useMutation(checkSecretProviderMutation());
  const submit = (e: FormEvent) => {
    e.preventDefault();
    mutation.mutate({ path: { name: provider.name }, body: { ref } });
  };
  const result = mutation.data;
  return (
    <form onSubmit={submit} className="flex flex-col gap-3">
      <Field id="check-ref" label="Reference" hint="The value is not shown, only whether it resolves.">
        <Input required maxLength={512} value={ref} onChange={(e) => setRef(e.target.value)} className="font-mono" />
      </Field>
      {result && (
        <div role="status" className="flex flex-col gap-1">
          <Badge
            tone={result.status === "ok" ? "success" : "failed"}
            icon={result.status === "ok" ? CheckCircle2 : XCircle}
          >
            {result.status === "ok" ? "The reference resolves." : `Check result: ${result.status.replace("_", " ")}`}
          </Badge>
          {result.message && <p className="text-xs text-muted-foreground">{result.message}</p>}
        </div>
      )}
      {mutation.isError && <FormError>{errorMessage(mutation.error)}</FormError>}
      <div className="flex justify-end gap-2">
        <Button variant="secondary" onClick={onDone}>
          Close
        </Button>
        <Button type="submit" disabled={mutation.isPending}>
          Check
        </Button>
      </div>
    </form>
  );
}

function ProviderDelete({ provider, onDone }: { provider: ProviderOut; onDone: () => void }) {
  const qc = useQueryClient();
  const mutation = useMutation({
    ...deleteSecretProviderMutation(),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: providersKey });
      onDone();
    },
  });
  return (
    <div className="flex flex-col gap-3">
      <p className="text-sm">Delete the provider {provider.name}? A provider that secrets use cannot be deleted.</p>
      {mutation.isError && <FormError>{errorMessage(mutation.error)}</FormError>}
      <div className="flex justify-end gap-2">
        <Button variant="secondary" onClick={onDone}>
          Cancel
        </Button>
        <Button
          variant="destructive"
          onClick={() => mutation.mutate({ path: { name: provider.name } })}
          disabled={mutation.isPending}
        >
          Delete
        </Button>
      </div>
    </div>
  );
}
