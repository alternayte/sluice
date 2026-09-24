import { ChartNoAxesColumn } from "lucide-react";
import { lazy, Suspense, type ReactNode } from "react";
import type { ChartRow, Series } from "./charts-impl";

export type { ChartRow, Series } from "./charts-impl";

// Recharts loads lazily, so that the initial route JavaScript stays small (NFR-003).
const StackedImpl = lazy(() => import("./charts-impl").then((m) => ({ default: m.StackedBars })));
const LinesImpl = lazy(() => import("./charts-impl").then((m) => ({ default: m.Lines })));

/**
 * chartColors are series colors for groups without a state color, in a fixed order: the
 * accent, two state hues, then Apple system teal, purple, pink and indigo. light-dark()
 * picks the system hue of the current theme. The order keeps neighbours apart for color
 * vision deficiencies.
 */
export const chartColors = [
  "var(--accent)",
  "var(--state-timed-out)",
  "light-dark(#30b0c7, #40c8e0)",
  "light-dark(#af52de, #bf5af2)",
  "var(--state-success)",
  "light-dark(#ff2d55, #ff375f)",
  "light-dark(#5856d6, #5e5ce6)",
];

function ChartFrame({
  title,
  rows,
  series,
  format,
  emptyText = "No data in this range.",
  toolbar,
  children,
}: {
  title: string;
  rows: ChartRow[];
  series: Series[];
  format?: (v: number) => string;
  emptyText?: string;
  toolbar?: ReactNode;
  children: ReactNode;
}) {
  return (
    <figure className="flex min-w-0 flex-col gap-3 rounded-panel border bg-panel p-4 shadow-panel">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <figcaption className="text-sm font-semibold">{title}</figcaption>
        {toolbar}
      </div>
      <div aria-hidden className="flex min-w-0 flex-col gap-2">
        {rows.length === 0 ? (
          <div className="flex h-[220px] flex-col items-center justify-center gap-2 text-center">
            <span className="flex h-9 w-9 items-center justify-center rounded-full bg-muted text-muted-foreground">
              <ChartNoAxesColumn className="h-4 w-4" />
            </span>
            <p className="text-sm text-muted-foreground">{emptyText}</p>
          </div>
        ) : (
          <>
            <Suspense fallback={<div className="skeleton h-[220px] rounded-control" />}>{children}</Suspense>
            {/* One series needs no legend: the title names it. */}
            {series.length > 1 && (
              <ul className="flex flex-wrap justify-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
                {series.map((s) => (
                  <li key={s.key} className="flex items-center gap-1.5">
                    <span className="h-2 w-2 rounded-full" style={{ background: s.color }} />
                    {s.label}
                  </li>
                ))}
              </ul>
            )}
          </>
        )}
      </div>
      {/* The data as a table, for screen readers and exact values. A table ignores the 1 px
          width of sr-only, so a wrapper clips it. */}
      <div className="sr-only">
        <table aria-label={`${title} data`}>
          <thead>
            <tr>
              <th scope="col">Label</th>
              {series.map((s) => (
                <th key={s.key} scope="col">
                  {s.label}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((r, i) => (
              <tr key={i}>
                <th scope="row">{r.label}</th>
                {series.map((s) => {
                  const v = r[s.key];
                  return <td key={s.key}>{typeof v === "number" ? (format ? format(v) : String(v)) : "—"}</td>;
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </figure>
  );
}

/** StackedChart is a stacked bar chart with a data table. */
export function StackedChart({ title, rows, series }: { title: string; rows: ChartRow[]; series: Series[] }) {
  return (
    <ChartFrame title={title} rows={rows} series={series}>
      <StackedImpl rows={rows} series={series} />
    </ChartFrame>
  );
}

/** LineChart is a line chart with a data table. A toolbar sits next to the title. */
export function LineChart({
  title,
  rows,
  series,
  format,
  emptyText,
  toolbar,
}: {
  title: string;
  rows: ChartRow[];
  series: Series[];
  format?: (v: number) => string;
  emptyText?: string;
  toolbar?: ReactNode;
}) {
  return (
    <ChartFrame title={title} rows={rows} series={series} format={format} emptyText={emptyText} toolbar={toolbar}>
      <LinesImpl rows={rows} series={series} format={format} />
    </ChartFrame>
  );
}
