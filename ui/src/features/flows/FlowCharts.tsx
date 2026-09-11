import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { AlertTriangle, CheckCircle2, CircleDashed, CirclePlay, XCircle, type LucideIcon } from "lucide-react";
import { useState } from "react";
import { getFlowMetricsOptions, getFlowStatsOptions } from "@/api/@tanstack/react-query.gen";
import { chartColors, LineChart, type ChartRow } from "@/components/charts";
import { DataState } from "@/components/data-state";
import { Input, Select } from "@/components/ui/input";
import { formatMs, stateColor } from "@/lib/charts";
import { stateLabel } from "@/lib/flows";
import { formatTime } from "@/lib/utils";

const stateIcons: Record<string, LucideIcon> = {
  success: CheckCircle2,
  failed: XCircle,
  timed_out: AlertTriangle,
  running: CirclePlay,
  cancelling: CirclePlay,
};

function shortTime(iso: string): string {
  return new Date(iso).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}

/** FlowCharts shows the last 50 execution states, their durations and a custom metric chart (REQ-UI-006). */
export function FlowCharts({ namespace, flowId }: { namespace: string; flowId: string }) {
  const stats = useQuery({ ...getFlowStatsOptions({ path: { namespace, flowId } }), refetchInterval: 5000 });
  return (
    <DataState query={stats}>
      {(s) => {
        const oldestFirst = [...s.recent].reverse();
        const durationRows: ChartRow[] = oldestFirst
          .filter((x) => x.duration_ms !== null && x.duration_ms !== undefined)
          .map((x) => ({ label: shortTime(x.created_at), duration: x.duration_ms ?? null }));
        return (
          <div className="flex flex-col gap-4">
            <section aria-labelledby="strip-heading" className="flex flex-col gap-2">
              <h2 id="strip-heading" className="text-base font-semibold">
                Last {s.recent.length} executions
              </h2>
              {s.recent.length === 0 ? (
                <p className="text-sm text-muted-foreground">The flow has no executions.</p>
              ) : (
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
                          className="flex h-6 w-6 items-center justify-center rounded-[4px] hover:bg-muted"
                        >
                          <Icon className="h-4 w-4" style={{ color: stateColor(state) }} aria-hidden />
                        </Link>
                      </li>
                    );
                  })}
                </ol>
              )}
            </section>
            <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
              <LineChart
                title="Duration of the last executions"
                rows={durationRows}
                series={[{ key: "duration", label: "Duration", color: "var(--accent)" }]}
                format={formatMs}
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
    ...getFlowMetricsOptions({ path: { namespace, flowId }, query: { name: metric, agg, group_by: groupBy.trim() || undefined } }),
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

  return (
    <div className="flex min-w-0 flex-col gap-2">
      <div className="flex flex-wrap items-end gap-2">
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          Metric
          <Select value={metric} onChange={(e) => setName(e.target.value)} disabled={(names.data ?? []).length === 0}>
            {(names.data ?? []).length === 0 && <option value="">No metrics</option>}
            {(names.data ?? []).map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </Select>
        </label>
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          Aggregation
          <Select value={agg} onChange={(e) => setAgg(e.target.value as typeof agg)}>
            <option value="sum">Sum</option>
            <option value="avg">Average</option>
            <option value="max">Maximum</option>
          </Select>
        </label>
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          Group by tag
          <Input value={groupBy} onChange={(e) => setGroupBy(e.target.value)} placeholder="Tag key" className="w-32" />
        </label>
      </div>
      <LineChart title={metric ? `Metric ${metric}` : "Custom metric"} rows={rows} series={series} />
    </div>
  );
}
