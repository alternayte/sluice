import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import {
  Check,
  CheckCircle2,
  ChevronRight,
  CircleOff,
  Copy,
  FileCode2,
  FolderTree,
  GitBranch,
  GitCompare,
  History,
  KeyRound,
  Play,
  Zap,
} from "lucide-react";
import { useCallback, useMemo, useState } from "react";
import {
  diffFlowRevisionsOptions,
  getFlowOptions,
  getFlowQueryKey,
  getFlowRevisionOptions,
  getNamespaceGitOptions,
  listExecutionsOptions,
  listFlowRevisionsOptions,
  listFlowsQueryKey,
  rotateWebhookKeyMutation,
  updateFlowMutation,
} from "@/api/@tanstack/react-query.gen";
import type { FlowDetail, Issue, RevisionSummary, WebhookKey } from "@/api/types.gen";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DataState, EmptyState } from "@/components/data-state";
import { DiffView } from "@/components/diff-view";
import { CodeEditor } from "@/components/editor/code-editor";
import { CompactExecutionTable } from "@/components/execution-bits";
import { FlowCharts } from "@/features/flows/FlowCharts";
import { RunFlowDialog } from "@/components/run-dialogs";
import { DisabledBadge, ValidBadge } from "@/components/state-badges";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { Dialog } from "@/components/ui/dialog";
import { Field, FormError } from "@/components/ui/field";
import { Input, Select } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import { Tabs } from "@/components/ui/tabs";
import { toast } from "@/components/ui/toast";
import { useCurrentUser } from "@/lib/auth";
import { useCommands, type Command } from "@/lib/commands";
import { errorMessage } from "@/lib/errors";
import { triggerSummary } from "@/lib/flows";
import { commitUrl } from "@/lib/git";
import { can } from "@/lib/roles";
import { cn, formatRelative, formatTime } from "@/lib/utils";

export type Tab = "overview" | "executions" | "triggers" | "source" | "revisions";
/** FlowSearch selects a tab. revision shows the source of one flow revision, for example the one an execution ran. */
export type FlowSearch = { tab?: Exclude<Tab, "overview">; revision?: string };

const tabs: { value: Tab; label: string }[] = [
  { value: "overview", label: "Overview" },
  { value: "executions", label: "Executions" },
  { value: "triggers", label: "Triggers" },
  { value: "source", label: "Source" },
  { value: "revisions", label: "Revisions" },
];

export function FlowPage({
  namespace,
  flowId,
  search,
  navigate,
}: {
  namespace: string;
  flowId: string;
  search: FlowSearch;
  navigate: (opts: { search: FlowSearch }) => void;
}) {
  const me = useCurrentUser();
  const flow = useQuery(getFlowOptions({ path: { namespace, flowId } }));
  const tab: Tab = search.tab ?? "overview";
  const [runOpen, setRunOpen] = useState(false);
  const openRun = useCallback(() => setRunOpen(true), []);
  const canRun = can(me.role, "operator") && !!flow.data?.valid && !!flow.data.revision;
  useFlowCommands({ namespace, flowId, path: flow.data?.path, canRun, onRun: openRun, navigate });

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <nav aria-label="Breadcrumb" className="flex items-center gap-1 text-xs text-muted-foreground">
          <Link to="/flows" className="hover:text-foreground hover:underline">
            Flows
          </Link>
          <ChevronRight className="h-3 w-3" aria-hidden />
          <Link
            to="/namespaces/$namespace"
            params={{ namespace }}
            className="font-mono hover:text-foreground hover:underline"
          >
            {namespace}
          </Link>
          <ChevronRight className="h-3 w-3" aria-hidden />
          <span className="font-mono text-foreground">{flowId}</span>
        </nav>
        <GitChip namespace={namespace} sha={flow.data?.revision?.git_sha} />
      </div>
      <DataState query={flow} skeleton="panel">
        {(f) => (
          <>
            <PageHeader
              title={f.flow_id}
              description={f.description || undefined}
              actions={
                <>
                  {can(me.role, "editor") ? (
                    <EnableToggle flow={f} />
                  ) : f.disabled ? (
                    <DisabledBadge />
                  ) : (
                    <Badge tone="success" icon={CheckCircle2}>
                      Enabled
                    </Badge>
                  )}
                  {canRun && (
                    <Button onClick={() => setRunOpen(true)}>
                      <Play className="h-3.5 w-3.5" aria-hidden />
                      Run
                    </Button>
                  )}
                </>
              }
            />
            {runOpen && (
              <RunFlowDialog
                namespace={namespace}
                flowId={flowId}
                definition={f.revision?.definition}
                onClose={() => setRunOpen(false)}
              />
            )}
            <Tabs
              label="Flow sections"
              tabs={tabs}
              value={tab}
              onChange={(t) => navigate({ search: t === "overview" ? {} : { tab: t } })}
            />
            {tab === "overview" && (
              <>
                <Overview flow={f} />
                <FlowCharts namespace={namespace} flowId={flowId} />
              </>
            )}
            {tab === "executions" && <FlowExecutions namespace={namespace} flowId={flowId} />}
            {tab === "triggers" && <Triggers flow={f} />}
            {tab === "source" &&
              (search.revision && search.revision !== f.revision?.id ? (
                <RevisionSource
                  namespace={namespace}
                  flowId={flowId}
                  revisionId={search.revision}
                  onCurrent={() => navigate({ search: { tab: "source" } })}
                />
              ) : f.revision ? (
                <CodeEditor
                  className="h-[65vh] min-h-80"
                  value={f.revision.source}
                  path="source.yaml"
                  label={`Source of ${f.path}`}
                  readOnly
                />
              ) : (
                <EmptyState icon={FileCode2} text="This flow has no current revision." />
              ))}
            {tab === "revisions" && <Revisions namespace={namespace} flowId={flowId} />}
          </>
        )}
      </DataState>
    </div>
  );
}

/** useFlowCommands registers the palette commands of a flow page. */
function useFlowCommands({
  namespace,
  flowId,
  path,
  canRun,
  onRun,
  navigate,
}: {
  namespace: string;
  flowId: string;
  path: string | undefined;
  canRun: boolean;
  onRun: () => void;
  navigate: (opts: { search: FlowSearch }) => void;
}) {
  const go = useNavigate();
  const commands = useMemo<Command[]>(() => {
    const group = "This flow";
    const hint = `${namespace}/${flowId}`;
    const out: Command[] = [];
    if (canRun)
      out.push({ id: "flow-run", group, label: "Run this flow", icon: Play, hint, keywords: "start", run: onRun });
    out.push(
      {
        id: "flow-namespace",
        group,
        label: "Open namespace",
        icon: FolderTree,
        hint: namespace,
        run: () => void go({ to: "/namespaces/$namespace", params: { namespace } }),
      },
      {
        id: "flow-executions",
        group,
        label: "Show executions of this flow",
        icon: History,
        run: () => navigate({ search: { tab: "executions" } }),
      },
      {
        id: "flow-triggers",
        group,
        label: "Show triggers",
        icon: Zap,
        run: () => navigate({ search: { tab: "triggers" } }),
      },
      {
        id: "flow-revisions",
        group,
        label: "Compare revisions",
        icon: GitCompare,
        keywords: "diff history",
        run: () => navigate({ search: { tab: "revisions" } }),
      },
    );
    if (path) {
      out.push({
        id: "flow-file",
        group,
        label: "Open flow file",
        icon: FileCode2,
        hint: path,
        keywords: "edit source yaml",
        run: () => void go({ to: "/namespaces/$namespace", params: { namespace }, search: { file: path } }),
      });
    }
    return out;
  }, [namespace, flowId, path, canRun, onRun, navigate, go]);
  useCommands(commands);
}

function FlowExecutions({ namespace, flowId }: { namespace: string; flowId: string }) {
  const executions = useQuery({
    ...listExecutionsOptions({ query: { flow: `${namespace}/${flowId}`, limit: 50 } }),
    // REQ-UI-012 allows 2 s for a state change. A 1 s poll keeps the list inside that limit.
    refetchInterval: 1000,
  });
  return (
    <DataState
      query={executions}
      empty={(d) => d.items.length === 0}
      emptyText="This flow has no executions."
      emptyIcon={History}
    >
      {(d) => <CompactExecutionTable items={d.items} />}
    </DataState>
  );
}

function EnableToggle({ flow }: { flow: FlowDetail }) {
  const qc = useQueryClient();
  const update = useMutation({
    ...updateFlowMutation(),
    onSuccess: (d) => {
      qc.setQueryData(getFlowQueryKey({ path: { namespace: flow.namespace, flowId: flow.flow_id } }), d);
      void qc.invalidateQueries({ queryKey: listFlowsQueryKey() });
      toast({
        title: d.disabled ? `${d.flow_id} disabled` : `${d.flow_id} enabled`,
        description: d.disabled ? "Its triggers do not start executions." : undefined,
        tone: d.disabled ? "info" : "success",
      });
    },
  });
  const enabled = !flow.disabled;
  return (
    <div className="flex flex-col items-end gap-1">
      <Switch
        checked={enabled}
        disabled={update.isPending}
        onChange={(next) =>
          update.mutate({ path: { namespace: flow.namespace, flowId: flow.flow_id }, body: { disabled: !next } })
        }
      >
        {enabled ? "Enabled" : "Disabled"}
      </Switch>
      {update.isError && <FormError>{errorMessage(update.error)}</FormError>}
    </div>
  );
}

// Inspector rows: a hairline between rows; on narrow screens the label sits above its value.
const dt = "pt-2.5 text-muted-foreground sm:border-b sm:py-2.5";
const dd = "min-w-0 border-b pb-2.5 sm:py-2.5";

function Overview({ flow }: { flow: FlowDetail }) {
  const labels = Object.entries(flow.labels ?? {});
  const errors: Issue[] = flow.revision?.errors ?? [];
  return (
    <div className="flex flex-col gap-4">
      <dl className="grid grid-cols-1 rounded-panel border bg-panel px-4 py-1 text-sm shadow-panel sm:grid-cols-[9rem_minmax(0,1fr)]">
        <dt className={dt}>Description</dt>
        <dd className={dd}>{flow.description || "—"}</dd>
        <dt className={dt}>Path</dt>
        <dd className={cn(dd, "font-mono text-xs break-all")}>{flow.path}</dd>
        <dt className={dt}>Validity</dt>
        <dd className={cn(dd, "flex flex-wrap items-center gap-3")}>
          <ValidBadge valid={flow.valid} />
          {flow.disabled && <DisabledBadge />}
        </dd>
        <dt className={dt}>Revision</dt>
        <dd className={cn(dd, "text-muted-foreground tabular-nums")}>
          {flow.revision ? (
            <span title={formatTime(flow.revision.created_at)}>Updated {formatRelative(flow.revision.created_at)}</span>
          ) : (
            "—"
          )}
        </dd>
        <dt className={cn(dt, "sm:border-b-0")}>Labels</dt>
        <dd className={cn(dd, "flex flex-wrap gap-1.5 border-b-0")}>
          {labels.length === 0
            ? "—"
            : labels.map(([k, v]) => (
                <span
                  key={k}
                  className="rounded-control bg-muted px-1.5 font-mono text-xs leading-5 shadow-[inset_0_0_0_0.5px_var(--border)]"
                >
                  {k}={v}
                </span>
              ))}
        </dd>
      </dl>
      {!flow.valid && (
        <section aria-labelledby="flow-errors" className="flex flex-col gap-2">
          <h2 id="flow-errors" className="text-sm font-semibold">
            Errors
          </h2>
          {errors.length === 0 ? (
            <EmptyState icon={CircleOff} text="The flow is invalid. The server sent no error details." />
          ) : (
            <Table>
              <THead>
                <Tr>
                  <Th>Line</Th>
                  <Th>Column</Th>
                  <Th>Code</Th>
                  <Th>Message</Th>
                </Tr>
              </THead>
              <TBody>
                {errors.map((e, i) => (
                  <Tr key={i}>
                    <Td className="font-mono text-xs">{e.line}</Td>
                    <Td className="font-mono text-xs">{e.column}</Td>
                    <Td className="font-mono text-xs">{e.code}</Td>
                    <Td className="whitespace-normal">{e.message}</Td>
                  </Tr>
                ))}
              </TBody>
            </Table>
          )}
        </section>
      )}
    </div>
  );
}

function Triggers({ flow }: { flow: FlowDetail }) {
  const me = useCurrentUser();
  const canRotate = can(me.role, "editor");
  const [rotate, setRotate] = useState<string | null>(null);
  if (flow.triggers.length === 0) {
    return <EmptyState icon={Zap} text="This flow has no triggers." />;
  }
  return (
    <>
      <Table>
        <THead>
          <Tr>
            <Th>Key</Th>
            <Th>Type</Th>
            <Th>State</Th>
            <Th>Config</Th>
            <Th>Next fire time</Th>
            {canRotate && (
              <Th>
                <span className="sr-only">Actions</span>
              </Th>
            )}
          </Tr>
        </THead>
        <TBody>
          {flow.triggers.map((t) => (
            <Tr key={t.key}>
              <Td className="font-mono text-xs">{t.key}</Td>
              <Td className="text-muted-foreground">{t.type.charAt(0).toUpperCase() + t.type.slice(1)}</Td>
              <Td>
                {t.active ? (
                  <Badge tone="success" icon={CheckCircle2}>
                    Active
                  </Badge>
                ) : (
                  <Badge tone="neutral" icon={CircleOff}>
                    Inactive
                  </Badge>
                )}
              </Td>
              <Td className="font-mono text-xs">{triggerSummary(t.type, t.config) || "—"}</Td>
              <Td className="tabular-nums">{formatTime(t.next_fire_at)}</Td>
              {canRotate && (
                <Td className="text-right">
                  {t.type === "webhook" && (
                    <Button
                      variant="secondary"
                      size="sm"
                      aria-label={`Rotate key of ${t.key}`}
                      onClick={() => setRotate(t.key)}
                    >
                      <KeyRound className="h-3.5 w-3.5" aria-hidden />
                      Rotate key
                    </Button>
                  )}
                </Td>
              )}
            </Tr>
          ))}
        </TBody>
      </Table>
      {rotate && <RotateWebhookKey flow={flow} triggerKey={rotate} onClose={() => setRotate(null)} />}
    </>
  );
}

/** RotateWebhookKey confirms a key rotation, then shows the new webhook URL once (DI-28). */
function RotateWebhookKey({
  flow,
  triggerKey,
  onClose,
}: {
  flow: FlowDetail;
  triggerKey: string;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const [created, setCreated] = useState<WebhookKey | null>(null);
  const [copied, setCopied] = useState(false);
  const mutation = useMutation({
    ...rotateWebhookKeyMutation(),
    onSuccess: (d) => {
      setCreated(d);
      void qc.invalidateQueries({
        queryKey: getFlowQueryKey({ path: { namespace: flow.namespace, flowId: flow.flow_id } }),
      });
    },
  });

  if (!created) {
    return (
      <ConfirmDialog
        open
        title="Rotate webhook key"
        confirmLabel="Rotate key"
        destructive
        pending={mutation.isPending}
        error={mutation.isError ? errorMessage(mutation.error) : undefined}
        onConfirm={() =>
          mutation.mutate({ path: { namespace: flow.namespace, flowId: flow.flow_id, triggerId: triggerKey } })
        }
        onClose={onClose}
      >
        <p>
          Sluice makes a new key for the trigger <span className="font-mono">{triggerKey}</span>. The old key stops
          working immediately. Callers of the old URL get 404.
        </p>
      </ConfirmDialog>
    );
  }

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(created.url);
      setCopied(true);
    } catch {
      setCopied(false);
      toast({ tone: "error", title: "Could not copy the URL", description: "Select the URL and copy it by hand." });
    }
  };
  return (
    <Dialog open onClose={onClose} title="New webhook URL">
      <p className="text-sm text-muted-foreground">Copy this URL now. It is not shown again.</p>
      <div className="flex gap-2">
        <Input
          aria-label="Webhook URL"
          readOnly
          value={created.url}
          className="font-mono text-xs"
          onFocus={(e) => e.currentTarget.select()}
        />
        <Button variant="secondary" onClick={() => void copy()}>
          {copied ? (
            <Check className="h-3.5 w-3.5 text-state-success" aria-hidden />
          ) : (
            <Copy className="h-3.5 w-3.5" aria-hidden />
          )}
          {copied ? "Copied" : "Copy"}
        </Button>
      </div>
      <div className="flex justify-end">
        <Button onClick={onClose}>Done</Button>
      </div>
    </Dialog>
  );
}

/** GitChip names the git source and the commit of a git flow, and links to the source for admins. */
function GitChip({ namespace, sha }: { namespace: string; sha?: string }) {
  const me = useCurrentUser();
  const git = useQuery({ ...getNamespaceGitOptions({ path: { namespace } }), retry: false });
  if (!git.data) return null;
  const g = git.data;
  const commit = sha || g.last_synced_sha;
  const href = commitUrl(g.repo_url, commit);
  const chip = "inline-flex items-center gap-1.5 rounded-full border bg-panel px-2.5 py-0.5 text-xs";
  return (
    <span className={chip} title={`${g.repo_url} (${g.branch})`}>
      <GitBranch className="h-3 w-3 text-muted-foreground" aria-hidden />
      {can(me.role, "admin") ? (
        <Link
          to="/settings/git"
          search={{ edit: g.source_id }}
          className="font-medium hover:text-accent-text hover:underline"
        >
          {g.source_name}
        </Link>
      ) : (
        <span className="font-medium">{g.source_name}</span>
      )}
      {commit &&
        (href ? (
          <a
            href={href}
            target="_blank"
            rel="noreferrer"
            className="font-mono text-muted-foreground hover:text-accent-text hover:underline"
          >
            {commit.slice(0, 12)}
          </a>
        ) : (
          <span className="font-mono text-muted-foreground">{commit.slice(0, 12)}</span>
        ))}
    </span>
  );
}

/** RevisionSource shows the source of one flow revision that is not the current one. */
function RevisionSource({
  namespace,
  flowId,
  revisionId,
  onCurrent,
}: {
  namespace: string;
  flowId: string;
  revisionId: string;
  onCurrent: () => void;
}) {
  const rev = useQuery(getFlowRevisionOptions({ path: { namespace, flowId, revisionId } }));
  return (
    <DataState query={rev} skeleton="panel">
      {(r) => (
        <div className="flex flex-col gap-2">
          <div className="flex flex-wrap items-center justify-between gap-2 rounded-panel border bg-muted/40 px-3 py-2 text-sm">
            <span>
              Revision <span className="font-mono">{revisionLabel(r)}</span>. This is not the current revision.
            </span>
            <Button size="sm" variant="secondary" onClick={onCurrent}>
              Show current revision
            </Button>
          </div>
          <CodeEditor
            className="h-[65vh] min-h-80"
            value={r.source}
            path="source.yaml"
            label={`Source of ${r.path} at ${revisionLabel(r)}`}
            readOnly
          />
        </div>
      )}
    </DataState>
  );
}

function revisionLabel(r: Pick<RevisionSummary, "id" | "created_at" | "git_sha" | "snapshot_version">): string {
  const v =
    r.snapshot_version != null ? `v${r.snapshot_version}` : r.git_sha ? r.git_sha.slice(0, 12) : r.id.slice(0, 8);
  return `${v}, ${formatTime(r.created_at)}`;
}

function Revisions({ namespace, flowId }: { namespace: string; flowId: string }) {
  const revisions = useQuery(listFlowRevisionsOptions({ path: { namespace, flowId }, query: { limit: 100 } }));
  return (
    <DataState
      query={revisions}
      empty={(d) => d.items.length === 0}
      emptyText="This flow has no revisions."
      emptyIcon={History}
    >
      {(d) => (
        <div className="flex flex-col gap-6">
          <Table>
            <THead>
              <Tr>
                <Th>Time</Th>
                <Th>Version</Th>
                <Th>Message</Th>
                <Th>State</Th>
              </Tr>
            </THead>
            <TBody>
              {d.items.map((r) => (
                <Tr key={r.id}>
                  <Td className="tabular-nums">{formatTime(r.created_at)}</Td>
                  <Td className="font-mono text-xs">
                    {r.snapshot_version != null ? `v${r.snapshot_version}` : (r.git_sha ?? "").slice(0, 12) || "—"}
                  </Td>
                  <Td className="max-w-96 truncate text-muted-foreground" title={r.message}>
                    {r.message || "—"}
                  </Td>
                  <Td>
                    <ValidBadge valid={r.valid} />
                  </Td>
                </Tr>
              ))}
            </TBody>
          </Table>
          <RevisionDiff namespace={namespace} flowId={flowId} revisions={d.items} />
        </div>
      )}
    </DataState>
  );
}

function RevisionDiff({
  namespace,
  flowId,
  revisions,
}: {
  namespace: string;
  flowId: string;
  revisions: RevisionSummary[];
}) {
  const [from, setFrom] = useState<string | undefined>(revisions[1]?.id);
  const [to, setTo] = useState<string | undefined>(revisions[0]?.id);
  const enabled = from !== undefined && to !== undefined && from !== to;
  const diff = useQuery({
    ...diffFlowRevisionsOptions({ path: { namespace, flowId }, query: { from: from ?? "", to: to ?? "" } }),
    enabled,
  });

  return (
    <section aria-labelledby="rev-diff-heading" className="flex flex-col gap-3">
      <h2 id="rev-diff-heading" className="text-sm font-semibold">
        Compare revisions
      </h2>
      {revisions.length < 2 ? (
        <EmptyState icon={GitCompare} text="Two revisions are necessary to show a diff." />
      ) : (
        <>
          <div className="grid max-w-2xl grid-cols-1 items-end gap-3 sm:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)]">
            <Field id="rev-from" label="From">
              <Select value={from ?? ""} onChange={(e) => setFrom(e.target.value)}>
                {revisions.map((r) => (
                  <option key={r.id} value={r.id}>
                    {revisionLabel(r)}
                  </option>
                ))}
              </Select>
            </Field>
            <ChevronRight className="mb-2 hidden h-4 w-4 text-muted-foreground sm:block" aria-hidden />
            <Field id="rev-to" label="To">
              <Select value={to ?? ""} onChange={(e) => setTo(e.target.value)}>
                {revisions.map((r) => (
                  <option key={r.id} value={r.id}>
                    {revisionLabel(r)}
                  </option>
                ))}
              </Select>
            </Field>
          </div>
          {!enabled ? (
            <EmptyState icon={GitCompare} text="Select two different revisions." className="py-6" />
          ) : (
            <DataState query={diff}>{(d) => <DiffView text={d.diff} label="Revision diff" />}</DataState>
          )}
        </>
      )}
    </section>
  );
}
