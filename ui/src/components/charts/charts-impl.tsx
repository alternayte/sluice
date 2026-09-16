import {
  Bar,
  BarChart,
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { needsDots } from "@/lib/charts";

/** Series is one bar or line of a chart. */
export type Series = { key: string; label: string; color: string };

/** ChartRow is one x position: a label and one value per series key. */
export type ChartRow = { label: string } & Record<string, number | string | null>;

const axis = { stroke: "var(--muted-foreground)", fontSize: 11 };
const tooltipStyle = { background: "var(--panel)", border: "1px solid var(--border)", borderRadius: 6, fontSize: 12 };

/** StackedBars draws stacked bars, for example executions per bucket by state. */
export function StackedBars({ rows, series }: { rows: ChartRow[]; series: Series[] }) {
  return (
    <ResponsiveContainer width="100%" height={220}>
      {/* The chart is aria-hidden; its data table serves screen readers, so the chart takes no focus. */}
      <BarChart data={rows} margin={{ top: 8, right: 8, bottom: 0, left: -16 }} accessibilityLayer={false}>
        <CartesianGrid stroke="var(--border)" vertical={false} />
        <XAxis dataKey="label" tick={axis} tickLine={false} axisLine={false} minTickGap={16} />
        <YAxis tick={axis} tickLine={false} axisLine={false} allowDecimals={false} />
        <Tooltip contentStyle={tooltipStyle} cursor={{ fill: "var(--muted)" }} isAnimationActive={false} />
        <Legend wrapperStyle={{ fontSize: 12 }} />
        {series.map((s) => (
          <Bar key={s.key} dataKey={s.key} name={s.label} stackId="a" fill={s.color} isAnimationActive={false} />
        ))}
      </BarChart>
    </ResponsiveContainer>
  );
}

/** Lines draws one line per series, for example duration percentiles or metric groups. */
export function Lines({ rows, series }: { rows: ChartRow[]; series: Series[] }) {
  return (
    <ResponsiveContainer width="100%" height={220}>
      <LineChart data={rows} margin={{ top: 8, right: 8, bottom: 0, left: -8 }} accessibilityLayer={false}>
        <CartesianGrid stroke="var(--border)" vertical={false} />
        <XAxis dataKey="label" tick={axis} tickLine={false} axisLine={false} minTickGap={16} />
        <YAxis tick={axis} tickLine={false} axisLine={false} />
        <Tooltip contentStyle={tooltipStyle} isAnimationActive={false} />
        <Legend wrapperStyle={{ fontSize: 12 }} />
        {/* A line with one point draws no segment. Such a series shows dots, so that one bucket is visible. */}
        {series.map((s) => (
          <Line
            key={s.key}
            dataKey={s.key}
            name={s.label}
            stroke={s.color}
            dot={needsDots(rows, s.key) ? { r: 3, fill: s.color, strokeWidth: 0 } : false}
            strokeWidth={2}
            connectNulls
            isAnimationActive={false}
          />
        ))}
      </LineChart>
    </ResponsiveContainer>
  );
}
