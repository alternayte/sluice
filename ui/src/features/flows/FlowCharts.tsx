import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { AlertTriangle, CheckCircle2, CircleDashed, CirclePlay, History, XCircle, type LucideIcon } from "lucide-react";
import { useState } from "react";
import { getFlowMetricsOptions, getFlowStatsOptions } from "@/api/@tanstack/react-query.gen";
import { chartColors, LineChart, type ChartRow } from "@/components/charts";
import { DataState, EmptyState } from "@/components/data-state";
import { Input, Select } from "@/components/ui/input";
import { SegmentedControl } from "@/components/ui/segmented";
import { formatMs, stateColor } from "@/lib/charts";
import { stateLabel } from "@/lib/flows";
import { cn, formatTime } from "@/lib/utils";

const stateIcons: Record<string, LucideIcon> = {
  success: CheckCircle2,
  failed: XCircle,
  timed_out: AlertTriangle,
  running: CirclePlay,
  cancelling: CirclePlay,
};

function shortTime(iso: string): string {
  return new Date(iso).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

const aggregations: { value: "sum" | "avg" | "max"; label: string }[] = [
  { value: "sum", label: "Sum" },
  { value: "avg", label: "Avg" },
  { value: "max", label: "Max" },
];

/** FlowCharts shows the last 50 execution states, their durations and a custom metric chart (REQ-UI-006). */
export function FlowCharts({ namespace, flowId }: { namespace: string; flowId: string }) {
  const stats = useQuery({ ...getFlowStatsOptions({ path: { namespace, flowId } }), refetchInterval: 5000 });
  return (
    <DataState query={stats} skeleton="panel">
      {(s) => {
        const oldestFirst = [...s.recent].reverse();
        const durationRows: ChartRow[] = oldestFirst
          .filter((x) => x.duration_ms !== null && x.duration_ms !== undefined)
          .map((x) => ({ label: shortTime(x.created_at), duration: x.duration_ms ?? null }));
        const counts = new Map<string, number>();
        for (const x of s.recent) counts.set(x.state.toLowerCase(), (counts.get(x.state.toLowerCase()) ?? 0) + 1);
        return (
          <div className="flex flex-col gap-4">
            <section aria-labelledby="strip-heading" className="flex flex-col gap-2">
              <h2 id="strip-heading" className="text-sm font-semibold">
                Last {s.recent.length} executions
              </h2>
              {s.recent.length === 0 ? (
                <EmptyState icon={History} text="The flow has no executions." className="py-6" />
              ) : (
                <div className="flex flex-col gap-2.5 rounded-panel border bg-panel p-3 shadow-panel">
                  <ol aria-label="Execution states" className="flex flex-wrap gap-0.5">
                    {oldestFirst.map((x) => {
                      const state = x.state.toLowerCase();
                      const Icon = stateIcons[state] ?? CircleDashed;
                      const label = `${stateLabel(state)}, ${formatTime(x.created_at)}`;
                      return (
                        <li key={x.id}>
                          <Link
                            to="/executions/$executionId"
                            params={{ executionId: x.id }}
                            aria-label={label}
                            title={label}
                            className="pressable flex h-6 w-6 items-center justify-center rounded-inner hover:bg-muted"
                          >
                            <Icon
                              className={cn(
                                "h-4 w-4",
                                (state === "running" || state === "cancelling") && "animate-breathe",
                              )}
                              style={{ color: stateColor(state) }}
                              aria-hidden
                            />
                          </Link>
                        </li>
                      );
                    })}
                  </ol>
                  <p className="flex flex-wrap gap-x-3 gap-y-1 px-1 text-xs text-muted-foreground tabular-nums">
                    {[...counts].map(([state, n]) => (
                      <span key={state} className="inline-flex items-center gap-1.5">
                        <span aria-hidden className="h-2 w-2 rounded-full" style={{ background: stateColor(state) }} />
                        {n} {stateLabel(state).toLowerCase()}
                      </span>
                    ))}
                  </p>
                </div>
              )}
            </section>
            <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
              <LineChart
                title="Duration of the last executions"
                rows={durationRows}
                series={[{ key: "duration", label: "Duration", color: "var(--accent)" }]}
                format={formatMs}
                emptyText="No execution of this flow ended."
              />
              <MetricChart namespace={namespace} flowId={flowId} />
            </div>
          </div>
        );
      }}
    </DataState>
  );
}

function MetricChart({ namespace, flowId }: { namespace: string; flowId: string }) {
  const [name, setName] = useState("");
  const [agg, setAgg] = useState<"sum" | "avg" | "max">("sum");
  const [groupBy, setGroupBy] = useState("");
  const names = useQuery({ ...getFlowMetricsOptions({ path: { namespace, flowId } }), select: (d) => d.names });
  const metric = name || names.data?.[0] || "";
  const data = useQuery({
    ...getFlowMetricsOptions({
      path: { namespace, flowId },
      query: { name: metric, agg, group_by: groupBy.trim() || undefined },
    }),
    enabled: metric !== "",
  });

  const series = (data.data?.series ?? []).map((s, i) => ({
    key: `g${i}`,
    label: s.group || (groupBy.trim() ? "(no tag)" : metric),
    color: chartColors[i % chartColors.length] ?? "var(--accent)",
  }));
  const byExecution = new Map<string, ChartRow>();
  (data.data?.series ?? []).forEach((s, i) => {
    for (const p of s.points) {
      const row = byExecution.get(p.execution_id) ?? ({ label: shortTime(p.created_at), _t: p.created_at } as ChartRow);
      row[`g${i}`] = p.value;
      byExecution.set(p.execution_id, row);
    }
  });
  const rows = [...byExecution.values()].sort((a, b) => String(a._t).localeCompare(String(b._t)));

  const noMetrics = names.isSuccess && (names.data ?? []).length === 0;
  return (
    <LineChart
      title={metric ? `Metric ${metric}` : "Custom metric"}
      rows={rows}
      series={series}
      emptyText={noMetrics ? "This flow reports no metrics." : "No data for this metric."}
      toolbar={
        <div className="flex flex-wrap items-center gap-1.5">
          <Select
            aria-label="Metric"
            className="h-7 w-auto max-w-40 text-xs"
            value={metric}
            onChange={(e) => setName(e.target.value)}
            disabled={(names.data ?? []).length === 0}
          >
            {(names.data ?? []).length === 0 && <option value="">No metrics</option>}
            {(names.data ?? []).map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </Select>
          <SegmentedControl label="Aggregation" size="sm" options={aggregations} value={agg} onChange={setAgg} />
          <Input
            aria-label="Group by tag"
            value={groupBy}
            onChange={(e) => setGroupBy(e.target.value)}
            placeholder="Group by tag"
            className="h-7 w-28 text-xs"
          />
        </div>
      }
    />
  );
}
