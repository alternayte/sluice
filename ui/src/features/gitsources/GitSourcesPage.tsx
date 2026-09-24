import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ArrowRight,
  CheckCircle2,
  CircleDashed,
  GitBranch,
  Loader2,
  Plus,
  RefreshCw,
  Trash2,
  XCircle,
} from "lucide-react";
import { useState, type FormEvent } from "react";
import {
  createGitSourceMutation,
  deleteGitSourceMutation,
  listGitSourcesOptions,
  syncGitSourceMutation,
} from "@/api/@tanstack/react-query.gen";
import type { SourceOut } from "@/api/types.gen";
import { DataState } from "@/components/data-state";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, FormError } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { SegmentedControl } from "@/components/ui/segmented";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import { toast } from "@/components/ui/toast";
import { errorMessage, fieldErrors } from "@/lib/errors";
import { cn, formatTime } from "@/lib/utils";

const sourcesKey = [{ _id: "listGitSources" }];

function SyncState({ s }: { s: SourceOut }) {
  if (s.last_sync_status === "success")
    return (
      <Badge tone="success" icon={CheckCircle2}>
        Synced
      </Badge>
    );
  if (s.last_sync_status === "failed")
    return (
      <Badge tone="failed" icon={XCircle}>
        Failed
      </Badge>
    );
  if (s.last_sync_status === "running")
    return (
      <Badge tone="accent" icon={Loader2} pulse>
        Running
      </Badge>
    );
  return <Badge icon={CircleDashed}>Not synced</Badge>;
}

/** GitSourcesPage manages git sources and their mappings (REQ-GIT-001, REQ-UI-009). */
export function GitSourcesPage() {
  const qc = useQueryClient();
  const sources = useQuery({ ...listGitSourcesOptions(), refetchInterval: 3000, select: (d) => d.items });
  const [creating, setCreating] = useState(false);
  const [deleting, setDeleting] = useState<SourceOut | null>(null);
  const sync = useMutation({
    ...syncGitSourceMutation(),
    onSuccess: () => {
      toast({ title: "Sync started", tone: "info" });
      void qc.invalidateQueries({ queryKey: sourcesKey });
    },
  });
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Git sources"
        description="Each mapping makes a directory of a repository a read-only namespace. Credentials are global secret keys."
        actions={
          <Button onClick={() => setCreating(true)}>
            <Plus className="h-4 w-4" aria-hidden />
            Add git source
          </Button>
        }
      />
      {sync.isError && <FormError>{errorMessage(sync.error)}</FormError>}
      <DataState query={sources} empty={(d) => d.length === 0} emptyText="No git sources exist." emptyIcon={GitBranch}>
        {(items) => (
          <Table>
            <THead>
              <Tr>
                <Th>Name</Th>
                <Th>Repository</Th>
                <Th>Mappings</Th>
                <Th>State</Th>
                <Th>Last sync</Th>
                <Th>Webhook URL</Th>
                <Th className="relative">
                  <span className="sr-only">Actions</span>
                </Th>
              </Tr>
            </THead>
            <TBody>
              {items.map((s) => (
                <Tr key={s.id}>
                  <Td className="font-medium">{s.name}</Td>
                  <Td className="max-w-72" title={`${s.repo_url} (${s.branch})`}>
                    <div className="flex min-w-0 items-center gap-2">
                      <span className="truncate font-mono text-xs">{s.repo_url}</span>
                      <span className="inline-flex shrink-0 items-center gap-1 rounded-inner bg-muted px-1.5 py-px font-mono text-xs text-muted-foreground">
                        <GitBranch className="h-3 w-3" aria-hidden />
                        {s.branch}
                      </span>
                    </div>
                  </Td>
                  <Td className="py-1.5 text-xs">
                    {s.mappings.map((m) => (
                      <div key={m.namespace} className="flex items-center gap-1.5">
                        <span className="font-mono text-muted-foreground">{m.repo_path || "/"}</span>
                        <ArrowRight className="h-3 w-3 text-muted-foreground" aria-hidden />
                        <span className="sr-only">to</span>
                        <span className="font-medium">{m.namespace}</span>
                      </div>
                    ))}
                  </Td>
                  <Td className="py-1.5">
                    <SyncState s={s} />
                    {s.last_error && (
                      <div className="max-w-48 truncate text-xs text-muted-foreground" title={s.last_error}>
                        {s.last_error}
                      </div>
                    )}
                  </Td>
                  <Td className="text-muted-foreground tabular-nums">
                    {s.last_sync_at ? formatTime(s.last_sync_at) : "Never"}
                  </Td>
                  <Td
                    className={cn(
                      "max-w-48 truncate text-xs",
                      s.webhook_secret_key ? "font-mono" : "text-muted-foreground",
                    )}
                    title={s.webhook_url}
                  >
                    {s.webhook_secret_key ? s.webhook_url : "No webhook secret"}
                  </Td>
                  <Td className="text-right whitespace-nowrap">
                    <div className="flex justify-end gap-1">
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={s.last_sync_status === "running"}
                        onClick={() => sync.mutate({ path: { sourceId: s.id } })}
                        aria-label={`Sync ${s.name} now`}
                      >
                        <RefreshCw className="h-3.5 w-3.5" aria-hidden />
                        Sync now
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        className="w-7 px-0 hover:text-destructive"
                        onClick={() => setDeleting(s)}
                        aria-label={`Delete ${s.name}`}
                        title="Delete"
                      >
                        <Trash2 className="h-3.5 w-3.5" aria-hidden />
                      </Button>
                    </div>
                  </Td>
                </Tr>
              ))}
            </TBody>
          </Table>
        )}
      </DataState>
      <Dialog open={creating} onClose={() => setCreating(false)} title="Add git source" size="lg">
        {creating && <SourceForm onDone={() => setCreating(false)} />}
      </Dialog>
      <Dialog open={deleting !== null} onClose={() => setDeleting(null)} title="Delete git source">
        {deleting && <SourceDelete source={deleting} onDone={() => setDeleting(null)} />}
      </Dialog>
    </div>
  );
}

type MappingRow = { repo_path: string; namespace: string };

function SourceForm({ onDone }: { onDone: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [repoURL, setRepoURL] = useState("");
  const [branch, setBranch] = useState("main");
  const [authType, setAuthType] = useState<"none" | "https_token" | "ssh_key">("https_token");
  const [credential, setCredential] = useState("");
  const [knownHosts, setKnownHosts] = useState("");
  const [poll, setPoll] = useState("60");
  const [webhookKey, setWebhookKey] = useState("");
  const [mappings, setMappings] = useState<MappingRow[]>([{ repo_path: "", namespace: "" }]);
  const mutation = useMutation({
    ...createGitSourceMutation(),
    onSuccess: (src) => {
      void qc.invalidateQueries({ queryKey: sourcesKey });
      toast({ title: "Git source added", description: `${src.name} syncs now.` });
      onDone();
    },
  });
  const fields = fieldErrors(mutation.error);
  const setMapping = (i: number, m: Partial<MappingRow>) =>
    setMappings(mappings.map((row, j) => (j === i ? { ...row, ...m } : row)));
  const submit = (e: FormEvent) => {
    e.preventDefault();
    mutation.mutate({
      body: {
        name,
        repo_url: repoURL,
        branch,
        auth_type: authType,
        credential_secret_key: authType === "none" ? undefined : credential,
        known_hosts: authType === "ssh_key" && knownHosts ? knownHosts : undefined,
        poll_interval: Number(poll),
        webhook_secret_key: webhookKey || undefined,
        mappings,
      },
    });
  };
  return (
    <form onSubmit={submit} className="flex flex-col gap-4">
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Field id="git-name" label="Name" error={fields.name}>
          <Input
            required
            pattern="[a-z0-9][a-z0-9_\-]*"
            maxLength={63}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        <Field id="git-branch" label="Branch" error={fields.branch}>
          <Input required value={branch} onChange={(e) => setBranch(e.target.value)} className="font-mono" />
        </Field>
      </div>
      <Field id="git-url" label="Repository URL" hint="https://… or ssh://git@host/repo.git" error={fields.repo_url}>
        <Input required value={repoURL} onChange={(e) => setRepoURL(e.target.value)} className="font-mono" />
      </Field>
      <div className="flex flex-col gap-1.5">
        <span className="text-sm font-medium">Authentication</span>
        <SegmentedControl
          label="Authentication"
          options={[
            { value: "https_token", label: "HTTPS token" },
            { value: "ssh_key", label: "SSH key" },
            { value: "none", label: "None" },
          ]}
          value={authType}
          onChange={setAuthType}
        />
        {fields.auth_type && <p className="text-xs text-destructive">{fields.auth_type}</p>}
      </div>
      {authType !== "none" && (
        <Field
          id="git-credential"
          label="Credential secret key"
          hint="A global secret with the token or the private key."
          error={fields.credential_secret_key}
        >
          <Input required value={credential} onChange={(e) => setCredential(e.target.value)} className="font-mono" />
        </Field>
      )}
      {authType === "ssh_key" && (
        <Field
          id="git-known-hosts"
          label="Known hosts"
          hint="Optional. Without it the host key is not checked."
          error={fields.known_hosts}
        >
          <Input value={knownHosts} onChange={(e) => setKnownHosts(e.target.value)} className="font-mono" />
        </Field>
      )}
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Field id="git-poll" label="Poll interval in seconds" error={fields.poll_interval}>
          <Input type="number" min={15} max={86400} required value={poll} onChange={(e) => setPoll(e.target.value)} />
        </Field>
        <Field
          id="git-webhook"
          label="Webhook secret key"
          hint="Optional. A global secret for webhook signatures."
          error={fields.webhook_secret_key}
        >
          <Input value={webhookKey} onChange={(e) => setWebhookKey(e.target.value)} className="font-mono" />
        </Field>
      </div>
      <fieldset className="flex flex-col">
        <legend className="mb-1.5 text-sm font-medium">Mappings</legend>
        <div className="flex flex-col divide-y rounded-panel border bg-muted/40">
          {mappings.map((m, i) => (
            <div key={i} className="grid grid-cols-1 gap-2 p-3 sm:grid-cols-2">
              <Field id={`git-map-path-${i}`} label="Repository path" hint={i === 0 ? "Empty is the root." : undefined}>
                <Input
                  value={m.repo_path}
                  onChange={(e) => setMapping(i, { repo_path: e.target.value })}
                  className="font-mono"
                />
              </Field>
              <Field id={`git-map-ns-${i}`} label="Namespace">
                <Input
                  required
                  value={m.namespace}
                  onChange={(e) => setMapping(i, { namespace: e.target.value })}
                  className="font-mono"
                />
              </Field>
            </div>
          ))}
          <div className="px-1.5 py-1.5">
            <Button
              size="sm"
              variant="ghost"
              onClick={() => setMappings([...mappings, { repo_path: "", namespace: "" }])}
            >
              <Plus className="h-3.5 w-3.5" aria-hidden />
              Add mapping
            </Button>
          </div>
        </div>
      </fieldset>
      {mutation.isError && <FormError>{errorMessage(mutation.error)}</FormError>}
      <div className="flex justify-end gap-2">
        <Button variant="secondary" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={mutation.isPending}>
          Add git source
        </Button>
      </div>
    </form>
  );
}

function SourceDelete({ source, onDone }: { source: SourceOut; onDone: () => void }) {
  const qc = useQueryClient();
  const mutation = useMutation({
    ...deleteGitSourceMutation(),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: sourcesKey });
      toast({ title: "Git source deleted", description: source.name });
      onDone();
    },
  });
  return (
    <div className="flex flex-col gap-3">
      <p className="text-sm">
        Delete the git source {source.name}? Its namespaces stay read-only with their history, and they no longer sync.
      </p>
      {mutation.isError && <FormError>{errorMessage(mutation.error)}</FormError>}
      <div className="flex justify-end gap-2">
        <Button variant="secondary" onClick={onDone}>
          Cancel
        </Button>
        <Button
          variant="destructive"
          onClick={() => mutation.mutate({ path: { sourceId: source.id } })}
          disabled={mutation.isPending}
        >
          Delete
        </Button>
      </div>
    </div>
  );
}
