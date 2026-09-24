import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
  CalendarClock,
  CircleCheck,
  CirclePlay,
  Search,
  Timer,
  XCircle,
  Activity,
  type LucideIcon,
} from "lucide-react";
import { useState, type ReactNode } from "react";
import { getDashboardOptions, listUpcomingSchedulesOptions } from "@/api/@tanstack/react-query.gen";
import type { DashboardOut } from "@/api/types.gen";
import { LineChart, StackedChart, type ChartRow } from "@/components/charts";
import { DataState, EmptyState } from "@/components/data-state";
import { ExecutionStateBadge } from "@/components/state-badges";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { SegmentedControl } from "@/components/ui/segmented";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import { formatMs, formatRate, stateColor } from "@/lib/charts";
import { stateLabel } from "@/lib/flows";
import { cn, formatRelative, formatTime } from "@/lib/utils";

type Range = "24h" | "7d" | "30d";

const ranges: { value: Range; label: string }[] = [
  { value: "24h", label: "24 hours" },
  { value: "7d", label: "7 days" },
  { value: "30d", label: "30 days" },
];

const rangeText: Record<Range, string> = {
  "24h": "Ended in the last 24 hours",
  "7d": "Ended in the last 7 days",
  "30d": "Ended in the last 30 days",
};

const stateSeries = [
  { key: "success", label: "Success", color: stateColor("success") },
  { key: "failed", label: "Failed", color: stateColor("failed") },
  { key: "timed_out", label: "Timed out", color: stateColor("timed_out") },
  { key: "cancelled", label: "Cancelled", color: stateColor("cancelled") },
  { key: "skipped", label: "Skipped", color: stateColor("skipped") },
];

const durationSeries = [
  { key: "p50", label: "p50", color: "var(--accent)" },
  { key: "p95", label: "p95", color: "var(--state-timed-out)" },
];

function bucketLabel(iso: string, range: Range): string {
  const d = new Date(iso);
  return range === "24h"
    ? d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" })
    : d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

/** Kpi is a stat card: a label, a large number and a caption. An accent marks a state that needs attention. */
function Kpi({
  label,
  value,
  caption,
  icon: Icon,
  accent,
  className,
}: {
  label: string;
  value: string;
  caption: string;
  icon: LucideIcon;
  accent?: "failed" | "running";
  className?: string;
}) {
  return (
    <div
      role="group"
      aria-label={label}
      className={cn("flex min-w-0 flex-col gap-1 rounded-panel border bg-panel px-4 py-3 shadow-panel", className)}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="truncate text-xs font-medium text-muted-foreground">{label}</span>
        <Icon
          aria-hidden
          className={cn(
            "h-3.5 w-3.5 shrink-0",
            accent === "failed" && "text-state-failed",
            accent === "running" && "animate-breathe text-state-running",
            !accent && "text-muted-foreground/70",
          )}
        />
      </div>
      <span className="text-2xl font-semibold tracking-tight tabular-nums">{value}</span>
      <span className="truncate text-xs text-muted-foreground tabular-nums">{caption}</span>
    </div>
  );
}

/** Section is a titled block of the dashboard with an optional count and link. */
function Section({
  id,
  title,
  count,
  link,
  children,
}: {
  id: string;
  title: string;
  count?: number;
  link?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section aria-labelledby={id} className="flex min-w-0 flex-col gap-2">
      <div className="flex items-center gap-2">
        <h2 id={id} className="text-sm font-semibold">
          {title}
        </h2>
        {count !== undefined && count > 0 && (
          <span className="rounded-full bg-muted px-1.5 text-xs font-medium text-muted-foreground tabular-nums">
            {count}
          </span>
        )}
        {link && <span className="ml-auto">{link}</span>}
      </div>
      {children}
    </section>
  );
}

const sectionLink = "text-xs font-medium text-accent-text hover:underline";

/** DashboardPage shows KPIs, charts, running executions, recent failures and next schedules (REQ-UI-003). */
export function DashboardPage() {
  const [range, setRange] = useState<Range>("24h");
  const [namespace, setNamespace] = useState("");
  const ns = namespace.trim() || undefined;
  const dash = useQuery({ ...getDashboardOptions({ query: { range, namespace: ns } }), refetchInterval: 10_000 });
  const schedules = useQuery(listUpcomingSchedulesOptions({ query: { limit: 10, namespace: ns } }));

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Dashboard"
        description="Executions that ended in the selected range."
        actions={
          <>
            <SegmentedControl label="Range" options={ranges} value={range} onChange={setRange} />
            <div className="relative w-full sm:w-48">
              <Search
                aria-hidden
                className="pointer-events-none absolute top-1/2 left-2.5 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground"
              />
              <Input
                aria-label="Namespace"
                value={namespace}
                placeholder="All namespaces"
                onChange={(e) => setNamespace(e.target.value)}
                className="pl-8"
              />
            </div>
          </>
        }
      />
      <DataState query={dash} skeleton="panel">
        {(d) => <DashboardBody d={d} range={range} ns={ns} />}
      </DataState>
      <Section id="schedules-heading" title="Next schedules" count={schedules.data?.items.length}>
        <DataState
          query={schedules}
          empty={(s) => s.items.length === 0}
          emptyText="No active schedules."
          emptyIcon={CalendarClock}
        >
          {(s) => (
            <Table>
              <THead>
                <Tr>
                  <Th>Flow</Th>
                  <Th>Trigger</Th>
                  <Th>Cron</Th>
                  <Th>Next fire time</Th>
                </Tr>
              </THead>
              <TBody>
                {s.items.map((u) => (
                  <Tr key={`${u.namespace}/${u.flow_id}/${u.trigger_id}`}>
                    <Td>
                      <Link
                        to="/flows/$namespace/$flowId"
                        params={{ namespace: u.namespace, flowId: u.flow_id }}
                        className="font-medium hover:underline"
                      >
                        <span className="font-normal text-muted-foreground">{u.namespace}/</span>
                        {u.flow_id}
                      </Link>
                    </Td>
                    <Td className="font-mono text-xs">{u.trigger_id}</Td>
                    <Td className="font-mono text-xs">
                      {u.cron} {u.timezone !== "UTC" && <span className="text-muted-foreground">{u.timezone}</span>}
                    </Td>
                    <Td className="tabular-nums">{formatTime(u.next_fire_at)}</Td>
                  </Tr>
                ))}
              </TBody>
            </Table>
          )}
        </DataState>
      </Section>
    </div>
  );
}

function ExecutionLink({ id, namespace, flowId }: { id: string; namespace: string; flowId?: string | null }) {
  return (
    <Link to="/executions/$executionId" params={{ executionId: id }} className="font-medium hover:underline">
      <span className="font-normal text-muted-foreground">{namespace}/</span>
      {flowId || "file"}
    </Link>
  );
}

function DashboardBody({ d, range, ns }: { d: DashboardOut; range: Range; ns: string | undefined }) {
  const k = d.kpis;
  const stateRows: ChartRow[] = d.buckets.map((b) => ({
    label: bucketLabel(b.start, range),
    success: b.success,
    failed: b.failed,
    timed_out: b.timed_out,
    cancelled: b.cancelled,
    skipped: b.skipped,
  }));
  const durationRows: ChartRow[] = d.buckets.map((b) => ({
    label: bucketLabel(b.start, range),
    p50: b.p50_ms ?? null,
    p95: b.p95_ms ?? null,
  }));
  const anyEnded = d.buckets.some((b) => b.success + b.failed + b.timed_out + b.cancelled + b.skipped > 0);
  const anyDuration = d.buckets.some((b) => b.p50_ms !== null && b.p50_ms !== undefined);
  const failed = k.failed + k.timed_out;
  return (
    <>
      <section aria-label="Key figures" className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
        <Kpi label="Executions" value={String(k.executions)} caption={rangeText[range]} icon={Activity} />
        <Kpi
          label="Success rate"
          value={formatRate(k.success_rate)}
          caption={`${k.succeeded} succeeded`}
          icon={CircleCheck}
        />
        <Kpi
          label="Failed"
          value={String(failed)}
          caption={k.timed_out > 0 ? `${k.timed_out} timed out` : "Failed or timed out"}
          icon={XCircle}
          accent={failed > 0 ? "failed" : undefined}
        />
        <Kpi
          label="Median duration"
          value={formatMs(k.median_duration_ms)}
          caption="Of ended executions"
          icon={Timer}
        />
        <Kpi
          label="Running now"
          value={String(k.running)}
          caption="Updates every 10 s"
          icon={CirclePlay}
          accent={k.running > 0 ? "running" : undefined}
          className="col-span-2 sm:col-span-1"
        />
      </section>
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <StackedChart title="Executions by end state" rows={anyEnded ? stateRows : []} series={stateSeries} />
        <LineChart
          title="Duration p50 and p95"
          rows={anyDuration ? durationRows : []}
          series={durationSeries}
          format={formatMs}
        />
      </div>
      <Section
        id="running-heading"
        title="Running now"
        count={d.running.length}
        link={
          d.running.length > 0 && (
            <Link to="/executions" search={{ state: "RUNNING", namespace: ns }} className={sectionLink}>
              View all
            </Link>
          )
        }
      >
        {d.running.length === 0 ? (
          <EmptyState icon={CirclePlay} text="No execution runs now." className="py-6" />
        ) : (
          <Table>
            <THead>
              <Tr>
                <Th>Execution</Th>
                <Th>State</Th>
                <Th>Trigger</Th>
                <Th>Started</Th>
              </Tr>
            </THead>
            <TBody>
              {d.running.map((x) => (
                <Tr key={x.id}>
                  <Td>
                    <ExecutionLink id={x.id} namespace={x.namespace} flowId={x.flow_id} />
                  </Td>
                  <Td>
                    <ExecutionStateBadge state={x.state} />
                  </Td>
                  <Td className="text-muted-foreground">{stateLabel(x.trigger_type)}</Td>
                  <Td className="text-muted-foreground tabular-nums" title={formatTime(x.started_at)}>
                    {x.started_at ? formatRelative(x.started_at) : "—"}
                  </Td>
                </Tr>
              ))}
            </TBody>
          </Table>
        )}
      </Section>
      <Section
        id="failures-heading"
        title="Recent failures"
        count={d.recent_failures.length}
        link={
          d.recent_failures.length > 0 && (
            <Link to="/executions" search={{ state: "FAILED,TIMED_OUT", namespace: ns }} className={sectionLink}>
              View all
            </Link>
          )
        }
      >
        {d.recent_failures.length === 0 ? (
          <EmptyState icon={CircleCheck} text="No execution failed." className="py-6" />
        ) : (
          <Table>
            <THead>
              <Tr>
                <Th>Execution</Th>
                <Th>State</Th>
                <Th>Ended</Th>
                <Th>Triage</Th>
              </Tr>
            </THead>
            <TBody>
              {d.recent_failures.map((x) => (
                <Tr key={x.id}>
                  <Td>
                    <ExecutionLink id={x.id} namespace={x.namespace} flowId={x.flow_id} />
                  </Td>
                  <Td>
                    <ExecutionStateBadge state={x.state} />
                  </Td>
                  <Td className="text-muted-foreground tabular-nums" title={formatTime(x.ended_at)}>
                    {x.ended_at ? formatRelative(x.ended_at) : "—"}
                  </Td>
                  <Td className="max-w-md min-w-64 py-2 whitespace-normal">
                    {x.triage_summary ?? (
                      <span className="line-clamp-2 font-mono text-xs text-muted-foreground">{x.error || "—"}</span>
                    )}
                  </Td>
                </Tr>
              ))}
            </TBody>
          </Table>
        )}
      </Section>
    </>
  );
}
