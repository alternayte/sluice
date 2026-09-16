import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, XCircle } from "lucide-react";
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
import { Field, FormError } from "@/components/ui/field";
import { Input, Select } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { errorMessage, fieldErrors } from "@/lib/errors";

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
      <DataState query={provider}>{(p) => <ProviderForm key={JSON.stringify(p)} provider={p} />}</DataState>
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
  const save = useMutation({ ...putAiProviderMutation(), onSuccess: refresh });
  const test = useMutation(testAiProviderMutation());
  const remove = useMutation({
    ...deleteAiProviderMutation(),
    onSuccess: () => {
      setRemoving(false);
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
    <form onSubmit={submit} className="flex max-w-xl flex-col gap-3 rounded-[8px] border bg-panel p-4">
      <p className="text-sm" role="status">
        {provider.configured ? "A provider is configured." : "No provider is configured. AI actions are hidden."}
      </p>
      <Field id="ai-type" label="Type" error={fields.type}>
        <Select value={type} onChange={(e) => setType(e.target.value as ProviderType)}>
          <option value="anthropic">Anthropic</option>
          <option value="openai_compatible">OpenAI compatible</option>
        </Select>
      </Field>
      <Field
        id="ai-base-url"
        label="Base URL"
        hint={type === "anthropic" ? "Empty uses https://api.anthropic.com." : "Empty uses https://api.openai.com/v1."}
        error={fields.base_url}
      >
        <Input value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} className="font-mono" />
      </Field>
      <Field id="ai-model" label="Model" error={fields.model}>
        <Input required value={model} onChange={(e) => setModel(e.target.value)} className="font-mono" />
      </Field>
      <Field
        id="ai-key"
        label="API key secret key"
        hint="A global secret that holds the API key."
        error={fields.api_key_secret_key}
      >
        <Input required value={keyName} onChange={(e) => setKeyName(e.target.value)} className="font-mono" />
      </Field>
      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          checked={autoTriage}
          onChange={(e) => setAutoTriage(e.target.checked)}
          className="h-4 w-4"
        />
        Triage failed and timed out executions automatically
      </label>
      {save.isError && <FormError>{errorMessage(save.error)}</FormError>}
      {test.isError && <FormError>{errorMessage(test.error)}</FormError>}
      {result && (
        <div role="status" className="flex flex-col gap-1">
          <Badge
            tone={result.status === "ok" ? "success" : "failed"}
            icon={result.status === "ok" ? CheckCircle2 : XCircle}
          >
            {result.status === "ok" ? "The provider answered." : "The test failed."}
          </Badge>
          {result.message && <p className="text-xs break-words text-muted-foreground">{result.message}</p>}
        </div>
      )}
      <div className="flex flex-wrap justify-end gap-2">
        {provider.configured && (
          <>
            <Button variant="secondary" onClick={() => setRemoving(true)}>
              Remove
            </Button>
            <Button variant="secondary" disabled={test.isPending} onClick={() => test.mutate({})}>
              Test provider
            </Button>
          </>
        )}
        <Button type="submit" disabled={save.isPending}>
          Save
        </Button>
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
