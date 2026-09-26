import {
  Bar,
  BarChart,
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
  type BarShapeProps,
  type TooltipContentProps,
} from "recharts";
import { needsDots } from "@/lib/charts";

/** Series is one bar or line of a chart. */
export type Series = { key: string; label: string; color: string };

/** ChartRow is one x position: a label and one value per series key. */
export type ChartRow = { label: string } & Record<string, number | string | null>;

const height = 220;
const axis = { fill: "var(--muted-foreground)", fontSize: 12 };
const grid = { stroke: "var(--border)", strokeWidth: 0.5 };
// Axis ticks without a unit format use compact numbers, for example 100K; the tooltip shows the exact value.
const compact = new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 1 });
const compactTick = (v: number) => compact.format(v);

/** ChartTooltip is the hover card of a chart, in the look of a macOS popover. */
function ChartTooltip({
  active,
  payload,
  label,
  format,
  hideZero,
}: Pick<TooltipContentProps, "active" | "payload" | "label"> & {
  format?: (v: number) => string;
  /** hideZero leaves out series without a value in this bucket, for stacked counts. */
  hideZero?: boolean;
}) {
  if (!active || !payload?.length) return null;
  const items = payload.filter((p) => typeof p.value === "number" && !(hideZero && p.value === 0));
  return (
    <div
      className="flex min-w-36 flex-col gap-1 rounded-control border bg-panel/95 px-2.5 py-2 text-xs backdrop-blur-xl"
      style={{ boxShadow: "var(--shadow-float)" }}
    >
      <p className="font-medium text-foreground">{label}</p>
      {items.length === 0 && <p className="text-muted-foreground">No value</p>}
      {items.map((p) => (
        <p key={String(p.dataKey)} className="flex items-center gap-2">
          <span aria-hidden className="h-2 w-2 shrink-0 rounded-full" style={{ background: p.color }} />
          <span className="text-muted-foreground">{p.name}</span>
          <span className="ml-auto pl-3 font-medium text-foreground tabular-nums">
            {format ? format(p.value as number) : String(p.value)}
          </span>
        </p>
      ))}
    </div>
  );
}

/** topKeys returns, per row, the key of the highest non-empty segment of the stack. */
function topKeys(rows: ChartRow[], series: Series[]): (string | undefined)[] {
  return rows.map((r) => {
    for (let i = series.length - 1; i >= 0; i--) {
      const s = series[i];
      const v = s ? r[s.key] : undefined;
      if (s && typeof v === "number" && v > 0) return s.key;
    }
    return undefined;
  });
}

/**
 * segment draws one piece of a stacked bar. A hairline gap in the panel color separates the
 * pieces, and the top piece of a stack has rounded corners.
 */
function segment(top: boolean) {
  return function StackSegment({ x, y, width, height: h, fill }: BarShapeProps) {
    if (!h || h <= 0 || !width) return <g />;
    const gap = top || h <= 3 ? 0 : 1.5;
    const hh = h - gap;
    const yy = y + gap;
    const r = top ? Math.min(3, hh, width / 2) : 0;
    const d = `M${x},${yy + hh} V${yy + r} Q${x},${yy} ${x + r},${yy} H${x + width - r} Q${x + width},${yy} ${x + width},${yy + r} V${yy + hh} Z`;
    return <path d={d} fill={fill} />;
  };
}

/**
 * indexed gives each row its position as the x key. Two rows can share a label (two
 * executions in the same minute); a label as the x key would put the hover dots on the first.
 */
function indexed(rows: ChartRow[]) {
  const data = rows.map((r, i) => ({ ...r, _x: i }));
  const labelAt = (i: unknown) => rows[Number(i)]?.label ?? "";
  return { data, labelAt };
}

/** StackedBars draws stacked bars, for example executions per bucket by state. */
export function StackedBars({
  rows,
  series,
  format,
}: {
  rows: ChartRow[];
  series: Series[];
  format?: (v: number) => string;
}) {
  const tops = topKeys(rows, series);
  const { data, labelAt } = indexed(rows);
  const flat = segment(false);
  const round = segment(true);
  return (
    <ResponsiveContainer width="100%" height={height}>
      {/* The chart is aria-hidden; its data table serves screen readers, so the chart takes no focus. */}
      <BarChart
        data={data}
        margin={{ top: 8, right: 4, bottom: 0, left: -20 }}
        barCategoryGap="22%"
        accessibilityLayer={false}
      >
        <CartesianGrid {...grid} vertical={false} />
        <XAxis
          dataKey="_x"
          tick={axis}
          tickFormatter={labelAt}
          tickLine={false}
          axisLine={false}
          minTickGap={16}
          tickMargin={6}
        />
        <YAxis
          tick={axis}
          tickLine={false}
          axisLine={false}
          allowDecimals={false}
          width={48}
          tickFormatter={compactTick}
        />
        <Tooltip
          content={(p) => (
            <ChartTooltip active={p.active} payload={p.payload} label={labelAt(p.label)} format={format} hideZero />
          )}
          cursor={{ fill: "var(--muted)", opacity: 0.6 }}
          isAnimationActive={false}
        />
        {series.map((s) => (
          <Bar
            key={s.key}
            dataKey={s.key}
            name={s.label}
            stackId="a"
            fill={s.color}
            maxBarSize={28}
            isAnimationActive={false}
            shape={(p: BarShapeProps) => (tops[p.index] === s.key ? round(p) : flat(p))}
          />
        ))}
      </BarChart>
    </ResponsiveContainer>
  );
}

/** Lines draws one line per series, for example duration percentiles or metric groups. */
export function Lines({
  rows,
  series,
  format,
}: {
  rows: ChartRow[];
  series: Series[];
  format?: (v: number) => string;
}) {
  const { data, labelAt } = indexed(rows);
  return (
    <ResponsiveContainer width="100%" height={height}>
      <LineChart data={data} margin={{ top: 8, right: 8, bottom: 0, left: -4 }} accessibilityLayer={false}>
        <CartesianGrid {...grid} vertical={false} />
        <XAxis
          dataKey="_x"
          tick={axis}
          tickFormatter={labelAt}
          tickLine={false}
          axisLine={false}
          minTickGap={24}
          tickMargin={6}
        />
        <YAxis
          tick={axis}
          tickLine={false}
          axisLine={false}
          width={format ? 64 : 48}
          tickFormatter={format ?? compactTick}
        />
        <Tooltip
          content={(p) => (
            <ChartTooltip active={p.active} payload={p.payload} label={labelAt(p.label)} format={format} />
          )}
          cursor={{ stroke: "var(--input)", strokeWidth: 1 }}
          isAnimationActive={false}
        />
        {/* A line with one point draws no segment. Such a series shows dots, so that one bucket is visible. */}
        {series.map((s) => (
          <Line
            key={s.key}
            dataKey={s.key}
            name={s.label}
            stroke={s.color}
            dot={needsDots(rows, s.key) ? { r: 3, fill: s.color, strokeWidth: 0 } : false}
            activeDot={{ r: 4, fill: s.color, stroke: "var(--panel)", strokeWidth: 2 }}
            strokeWidth={2}
            strokeLinecap="round"
            strokeLinejoin="round"
            connectNulls
            isAnimationActive={false}
          />
        ))}
      </LineChart>
    </ResponsiveContainer>
  );
}
