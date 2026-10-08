import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import {
  AlertTriangle,
  Ban,
  ChevronRight,
  Copy,
  CornerDownRight,
  Crosshair,
  FileJson,
  RefreshCw,
  RotateCcw,
  X,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import {
  cancelExecutionMutation,
  getExecutionOptions,
  getExecutionQueryKey,
  listExecutionArtifactsOptions,
  listExecutionMetricsOptions,
  rerunExecutionMutation,
  restartExecutionMutation,
} from "@/api/@tanstack/react-query.gen";
import type { ExecutionDetail, TaskRun } from "@/api/types.gen";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DataState, EmptyState } from "@/components/data-state";
import { LabelBadges } from "@/components/execution-bits";
import { Gantt } from "@/features/executions/gantt";
import { LogViewer } from "@/features/executions/log-viewer";
import { ExecutionStateBadge } from "@/components/state-badges";
import { Button, buttonClass } from "@/components/ui/button";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import { Tabs } from "@/components/ui/tabs";
import { toast } from "@/components/ui/toast";
import { useCurrentUser } from "@/lib/auth";
import { useCommands, type Command } from "@/lib/commands";
import { errorMessage } from "@/lib/errors";
import {
  canCancel,
  canRestart,
  compactJson,
  executionTitle,
  firstFailedRun,
  isTerminal,
  taskLabel,
  type GanttRow,
} from "@/lib/executions";
import { stateLabel } from "@/lib/flows";
import { can } from "@/lib/roles";
import { cn, formatBytes, formatDuration, formatRelative, formatTime } from "@/lib/utils";

export type Tab = "logs" | "outputs" | "metrics" | "artifacts";
/** task and run select a task (and one attempt of it) for the inspector. */
export type DetailSearch = { tab?: Exclude<Tab, "logs">; task?: string; run?: string };

const tabs: { value: Tab; label: string }[] = [
  { value: "logs", label: "Logs" },
  { value: "outputs", label: "Outputs" },
  { value: "metrics", label: "Metrics" },
  { value: "artifacts", label: "Artifacts" },
];

const detailKey = (id: string) => getExecutionQueryKey({ path: { executionId: id } });

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

/** downloadJson saves the loaded execution with its task runs as a JSON file. */
function downloadJson(e: ExecutionDetail) {
  const blob = new Blob([JSON.stringify(e, null, 2)], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `execution-${e.id}.json`;
  document.body.appendChild(a);
  a.click();
  a.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}

async function copyText(text: string, what: string) {
  try {
    await navigator.clipboard.writeText(text);
    toast({ title: `${what} copied` });
  } catch {
    toast({ tone: "error", title: `Could not copy the ${what.toLowerCase()}` });
  }
}

export function ExecutionPage({
  executionId,
  search,
  navigate,
  insights,
}: {
  executionId: string;
  search: DetailSearch;
  navigate: (opts: { search: DetailSearch; replace?: boolean }) => void;
  /** insights renders the failure triage. The route passes it, because a feature does not import another feature. */
  insights?: (e: ExecutionDetail) => ReactNode;
}) {
  const qc = useQueryClient();
  const detail = useQuery({
    ...getExecutionOptions({ path: { executionId } }),
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

  return (
    <div className="flex min-w-0 flex-col gap-5">
      <nav aria-label="Breadcrumb" className="flex items-center gap-1 text-sm text-muted-foreground">
        <Link to="/executions" className="rounded-inner hover:text-foreground">
          Executions
        </Link>
        <ChevronRight className="h-3.5 w-3.5" aria-hidden />
        <span className="font-mono text-xs">{executionId.slice(0, 8)}</span>
      </nav>
      <DataState query={detail} skeleton="panel">
        {(e) => <Detail execution={e} live={live} search={search} navigate={navigate} insights={insights} />}
      </DataState>
    </div>
  );
}

function Detail({
  execution: e,
  live,
  search,
  navigate,
  insights,
}: {
  execution: ExecutionDetail;
  live: boolean;
  search: DetailSearch;
  navigate: (opts: { search: DetailSearch; replace?: boolean }) => void;
  insights?: (e: ExecutionDetail) => ReactNode;
}) {
  const me = useCurrentUser();
  const actions = useExecutionActions(e);
  const now = useNow(live);
  const tab: Tab = search.tab ?? "logs";
  const task = search.task ?? "";
  const selectedRun = e.task_runs.find((r) => r.id === search.run) ?? undefined;
  const failed = firstFailedRun(e.task_runs);

  const setSearch = useCallback(
    (next: Partial<DetailSearch>) =>
      navigate({ search: { tab: search.tab, task: search.task, run: search.run, ...next }, replace: true }),
    [navigate, search.tab, search.task, search.run],
  );
  const select = (row: GanttRow | undefined) =>
    row && row.id !== search.run
      ? setSearch({ task: row.taskKey, run: row.id })
      : setSearch({ task: undefined, run: undefined });
  const jumpToFailure = useCallback(() => {
    if (failed) setSearch({ task: failed.task_key, run: failed.id, tab: undefined });
  }, [failed, setSearch]);

  const commands = useMemo((): Command[] => {
    const list: Command[] = [
      {
        id: "exec-json",
        group: "This execution",
        label: "Download as JSON",
        icon: FileJson,
        run: () => downloadJson(e),
      },
      {
        id: "exec-copy",
        group: "This execution",
        label: "Copy execution ID",
        icon: Copy,
        run: () => void copyText(e.id, "Execution ID"),
      },
    ];
    if (failed) {
      list.unshift({
        id: "exec-jump",
        group: "This execution",
        label: "Jump to first failure",
        icon: Crosshair,
        run: jumpToFailure,
      });
    }
    if (can(me.role, "operator")) {
      list.unshift({ id: "exec-rerun", group: "This execution", label: "Rerun", icon: RefreshCw, run: actions.rerun });
      if (canRestart(e.state)) {
        list.unshift({
          id: "exec-restart",
          group: "This execution",
          label: "Restart from failed",
          icon: RotateCcw,
          run: actions.restart,
        });
      }
      if (canCancel(e.state)) {
        list.push({
          id: "exec-cancel",
          group: "This execution",
          label: "Cancel execution",
          icon: Ban,
          run: actions.askCancel,
        });
      }
    }
    return list;
  }, [e, failed, jumpToFailure, me.role, actions.rerun, actions.restart, actions.askCancel]);
  useCommands(commands);

  return (
    <>
      <Header execution={e} live={live} now={now} actions={actions} />
      {insights?.(e)}
      <SplitView
        left={
          <section aria-labelledby="timeline-heading" className="flex min-h-0 min-w-0 flex-1 flex-col">
            <div className="flex h-11 shrink-0 items-center justify-between gap-2 border-b px-4">
              <h2 id="timeline-heading" className="text-sm font-semibold">
                Timeline
              </h2>
              <div className="flex items-center gap-1">
                {failed && (
                  <Button variant="ghost" size="sm" onClick={jumpToFailure}>
                    <Crosshair className="h-3.5 w-3.5" aria-hidden />
                    Jump to first failure
                  </Button>
                )}
              </div>
            </div>
            <div className="min-h-0 flex-1 overflow-auto">
              <Gantt runs={e.task_runs} now={now} live={live} selected={selectedRun?.id} onSelect={select} />
            </div>
          </section>
        }
        right={
          <section aria-label="Inspector" className="flex min-h-0 min-w-0 flex-1 flex-col">
            {selectedRun && <AttemptCard run={selectedRun} onClose={() => select(undefined)} />}
            <Tabs
              label="Execution sections"
              tabs={tabs}
              value={tab}
              onChange={(t) => setSearch({ tab: t === "logs" ? undefined : t })}
              className="shrink-0 px-2"
            />
            <div className="flex min-h-0 flex-1 flex-col overflow-auto p-3">
              {tab === "logs" && (
                <LogViewer
                  key={e.id}
                  executionId={e.id}
                  task={task}
                  item={selectedRun?.item != null ? selectedRun.item_index : undefined}
                  onTaskChange={(t) => setSearch({ task: t || undefined, run: undefined })}
                  className="flex-1"
                />
              )}
              {tab === "outputs" && <Outputs execution={e} task={task} />}
              {tab === "metrics" && <Metrics executionId={e.id} task={task} />}
              {tab === "artifacts" && <Artifacts executionId={e.id} task={task} />}
            </div>
          </section>
        }
      />
      <ConfirmDialog
        open={actions.confirmOpen}
        title="Cancel execution"
        confirmLabel="Cancel execution"
        destructive
        pending={actions.cancel.isPending}
        error={actions.cancel.isError ? errorMessage(actions.cancel.error) : undefined}
        onConfirm={actions.confirmCancel}
        onClose={actions.closeCancel}
      >
        Cancel this execution? Running tasks are stopped.
      </ConfirmDialog>
    </>
  );
}

/** useExecutionActions holds cancel, rerun and restart, so the header and the palette share them. */
function useExecutionActions(e: ExecutionDetail) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const path = useMemo(() => ({ path: { executionId: e.id } }), [e.id]);
  const cancel = useMutation({
    ...cancelExecutionMutation(),
    onSuccess: (d) => {
      qc.setQueryData(detailKey(e.id), d);
      setConfirmOpen(false);
      toast({ title: "Cancel requested", description: "Running tasks stop now." });
    },
  });
  const goTo = (what: string) => (d: ExecutionDetail) => {
    toast({ title: `${what} started` });
    void navigate({ to: "/executions/$executionId", params: { executionId: d.id } });
  };
  const rerunM = useMutation({ ...rerunExecutionMutation(), onSuccess: goTo("Rerun") });
  const restartM = useMutation({ ...restartExecutionMutation(), onSuccess: goTo("Restart") });
  const { mutate: rerunMutate } = rerunM;
  const { mutate: restartMutate } = restartM;
  const { mutate: cancelMutate } = cancel;
  return {
    cancel,
    rerunM,
    restartM,
    confirmOpen,
    error: rerunM.error ?? restartM.error,
    rerun: useCallback(() => rerunMutate(path), [rerunMutate, path]),
    restart: useCallback(() => restartMutate(path), [restartMutate, path]),
    askCancel: useCallback(() => setConfirmOpen(true), []),
    closeCancel: useCallback(() => setConfirmOpen(false), []),
    confirmCancel: useCallback(() => cancelMutate(path), [cancelMutate, path]),
  };
}

type Actions = ReturnType<typeof useExecutionActions>;

const splitKey = "sluice-exec-split";

/**
 * SplitView puts the waterfall and the inspector side by side on wide screens, with a divider
 * that drags or moves with the arrow keys. Narrow screens stack the two.
 */
function SplitView({ left, right }: { left: ReactNode; right: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);
  const [pct, setPctState] = useState(() => {
    try {
      const v = Number(localStorage.getItem(splitKey));
      return v >= 25 && v <= 75 ? v : 46;
    } catch {
      return 46;
    }
  });
  const drag = useRef<{ x: number; pct: number; width: number } | null>(null);
  const setPct = (v: number) => {
    const c = Math.round(Math.min(72, Math.max(28, v)) * 10) / 10;
    setPctState(c);
    try {
      localStorage.setItem(splitKey, String(c));
    } catch {
      // Storage is not available. The width lasts for this page load.
    }
  };
  const onKeyDown = (ev: KeyboardEvent<HTMLDivElement>) => {
    const step = ev.shiftKey ? 10 : 2;
    if (ev.key === "ArrowLeft") setPct(pct - step);
    else if (ev.key === "ArrowRight") setPct(pct + step);
    else if (ev.key === "Home") setPct(28);
    else if (ev.key === "End") setPct(72);
    else return;
    ev.preventDefault();
  };

  return (
    <div
      ref={ref}
      className="flex flex-col gap-4 lg:h-[max(34rem,calc(100vh-15.5rem))] lg:flex-row lg:gap-0"
      style={{ "--split": `${pct}%` } as React.CSSProperties}
    >
      <div className="flex max-h-[28rem] min-h-40 min-w-0 flex-col overflow-hidden rounded-panel border bg-panel shadow-panel lg:max-h-none lg:w-[var(--split)] lg:shrink-0">
        {left}
      </div>
      <div
        role="separator"
        aria-orientation="vertical"
        aria-label="Resize the inspector"
        aria-valuemin={28}
        aria-valuemax={72}
        aria-valuenow={pct}
        tabIndex={0}
        onKeyDown={onKeyDown}
        onPointerDown={(ev) => {
          ev.currentTarget.setPointerCapture(ev.pointerId);
          drag.current = { x: ev.clientX, pct, width: ref.current?.clientWidth ?? 1 };
          document.body.style.cursor = "col-resize";
          document.body.style.userSelect = "none";
        }}
        onPointerMove={(ev) => {
          const d = drag.current;
          if (d) setPct(d.pct + ((ev.clientX - d.x) / d.width) * 100);
        }}
        onPointerUp={() => {
          drag.current = null;
          document.body.style.cursor = "";
          document.body.style.userSelect = "";
        }}
        onDoubleClick={() => setPct(46)}
        className="group hidden w-3 shrink-0 cursor-col-resize items-center justify-center outline-none lg:flex"
      >
        <span className="h-10 w-1 rounded-full bg-border transition-[background-color,height] duration-200 ease-snappy group-hover:h-16 group-hover:bg-accent group-focus-visible:h-16 group-focus-visible:bg-accent group-active:bg-accent" />
      </div>
      <div className="flex min-h-[32rem] min-w-0 flex-1 flex-col overflow-hidden rounded-panel border bg-panel shadow-panel lg:min-h-0">
        {right}
      </div>
    </div>
  );
}

function ExecLink({ id, children }: { id: string; children?: ReactNode }) {
  return (
    <Link
      to="/executions/$executionId"
      params={{ executionId: id }}
      className="font-mono text-xs text-accent-text hover:underline"
    >
      {children ?? id.slice(0, 8)}
    </Link>
  );
}

function Meta({ label, children, wide = false }: { label: string; children: ReactNode; wide?: boolean }) {
  return (
    <div className={cn("flex min-w-0 flex-col gap-0.5", wide && "col-span-2")}>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="min-w-0 text-sm">{children}</dd>
    </div>
  );
}

function Header({
  execution: e,
  live,
  now,
  actions,
}: {
  execution: ExecutionDetail;
  live: boolean;
  now: number;
  actions: Actions;
}) {
  const me = useCurrentUser();
  const t = executionTitle(e);
  const started = e.started_at ? Date.parse(e.started_at) : undefined;
  const duration = e.duration_ms ?? (started !== undefined && live ? Math.max(now - started, 0) : null);
  const version = e.snapshot_version != null ? `v${e.snapshot_version}` : e.git_sha ? e.git_sha.slice(0, 12) : "—";

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex min-w-0 flex-col gap-1">
          <h1 className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xl font-semibold tracking-tight">
            <span className="break-all">
              <span className="text-muted-foreground">{e.namespace}/</span>
              {t.title}
            </span>
            <ExecutionStateBadge state={e.state} />
          </h1>
          <div className="flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
            <span className="font-mono break-all">{e.id}</span>
            <button
              type="button"
              onClick={() => void copyText(e.id, "Execution ID")}
              className="pressable inline-flex h-6 w-6 items-center justify-center rounded-inner hover:bg-muted hover:text-foreground"
            >
              <Copy className="h-3.5 w-3.5" aria-hidden />
              <span className="sr-only">Copy execution ID</span>
            </button>
            <span aria-hidden>·</span>
            <span title={formatTime(e.created_at)}>Created {formatRelative(e.created_at, now)}</span>
          </div>
        </div>
        <div className="flex flex-col items-end gap-1">
          <div className="flex flex-wrap justify-end gap-2">
            <Button variant="secondary" onClick={() => downloadJson(e)}>
              <FileJson className="h-4 w-4" aria-hidden />
              Download JSON
            </Button>
            {can(me.role, "operator") && (
              <>
                {canCancel(e.state) && (
                  <Button variant="secondary" onClick={actions.askCancel}>
                    <Ban className="h-4 w-4" aria-hidden />
                    Cancel
                  </Button>
                )}
                {canRestart(e.state) && (
                  <Button variant="secondary" disabled={actions.restartM.isPending} onClick={actions.restart}>
                    <RotateCcw className="h-4 w-4" aria-hidden />
                    Restart from failed
                  </Button>
                )}
                <Button disabled={actions.rerunM.isPending} onClick={actions.rerun}>
                  <RefreshCw className={cn("h-4 w-4", actions.rerunM.isPending && "animate-spin")} aria-hidden />
                  Rerun
                </Button>
              </>
            )}
          </div>
          {actions.error && (
            <p role="alert" className="text-xs text-destructive">
              {errorMessage(actions.error)}
            </p>
          )}
        </div>
      </div>
      {e.state === "FAILED" && e.error && (
        <p
          role="alert"
          className="flex animate-enter items-start gap-2.5 rounded-panel border border-state-failed/25 bg-state-failed/[0.06] px-4 py-3 text-sm"
        >
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-state-failed" aria-hidden />
          <span className="font-mono text-xs leading-5 break-words whitespace-pre-wrap">{e.error}</span>
        </p>
      )}
      <dl className="grid grid-cols-2 gap-x-6 gap-y-3 rounded-panel border bg-panel px-4 py-3 shadow-panel sm:grid-cols-3 lg:grid-cols-6">
        <Meta label="Duration">
          <span className="tabular-nums">{formatDuration(duration)}</span>
        </Meta>
        <Meta label="Trigger">
          {stateLabel(e.trigger_type)}
          {e.created_by && <span className="text-muted-foreground"> by {e.created_by}</span>}
        </Meta>
        <Meta label="Version">
          <span className="font-mono text-xs">{version}</span>
        </Meta>
        <Meta label="Created">{formatTime(e.created_at)}</Meta>
        <Meta label="Inputs" wide>
          <code className="block truncate font-mono text-xs" title={compactJson(e.inputs)}>
            {compactJson(e.inputs)}
          </code>
        </Meta>
        {t.path && (
          <Meta label="File" wide>
            <span className="font-mono text-xs break-all">{t.path}</span>
          </Meta>
        )}
        <Meta label="Labels" wide>
          <LabelBadges labels={e.labels} />
        </Meta>
        {e.parent_execution_id && (
          <Meta label="Parent execution">
            <ExecLink id={e.parent_execution_id} />
          </Meta>
        )}
        {e.restart_of_id && (
          <Meta label="Restart of">
            <ExecLink id={e.restart_of_id} />
          </Meta>
        )}
        {e.children.length > 0 && (
          <Meta label="Child executions" wide>
            <span className="flex flex-wrap gap-x-4 gap-y-1">
              {e.children.map((c) => (
                <span key={c.id} className="flex items-center gap-1.5">
                  <ExecLink id={c.id} />
                  <ExecutionStateBadge state={c.state} />
                </span>
              ))}
            </span>
          </Meta>
        )}
      </dl>
    </div>
  );
}

/** AttemptCard shows the facts of the selected task run above the inspector tabs. */
function AttemptCard({ run: r, onClose }: { run: TaskRun; onClose: () => void }) {
  const queued = r.queued_at ? Date.parse(r.queued_at) : undefined;
  const started = r.started_at ? Date.parse(r.started_at) : undefined;
  const wait = queued !== undefined && started !== undefined ? started - queued : null;
  return (
    <div className="flex shrink-0 animate-enter flex-col gap-2 border-b bg-muted/40 px-4 py-3">
      <div className="flex items-center justify-between gap-2">
        <h3 className="flex min-w-0 items-center gap-2 text-sm font-semibold">
          <span className="truncate font-mono">
            {taskLabel(r)} #{r.attempt}
          </span>
          <ExecutionStateBadge state={r.state} />
        </h3>
        <button
          type="button"
          onClick={onClose}
          className="pressable inline-flex h-6 items-center gap-1 rounded-full px-2 text-xs text-muted-foreground hover:bg-muted hover:text-foreground"
        >
          <X className="h-3 w-3" aria-hidden />
          All tasks
        </button>
      </div>
      <dl className="grid grid-cols-2 gap-x-4 gap-y-1.5 text-xs sm:grid-cols-4">
        <div>
          <dt className="text-muted-foreground">Type</dt>
          <dd>{r.task_type || "—"}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Executor</dt>
          <dd className="truncate">{[r.executor_type, r.pool].filter(Boolean).join(" · ") || "—"}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Duration</dt>
          <dd className="tabular-nums">{formatDuration(r.duration_ms)}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Queue wait</dt>
          <dd className="tabular-nums">{formatDuration(wait)}</dd>
        </div>
        {r.item != null && (
          <div className="col-span-2 min-w-0">
            <dt className="text-muted-foreground">Item</dt>
            <dd className="truncate font-mono" title={compactJson(r.item)}>
              {compactJson(r.item)}
            </dd>
          </div>
        )}
        {r.exit_code != null && (
          <div>
            <dt className="text-muted-foreground">Exit code</dt>
            <dd className="font-mono">{r.exit_code}</dd>
          </div>
        )}
        {r.reason && (
          <div className="col-span-2 sm:col-span-3">
            <dt className="text-muted-foreground">Reason</dt>
            <dd className="truncate">{r.reason}</dd>
          </div>
        )}
        {r.child_execution_id && (
          <div>
            <dt className="text-muted-foreground">Child execution</dt>
            <dd>
              <ExecLink id={r.child_execution_id} />
            </dd>
          </div>
        )}
      </dl>
      {r.error && (
        <p className="flex items-start gap-1.5 font-mono text-xs break-words whitespace-pre-wrap text-destructive">
          <CornerDownRight className="mt-0.5 h-3 w-3 shrink-0" aria-hidden />
          {r.error}
        </p>
      )}
    </div>
  );
}

function JsonBlock({ value }: { value: unknown }) {
  return (
    <pre className="max-h-96 overflow-auto rounded-control border bg-muted/40 p-3 font-mono text-xs leading-5">
      {JSON.stringify(value, null, 2)}
    </pre>
  );
}

function Outputs({ execution: e, task }: { execution: ExecutionDetail; task: string }) {
  const runs = e.task_runs.filter(
    (r) => (!task || r.task_key === task) && r.outputs && Object.keys(r.outputs).length > 0,
  );
  const hasExec = !task && !!e.outputs && Object.keys(e.outputs).length > 0;
  if (!hasExec && runs.length === 0) return <EmptyState text="No outputs." />;
  return (
    <div className="flex min-w-0 flex-col gap-4">
      {hasExec && (
        <section className="flex flex-col gap-2">
          <h3 className="text-xs font-semibold text-muted-foreground">Execution outputs</h3>
          <JsonBlock value={e.outputs} />
        </section>
      )}
      {runs.map((r) => (
        <section key={r.id} className="flex flex-col gap-2">
          <h3 className="font-mono text-xs font-semibold">
            {taskLabel(r)} #{r.attempt}
          </h3>
          <JsonBlock value={r.outputs} />
        </section>
      ))}
    </div>
  );
}

function Metrics({ executionId, task }: { executionId: string; task: string }) {
  const metrics = useQuery(listExecutionMetricsOptions({ path: { executionId } }));
  return (
    <DataState
      query={{ ...metrics, data: metrics.data?.items.filter((m) => !task || m.task_key === task) }}
      empty={(d) => d.length === 0}
      emptyText="No metrics."
    >
      {(items) => (
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
            {items.map((m, i) => (
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

function Artifacts({ executionId, task }: { executionId: string; task: string }) {
  const artifacts = useQuery(listExecutionArtifactsOptions({ path: { executionId } }));
  return (
    <DataState
      query={{ ...artifacts, data: artifacts.data?.items.filter((a) => !task || a.task_key === task) }}
      empty={(d) => d.length === 0}
      emptyText="No artifacts."
    >
      {(items) => (
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
            {items.map((a) => (
              <Tr key={a.id}>
                <Td className="font-mono text-xs">{a.name}</Td>
                <Td className="font-mono text-xs">{a.task_key}</Td>
                <Td className="text-right tabular-nums">{formatBytes(a.size)}</Td>
                <Td className="text-xs">{a.content_type}</Td>
                <Td className="text-right">
                  <a
                    href={`/api/v1/executions/${encodeURIComponent(executionId)}/artifacts/${encodeURIComponent(a.id)}`}
                    download={a.name}
                    className={buttonClass("ghost", "sm")}
                  >
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
