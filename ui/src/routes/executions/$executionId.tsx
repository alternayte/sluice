import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { AlertTriangle, Ban, Download, RefreshCw, RotateCcw } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { api, unwrap } from "@/api/client";
import type { components } from "@/api/schema";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DataState } from "@/components/data-state";
import { LabelBadges } from "@/components/execution-bits";
import { Gantt } from "@/components/gantt";
import { LogViewer } from "@/components/log-viewer";
import { ExecutionStateBadge } from "@/components/state-badges";
import { Button } from "@/components/ui/button";
import { FormError } from "@/components/ui/field";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import { Tabs } from "@/components/ui/tabs";
import { useCurrentUser } from "@/lib/auth";
import { errorMessage } from "@/lib/errors";
import { canCancel, canRestart, compactJson, executionTitle, isTerminal } from "@/lib/executions";
import { stateLabel } from "@/lib/flows";
import { can } from "@/lib/roles";
import { formatBytes, formatDuration, formatTime } from "@/lib/utils";

type ExecutionDetail = components["schemas"]["ExecutionDetail"];
type Tab = "logs" | "outputs" | "metrics" | "artifacts";
type DetailSearch = { tab?: Exclude<Tab, "logs"> };

const tabs: { value: Tab; label: string }[] = [
  { value: "logs", label: "Logs" },
  { value: "outputs", label: "Outputs" },
  { value: "metrics", label: "Metrics" },
  { value: "artifacts", label: "Artifacts" },
];

export const Route = createFileRoute("/executions/$executionId")({
  validateSearch: (s: Record<string, unknown>): DetailSearch =>
    s.tab === "outputs" || s.tab === "metrics" || s.tab === "artifacts" ? { tab: s.tab } : {},
  component: ExecutionPage,
});

const detailKey = (id: string) => ["executions", "detail", id] as const;

/** useNow returns the current time and updates it every second while active. */
function useNow(active: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    const t = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(t);
  }, [active]);
  return now;
}

function ExecutionPage() {
  const { executionId } = Route.useParams();
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const qc = useQueryClient();
  const detail = useQuery({
    queryKey: detailKey(executionId),
    queryFn: async () =>
      unwrap(await api.GET("/api/v1/executions/{executionId}", { params: { path: { executionId } } })),
    refetchInterval: (q) => (q.state.data && isTerminal(q.state.data.state) ? false : 2000),
  });
  const live = detail.data ? !isTerminal(detail.data.state) : false;

  useEffect(() => {
    if (!live) return;
    const es = new EventSource(`/api/v1/executions/${encodeURIComponent(executionId)}/events`);
    es.addEventListener("execution", (ev) => {
      try {
        qc.setQueryData(detailKey(executionId), JSON.parse((ev as MessageEvent<string>).data) as ExecutionDetail);
      } catch {
        // The fallback refetch covers a malformed event.
      }
    });
    es.addEventListener("end", () => es.close());
    return () => es.close();
  }, [executionId, live, qc]);

  const tab: Tab = search.tab ?? "logs";

  return (
    <div className="flex min-w-0 flex-col gap-4">
      <div className="text-sm text-muted-foreground">
        <Link to="/executions" className="hover:underline">
          Executions
        </Link>
      </div>
      <DataState query={detail}>
        {(e) => (
          <>
            <Header execution={e} live={live} />
            <section aria-labelledby="timeline-heading" className="flex min-w-0 flex-col gap-2">
              <h2 id="timeline-heading" className="text-base font-semibold">
                Timeline
              </h2>
              <Timeline execution={e} live={live} />
            </section>
            <Tabs
              label="Execution sections"
              tabs={tabs}
              value={tab}
              onChange={(t) => void navigate({ search: t === "logs" ? {} : { tab: t } })}
            />
            {tab === "logs" && <LogViewer key={executionId} executionId={executionId} />}
            {tab === "outputs" && <Outputs execution={e} />}
            {tab === "metrics" && <Metrics executionId={executionId} />}
            {tab === "artifacts" && <Artifacts executionId={executionId} />}
          </>
        )}
      </DataState>
    </div>
  );
}

function Timeline({ execution, live }: { execution: ExecutionDetail; live: boolean }) {
  const now = useNow(live);
  return <Gantt runs={execution.task_runs} now={now} />;
}

function ExecLink({ id, children }: { id: string; children?: ReactNode }) {
  return (
    <Link to="/executions/$executionId" params={{ executionId: id }} className="font-mono text-xs hover:underline">
      {children ?? id.slice(0, 8)}
    </Link>
  );
}

function Header({ execution: e, live }: { execution: ExecutionDetail; live: boolean }) {
  const me = useCurrentUser();
  const now = useNow(live);
  const t = executionTitle(e);
  const started = e.started_at ? Date.parse(e.started_at) : undefined;
  const duration = e.duration_ms ?? (started !== undefined && live ? Math.max(now - started, 0) : null);
  const version = e.snapshot_version != null ? `v${e.snapshot_version}` : e.git_sha ? e.git_sha.slice(0, 12) : "—";

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex min-w-0 flex-col gap-1">
          <h1 className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xl font-semibold">
            <span className="break-all">
              {e.namespace}/{t.title}
            </span>
            <ExecutionStateBadge state={e.state} />
          </h1>
          <p className="font-mono text-xs break-all text-muted-foreground">{e.id}</p>
        </div>
        {can(me.role, "operator") && <Actions execution={e} />}
      </div>
      {e.state === "FAILED" && e.error && (
        <p role="alert" className="flex items-start gap-2 rounded-[8px] border bg-panel p-3 text-sm text-state-failed">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
          <span className="break-words whitespace-pre-wrap">{e.error}</span>
        </p>
      )}
      <dl className="grid grid-cols-1 gap-x-6 gap-y-2 rounded-[8px] border bg-panel p-4 text-sm sm:grid-cols-[10rem_minmax(0,1fr)]">
        <dt className="text-muted-foreground">Duration</dt>
        <dd className="tabular-nums">{formatDuration(duration)}</dd>
        <dt className="text-muted-foreground">Trigger</dt>
        <dd>
          {stateLabel(e.trigger_type)}
          {e.created_by && <span className="text-muted-foreground"> by {e.created_by}</span>}
        </dd>
        {t.path && (
          <>
            <dt className="text-muted-foreground">File</dt>
            <dd className="font-mono text-xs break-all">{t.path}</dd>
          </>
        )}
        <dt className="text-muted-foreground">Version</dt>
        <dd className="font-mono text-xs">{version}</dd>
        <dt className="text-muted-foreground">Created</dt>
        <dd>{formatTime(e.created_at)}</dd>
        <dt className="text-muted-foreground">Inputs</dt>
        <dd className="min-w-0">
          <code className="block font-mono text-xs break-all">{compactJson(e.inputs)}</code>
        </dd>
        <dt className="text-muted-foreground">Labels</dt>
        <dd>
          <LabelBadges labels={e.labels} />
        </dd>
        {e.parent_execution_id && (
          <>
            <dt className="text-muted-foreground">Parent execution</dt>
            <dd>
              <ExecLink id={e.parent_execution_id} />
            </dd>
          </>
        )}
        {e.restart_of_id && (
          <>
            <dt className="text-muted-foreground">Restart of</dt>
            <dd>
              <ExecLink id={e.restart_of_id} />
            </dd>
          </>
        )}
        {e.children.length > 0 && (
          <>
            <dt className="text-muted-foreground">Child executions</dt>
            <dd className="flex flex-wrap gap-x-4 gap-y-1">
              {e.children.map((c) => (
                <span key={c.id} className="flex items-center gap-1.5">
                  <ExecLink id={c.id} />
                  <ExecutionStateBadge state={c.state} />
                </span>
              ))}
            </dd>
          </>
        )}
      </dl>
    </div>
  );
}

function Actions({ execution: e }: { execution: ExecutionDetail }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [confirm, setConfirm] = useState(false);
  const path = { params: { path: { executionId: e.id } } };
  const cancel = useMutation({
    mutationFn: async () => unwrap(await api.POST("/api/v1/executions/{executionId}/cancel", path)),
    onSuccess: (d) => {
      qc.setQueryData(detailKey(e.id), d);
      setConfirm(false);
    },
  });
  const goTo = (d: ExecutionDetail) => void navigate({ to: "/executions/$executionId", params: { executionId: d.id } });
  const rerun = useMutation({
    mutationFn: async () => unwrap(await api.POST("/api/v1/executions/{executionId}/rerun", path)),
    onSuccess: goTo,
  });
  const restart = useMutation({
    mutationFn: async () => unwrap(await api.POST("/api/v1/executions/{executionId}/restart", path)),
    onSuccess: goTo,
  });
  const err = rerun.error ?? restart.error;

  return (
    <div className="flex flex-col items-end gap-1">
      <div className="flex flex-wrap gap-2">
        {canCancel(e.state) && (
          <Button variant="secondary" onClick={() => setConfirm(true)}>
            <Ban className="h-4 w-4" aria-hidden />
            Cancel
          </Button>
        )}
        <Button variant="secondary" disabled={rerun.isPending} onClick={() => rerun.mutate()}>
          <RefreshCw className="h-4 w-4" aria-hidden />
          Rerun
        </Button>
        {canRestart(e.state) && (
          <Button variant="secondary" disabled={restart.isPending} onClick={() => restart.mutate()}>
            <RotateCcw className="h-4 w-4" aria-hidden />
            Restart from failed
          </Button>
        )}
      </div>
      {err && <FormError>{errorMessage(err)}</FormError>}
      <ConfirmDialog
        open={confirm}
        title="Cancel execution"
        confirmLabel="Cancel execution"
        destructive
        pending={cancel.isPending}
        error={cancel.isError ? errorMessage(cancel.error) : undefined}
        onConfirm={() => cancel.mutate()}
        onClose={() => setConfirm(false)}
      >
        Cancel this execution? Running tasks are stopped.
      </ConfirmDialog>
    </div>
  );
}

function JsonBlock({ value }: { value: unknown }) {
  return (
    <pre className="max-h-96 overflow-auto rounded-[8px] border bg-panel p-3 font-mono text-xs">
      {JSON.stringify(value, null, 2)}
    </pre>
  );
}

function Outputs({ execution: e }: { execution: ExecutionDetail }) {
  const runs = e.task_runs.filter((r) => r.outputs && Object.keys(r.outputs).length > 0);
  const hasExec = !!e.outputs && Object.keys(e.outputs).length > 0;
  if (!hasExec && runs.length === 0) return <p className="py-6 text-sm text-muted-foreground">No outputs.</p>;
  return (
    <div className="flex min-w-0 flex-col gap-4">
      {hasExec && (
        <section className="flex flex-col gap-2">
          <h3 className="text-sm font-medium">Execution outputs</h3>
          <JsonBlock value={e.outputs} />
        </section>
      )}
      {runs.map((r) => (
        <section key={r.id} className="flex flex-col gap-2">
          <h3 className="font-mono text-sm">
            {r.task_key} #{r.attempt}
          </h3>
          <JsonBlock value={r.outputs} />
        </section>
      ))}
    </div>
  );
}

function Metrics({ executionId }: { executionId: string }) {
  const metrics = useQuery({
    queryKey: ["executions", "metrics", executionId],
    queryFn: async () =>
      unwrap(await api.GET("/api/v1/executions/{executionId}/metrics", { params: { path: { executionId } } })),
  });
  return (
    <DataState query={metrics} empty={(d) => d.items.length === 0} emptyText="No metrics.">
      {(d) => (
        <Table>
          <THead>
            <Tr>
              <Th>Task</Th>
              <Th>Name</Th>
              <Th className="text-right">Value</Th>
              <Th>Unit</Th>
              <Th>Tags</Th>
            </Tr>
          </THead>
          <TBody>
            {d.items.map((m, i) => (
              <Tr key={`${m.task_run_id}-${m.name}-${i}`}>
                <Td className="font-mono text-xs">{m.task_key}</Td>
                <Td>{m.name}</Td>
                <Td className="text-right tabular-nums">{m.value}</Td>
                <Td>{m.unit || "—"}</Td>
                <Td>
                  <LabelBadges labels={m.tags} />
                </Td>
              </Tr>
            ))}
          </TBody>
        </Table>
      )}
    </DataState>
  );
}

function Artifacts({ executionId }: { executionId: string }) {
  const artifacts = useQuery({
    queryKey: ["executions", "artifacts", executionId],
    queryFn: async () =>
      unwrap(await api.GET("/api/v1/executions/{executionId}/artifacts", { params: { path: { executionId } } })),
  });
  return (
    <DataState query={artifacts} empty={(d) => d.items.length === 0} emptyText="No artifacts.">
      {(d) => (
        <Table>
          <THead>
            <Tr>
              <Th>Name</Th>
              <Th>Task</Th>
              <Th className="text-right">Size</Th>
              <Th>Type</Th>
              <Th>
                <span className="sr-only">Download</span>
              </Th>
            </Tr>
          </THead>
          <TBody>
            {d.items.map((a) => (
              <Tr key={a.id}>
                <Td className="font-mono text-xs">{a.name}</Td>
                <Td className="font-mono text-xs">{a.task_key}</Td>
                <Td className="text-right tabular-nums">{formatBytes(a.size)}</Td>
                <Td className="text-xs">{a.content_type}</Td>
                <Td>
                  <a
                    href={`/api/v1/executions/${encodeURIComponent(executionId)}/artifacts/${encodeURIComponent(a.id)}`}
                    download={a.name}
                    className="inline-flex items-center gap-1 text-accent hover:underline"
                  >
                    <Download className="h-3.5 w-3.5" aria-hidden />
                    Download
                  </a>
                </Td>
              </Tr>
            ))}
          </TBody>
        </Table>
      )}
    </DataState>
  );
}
