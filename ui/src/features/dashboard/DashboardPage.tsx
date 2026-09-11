import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { getDashboardOptions, listUpcomingSchedulesOptions } from "@/api/@tanstack/react-query.gen";
import type { DashboardOut } from "@/api/types.gen";
import { LineChart, StackedChart, type ChartRow } from "@/components/charts";
import { DataState } from "@/components/data-state";
import { ExecutionStateBadge } from "@/components/state-badges";
import { Input, Select } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import { formatMs, formatRate, stateColor } from "@/lib/charts";
import { formatTime } from "@/lib/utils";

type Range = "24h" | "7d" | "30d";

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

function Kpi({ label, value }: { label: string; value: string }) {
  return (
    <div role="group" aria-label={label} className="flex flex-col gap-1 rounded-[8px] border bg-panel p-3">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="text-2xl font-semibold tabular-nums">{value}</span>
    </div>
  );
}

/** DashboardPage shows KPIs, charts, running executions, recent failures and next schedules (REQ-UI-003). */
export function DashboardPage() {
  const [range, setRange] = useState<Range>("24h");
  const [namespace, setNamespace] = useState("");
  const ns = namespace.trim() || undefined;
  const dash = useQuery({ ...getDashboardOptions({ query: { range, namespace: ns } }), refetchInterval: 10_000 });
  const schedules = useQuery(listUpcomingSchedulesOptions({ query: { limit: 10, namespace: ns } }));

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Dashboard"
        description="Executions that ended in the selected range."
        actions={
          <div className="flex flex-wrap items-end gap-2">
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              Range
              <Select value={range} onChange={(e) => setRange(e.target.value as Range)}>
                <option value="24h">Last 24 hours</option>
                <option value="7d">Last 7 days</option>
                <option value="30d">Last 30 days</option>
              </Select>
            </label>
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              Namespace
              <Input value={namespace} placeholder="All namespaces" onChange={(e) => setNamespace(e.target.value)} className="w-44" />
            </label>
          </div>
        }
      />
      <DataState query={dash}>{(d) => <DashboardBody d={d} range={range} />}</DataState>
      <section aria-labelledby="schedules-heading" className="flex flex-col gap-2">
        <h2 id="schedules-heading" className="text-base font-semibold">
          Next schedules
        </h2>
        <DataState query={schedules} empty={(s) => s.items.length === 0} emptyText="No active schedules.">
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
                      <Link to="/flows/$namespace/$flowId" params={{ namespace: u.namespace, flowId: u.flow_id }} className="hover:underline">
                        {u.namespace}/{u.flow_id}
                      </Link>
                    </Td>
                    <Td className="font-mono text-xs">{u.trigger_id}</Td>
                    <Td className="font-mono text-xs">
                      {u.cron} {u.timezone !== "UTC" && <span className="text-muted-foreground">{u.timezone}</span>}
                    </Td>
                    <Td>{formatTime(u.next_fire_at)}</Td>
                  </Tr>
                ))}
              </TBody>
            </Table>
          )}
        </DataState>
      </section>
    </div>
  );
}

function DashboardBody({ d, range }: { d: DashboardOut; range: Range }) {
  const k = d.kpis;
  const stateRows: ChartRow[] = d.buckets.map((b) => ({
    label: bucketLabel(b.start, range),
    success: b.success,
    failed: b.failed,
    timed_out: b.timed_out,
    cancelled: b.cancelled,
    skipped: b.skipped,
  }));
  const durationRows: ChartRow[] = d.buckets.map((b) => ({ label: bucketLabel(b.start, range), p50: b.p50_ms ?? null, p95: b.p95_ms ?? null }));
  const anyEnded = d.buckets.some((b) => b.success + b.failed + b.timed_out + b.cancelled + b.skipped > 0);
  const anyDuration = d.buckets.some((b) => b.p50_ms !== null && b.p50_ms !== undefined);
  return (
    <>
      <section aria-label="Key figures" className="grid grid-cols-2 gap-3 md:grid-cols-5">
        <Kpi label="Executions" value={String(k.executions)} />
        <Kpi label="Success rate" value={formatRate(k.success_rate)} />
        <Kpi label="Failed" value={String(k.failed + k.timed_out)} />
        <Kpi label="Median duration" value={formatMs(k.median_duration_ms)} />
        <Kpi label="Running now" value={String(k.running)} />
      </section>
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <StackedChart title="Executions by end state" rows={anyEnded ? stateRows : []} series={stateSeries} />
        <LineChart title="Duration p50 and p95" rows={anyDuration ? durationRows : []} series={durationSeries} format={formatMs} />
      </div>
      <section aria-labelledby="running-heading" className="flex flex-col gap-2">
        <h2 id="running-heading" className="text-base font-semibold">
          Running now
        </h2>
        {d.running.length === 0 ? (
          <p className="text-sm text-muted-foreground">No execution runs now.</p>
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
                    <Link to="/executions/$executionId" params={{ executionId: x.id }} className="hover:underline">
                      {x.namespace}/{x.flow_id || "file"}
                    </Link>
                  </Td>
                  <Td>
                    <ExecutionStateBadge state={x.state} />
                  </Td>
                  <Td>{x.trigger_type}</Td>
                  <Td>{x.started_at ? formatTime(x.started_at) : "—"}</Td>
                </Tr>
              ))}
            </TBody>
          </Table>
        )}
      </section>
      <section aria-labelledby="failures-heading" className="flex flex-col gap-2">
        <h2 id="failures-heading" className="text-base font-semibold">
          Recent failures
        </h2>
        {d.recent_failures.length === 0 ? (
          <p className="text-sm text-muted-foreground">No execution failed.</p>
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
                    <Link to="/executions/$executionId" params={{ executionId: x.id }} className="hover:underline">
                      {x.namespace}/{x.flow_id || "file"}
                    </Link>
                  </Td>
                  <Td>
                    <ExecutionStateBadge state={x.state} />
                  </Td>
                  <Td>{x.ended_at ? formatTime(x.ended_at) : "—"}</Td>
                  <Td className="max-w-md whitespace-normal text-sm">{x.triage_summary ?? <span className="text-muted-foreground">{x.error || "—"}</span>}</Td>
                </Tr>
              ))}
            </TBody>
          </Table>
        )}
      </section>
    </>
  );
}
