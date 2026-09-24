import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, CircleOff, XCircle } from "lucide-react";
import { useState, type FormEvent } from "react";
import {
  deleteAiProviderMutation,
  getAiProviderOptions,
  getAiProviderQueryKey,
  getAiStatusQueryKey,
  putAiProviderMutation,
  testAiProviderMutation,
} from "@/api/@tanstack/react-query.gen";
import type { AiProvider } from "@/api/types.gen";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DataState } from "@/components/data-state";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { FormError } from "@/components/ui/field";
import { Input, Select } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { toast } from "@/components/ui/toast";
import { errorMessage, fieldErrors } from "@/lib/errors";
import { SettingsGroup, SettingsRow } from "@/components/ui/settings-group";

type ProviderType = "anthropic" | "openai_compatible";

/** AiSettingsPage configures the one AI provider and tests it (REQ-AI-001, REQ-UI-009). */
export function AiSettingsPage() {
  const provider = useQuery(getAiProviderOptions());
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="AI provider"
        description="The assistant, flow authoring and failure triage use this provider. The API key is a global secret; this page never shows it."
      />
      <DataState query={provider} skeleton="panel">
        {(p) => <ProviderForm key={JSON.stringify(p)} provider={p} />}
      </DataState>
    </div>
  );
}

function ProviderForm({ provider }: { provider: AiProvider }) {
  const qc = useQueryClient();
  const [type, setType] = useState<ProviderType>(provider.type ?? "anthropic");
  const [baseUrl, setBaseUrl] = useState(provider.base_url ?? "");
  const [model, setModel] = useState(provider.model ?? "");
  const [keyName, setKeyName] = useState(provider.api_key_secret_key ?? "");
  const [autoTriage, setAutoTriage] = useState(provider.auto_triage);
  const [removing, setRemoving] = useState(false);
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: getAiProviderQueryKey() });
    void qc.invalidateQueries({ queryKey: getAiStatusQueryKey() });
  };
  const save = useMutation({
    ...putAiProviderMutation(),
    onSuccess: () => {
      toast({ title: "AI provider saved" });
      refresh();
    },
  });
  const test = useMutation(testAiProviderMutation());
  const remove = useMutation({
    ...deleteAiProviderMutation(),
    onSuccess: () => {
      setRemoving(false);
      toast({ title: "AI provider removed", description: "AI actions are hidden until a provider is configured." });
      refresh();
    },
  });
  const fields = fieldErrors(save.error);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    test.reset();
    save.mutate({
      body: {
        type,
        base_url: baseUrl.trim() || undefined,
        model: model.trim(),
        api_key_secret_key: keyName.trim(),
        auto_triage: autoTriage,
      },
    });
  };
  const result = test.data;

  return (
    <form onSubmit={submit} className="flex max-w-2xl flex-col gap-6">
      <SettingsGroup>
        <SettingsRow label="Status">
          <p className="text-sm" role="status">
            <Badge
              tone={provider.configured ? "success" : "neutral"}
              icon={provider.configured ? CheckCircle2 : CircleOff}
            >
              {provider.configured ? "A provider is configured." : "No provider is configured. AI actions are hidden."}
            </Badge>
          </p>
        </SettingsRow>
      </SettingsGroup>
      <SettingsGroup title="Provider">
        <SettingsRow id="ai-type" label="Type" error={fields.type}>
          <Select value={type} onChange={(e) => setType(e.target.value as ProviderType)}>
            <option value="anthropic">Anthropic</option>
            <option value="openai_compatible">OpenAI compatible</option>
          </Select>
        </SettingsRow>
        <SettingsRow
          id="ai-base-url"
          label="Base URL"
          hint={
            type === "anthropic" ? "Empty uses https://api.anthropic.com." : "Empty uses https://api.openai.com/v1."
          }
          error={fields.base_url}
        >
          <Input value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} className="font-mono" />
        </SettingsRow>
        <SettingsRow id="ai-model" label="Model" error={fields.model}>
          <Input required value={model} onChange={(e) => setModel(e.target.value)} className="font-mono" />
        </SettingsRow>
        <SettingsRow
          id="ai-key"
          label="API key secret key"
          hint="A global secret that holds the API key."
          error={fields.api_key_secret_key}
        >
          <Input required value={keyName} onChange={(e) => setKeyName(e.target.value)} className="font-mono" />
        </SettingsRow>
      </SettingsGroup>
      <SettingsGroup title="Failure triage">
        <SettingsRow label="Automatic triage">
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={autoTriage}
              onChange={(e) => setAutoTriage(e.target.checked)}
              className="h-4 w-4 accent-accent"
            />
            Triage failed and timed out executions automatically
          </label>
        </SettingsRow>
      </SettingsGroup>
      {save.isError && <FormError>{errorMessage(save.error)}</FormError>}
      {test.isError && <FormError>{errorMessage(test.error)}</FormError>}
      {result && (
        <div
          role="status"
          className="flex animate-enter flex-col gap-1 rounded-panel border bg-panel px-4 py-3 shadow-panel"
        >
          <Badge
            tone={result.status === "ok" ? "success" : "failed"}
            icon={result.status === "ok" ? CheckCircle2 : XCircle}
          >
            {result.status === "ok" ? "The provider answered." : "The test failed."}
          </Badge>
          {result.message && <p className="font-mono text-xs break-words text-muted-foreground">{result.message}</p>}
        </div>
      )}
      <div className="flex flex-wrap items-center gap-2">
        {provider.configured && (
          <Button variant="ghost" className="hover:text-destructive" onClick={() => setRemoving(true)}>
            Remove
          </Button>
        )}
        <div className="ml-auto flex flex-wrap gap-2">
          {provider.configured && (
            <Button variant="secondary" disabled={test.isPending} onClick={() => test.mutate({})}>
              {test.isPending ? "Testing" : "Test provider"}
            </Button>
          )}
          <Button type="submit" disabled={save.isPending}>
            Save
          </Button>
        </div>
      </div>
      <ConfirmDialog
        open={removing}
        title="Remove AI provider"
        confirmLabel="Remove"
        destructive
        pending={remove.isPending}
        error={remove.isError ? errorMessage(remove.error) : undefined}
        onConfirm={() => remove.mutate({})}
        onClose={() => setRemoving(false)}
      >
        Remove the AI provider? The assistant and triage stop until an admin configures a provider again.
      </ConfirmDialog>
    </form>
  );
}
