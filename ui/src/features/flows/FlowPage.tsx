import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Check, CheckCircle2, CircleOff, Copy, KeyRound, Play } from "lucide-react";
import { useState } from "react";
import {
  diffFlowRevisionsOptions,
  getFlowOptions,
  getFlowQueryKey,
  listExecutionsOptions,
  listFlowRevisionsOptions,
  listFlowsQueryKey,
  rotateWebhookKeyMutation,
  updateFlowMutation,
} from "@/api/@tanstack/react-query.gen";
import type { FlowDetail, Issue, RevisionSummary, WebhookKey } from "@/api/types.gen";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DataState } from "@/components/data-state";
import { DiffView } from "@/components/diff-view";
import { CodeEditor } from "@/components/editor/code-editor";
import { CompactExecutionTable } from "@/components/execution-bits";
import { FlowCharts } from "@/features/flows/FlowCharts";
import { RunFlowDialog } from "@/components/run-dialogs";
import { DisabledBadge, ValidBadge } from "@/components/state-badges";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Field, FormError } from "@/components/ui/field";
import { Input, Select } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import { Tabs } from "@/components/ui/tabs";
import { useCurrentUser } from "@/lib/auth";
import { errorMessage } from "@/lib/errors";
import { triggerSummary } from "@/lib/flows";
import { can } from "@/lib/roles";
import { cn, formatTime } from "@/lib/utils";

export type Tab = "overview" | "executions" | "triggers" | "source" | "revisions";
export type FlowSearch = { tab?: Exclude<Tab, "overview"> };

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

  return (
    <div className="flex flex-col gap-4">
      <div className="text-sm text-muted-foreground">
        <Link to="/flows" search={{ namespace }} className="hover:underline">
          {namespace}
        </Link>
      </div>
      <DataState query={flow}>
        {(f) => (
          <>
            <PageHeader
              title={f.flow_id}
              description={f.description || undefined}
              actions={
                <>
                  {can(me.role, "operator") && f.valid && f.revision && (
                    <Button onClick={() => setRunOpen(true)}>
                      <Play className="h-4 w-4" aria-hidden />
                      Run
                    </Button>
                  )}
                  {can(me.role, "editor") ? (
                    <EnableToggle flow={f} />
                  ) : f.disabled ? (
                    <DisabledBadge />
                  ) : (
                    <Badge tone="success" icon={CheckCircle2}>
                      Enabled
                    </Badge>
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
              (f.revision ? (
                <CodeEditor
                  className="h-[65vh] min-h-80"
                  value={f.revision.source}
                  path="source.yaml"
                  label={`Source of ${f.path}`}
                  readOnly
                />
              ) : (
                <p className="py-6 text-sm text-muted-foreground">This flow has no current revision.</p>
              ))}
            {tab === "revisions" && <Revisions namespace={namespace} flowId={flowId} />}
          </>
        )}
      </DataState>
    </div>
  );
}

function FlowExecutions({ namespace, flowId }: { namespace: string; flowId: string }) {
  const executions = useQuery({
    ...listExecutionsOptions({ query: { flow: `${namespace}/${flowId}`, limit: 50 } }),
    // REQ-UI-012 allows 2 s for a state change. A 1 s poll keeps the list inside that limit.
    refetchInterval: 1000,
  });
  return (
    <DataState query={executions} empty={(d) => d.items.length === 0} emptyText="This flow has no executions.">
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
    },
  });
  const enabled = !flow.disabled;
  return (
    <div className="flex flex-col items-end gap-1">
      <button
        type="button"
        role="switch"
        aria-checked={enabled}
        disabled={update.isPending}
        onClick={() =>
          update.mutate({ path: { namespace: flow.namespace, flowId: flow.flow_id }, body: { disabled: enabled } })
        }
        className="inline-flex h-9 items-center gap-2 rounded-[6px] border border-input bg-panel px-3 text-sm font-medium hover:bg-muted disabled:opacity-50"
      >
        <span
          aria-hidden
          className={cn("relative h-4 w-7 rounded-full transition-colors", enabled ? "bg-state-success" : "bg-input")}
        >
          <span
            className={cn(
              "absolute top-0.5 h-3 w-3 rounded-full bg-white transition-all",
              enabled ? "left-3.5" : "left-0.5",
            )}
          />
        </span>
        {enabled ? "Enabled" : "Disabled"}
      </button>
      {update.isError && <FormError>{errorMessage(update.error)}</FormError>}
    </div>
  );
}

function Overview({ flow }: { flow: FlowDetail }) {
  const labels = Object.entries(flow.labels ?? {});
  const errors: Issue[] = flow.revision?.errors ?? [];
  return (
    <div className="flex flex-col gap-4">
      <dl className="grid grid-cols-1 gap-x-6 gap-y-3 rounded-[8px] border bg-panel p-4 text-sm sm:grid-cols-[10rem_minmax(0,1fr)]">
        <dt className="text-muted-foreground">Description</dt>
        <dd>{flow.description || "—"}</dd>
        <dt className="text-muted-foreground">Path</dt>
        <dd className="font-mono text-xs break-all">{flow.path}</dd>
        <dt className="text-muted-foreground">Validity</dt>
        <dd className="flex flex-wrap items-center gap-3">
          <ValidBadge valid={flow.valid} />
          {flow.disabled && <DisabledBadge />}
        </dd>
        <dt className="text-muted-foreground">Labels</dt>
        <dd className="flex flex-wrap gap-1.5">
          {labels.length === 0
            ? "—"
            : labels.map(([k, v]) => (
                <span key={k} className="rounded-[6px] border bg-muted px-1.5 py-0.5 font-mono text-xs">
                  {k}={v}
                </span>
              ))}
        </dd>
      </dl>
      {!flow.valid && (
        <section aria-labelledby="flow-errors" className="flex flex-col gap-2">
          <h2 id="flow-errors" className="text-base font-semibold">
            Errors
          </h2>
          {errors.length === 0 ? (
            <p className="text-sm text-muted-foreground">The flow is invalid. The server sent no error details.</p>
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
    return <p className="py-6 text-sm text-muted-foreground">This flow has no triggers.</p>;
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
              <Td>{t.type.charAt(0).toUpperCase() + t.type.slice(1)}</Td>
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
              <Td>{formatTime(t.next_fire_at)}</Td>
              {canRotate && (
                <Td className="text-right">
                  {t.type === "webhook" && (
                    <Button variant="secondary" size="sm" aria-label={`Rotate key of ${t.key}`} onClick={() => setRotate(t.key)}>
                      <KeyRound className="h-4 w-4" aria-hidden />
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
function RotateWebhookKey({ flow, triggerKey, onClose }: { flow: FlowDetail; triggerKey: string; onClose: () => void }) {
  const qc = useQueryClient();
  const [created, setCreated] = useState<WebhookKey | null>(null);
  const [copied, setCopied] = useState(false);
  const mutation = useMutation({
    ...rotateWebhookKeyMutation(),
    onSuccess: (d) => {
      setCreated(d);
      void qc.invalidateQueries({ queryKey: getFlowQueryKey({ path: { namespace: flow.namespace, flowId: flow.flow_id } }) });
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
    }
  };
  return (
    <Dialog open onClose={onClose} title="New webhook URL">
      <p className="text-sm">Copy this URL now. It is not shown again.</p>
      <div className="flex gap-2">
        <Input
          aria-label="Webhook URL"
          readOnly
          value={created.url}
          className="font-mono text-xs"
          onFocus={(e) => e.currentTarget.select()}
        />
        <Button variant="secondary" onClick={() => void copy()}>
          {copied ? <Check className="h-4 w-4" aria-hidden /> : <Copy className="h-4 w-4" aria-hidden />}
          {copied ? "Copied" : "Copy"}
        </Button>
      </div>
      <div className="flex justify-end">
        <Button onClick={onClose}>Done</Button>
      </div>
    </Dialog>
  );
}

function revisionLabel(r: RevisionSummary): string {
  const v = r.snapshot_version != null ? `v${r.snapshot_version}` : r.git_sha ? r.git_sha.slice(0, 12) : r.id.slice(0, 8);
  return `${v}, ${formatTime(r.created_at)}`;
}

function Revisions({ namespace, flowId }: { namespace: string; flowId: string }) {
  const revisions = useQuery(listFlowRevisionsOptions({ path: { namespace, flowId }, query: { limit: 100 } }));
  return (
    <DataState query={revisions} empty={(d) => d.items.length === 0} emptyText="This flow has no revisions.">
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
                  <Td>{formatTime(r.created_at)}</Td>
                  <Td className="font-mono text-xs">
                    {r.snapshot_version != null ? `v${r.snapshot_version}` : (r.git_sha ?? "").slice(0, 12) || "—"}
                  </Td>
                  <Td className="max-w-96 truncate" title={r.message}>
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
      <h2 id="rev-diff-heading" className="text-base font-semibold">
        Compare revisions
      </h2>
      {revisions.length < 2 ? (
        <p className="text-sm text-muted-foreground">Two revisions are necessary to show a diff.</p>
      ) : (
        <>
          <div className="grid max-w-2xl grid-cols-1 gap-3 sm:grid-cols-2">
            <Field id="rev-from" label="From">
              <Select value={from ?? ""} onChange={(e) => setFrom(e.target.value)}>
                {revisions.map((r) => (
                  <option key={r.id} value={r.id}>
                    {revisionLabel(r)}
                  </option>
                ))}
              </Select>
            </Field>
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
            <p className="text-sm text-muted-foreground">Select two different revisions.</p>
          ) : (
            <DataState query={diff}>{(d) => <DiffView text={d.diff} label="Revision diff" />}</DataState>
          )}
        </>
      )}
    </section>
  );
}
