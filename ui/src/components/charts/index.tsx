import { lazy, Suspense } from "react";
import type { ChartRow, Series } from "./charts-impl";

export type { ChartRow, Series } from "./charts-impl";

// Recharts loads lazily, so that the initial route JavaScript stays small (NFR-003).
const StackedImpl = lazy(() => import("./charts-impl").then((m) => ({ default: m.StackedBars })));
const LinesImpl = lazy(() => import("./charts-impl").then((m) => ({ default: m.Lines })));

/** chartColors are series colors for groups without a state color. */
export const chartColors = [
  "var(--accent)",
  "var(--state-success)",
  "var(--state-timed-out)",
  "var(--state-failed)",
  "var(--state-cancelled)",
  "#8b5cf6",
  "#0ea5e9",
  "#d946ef",
];

function ChartFrame({
  title,
  rows,
  series,
  format,
  children,
}: {
  title: string;
  rows: ChartRow[];
  series: Series[];
  format?: (v: number) => string;
  children: React.ReactNode;
}) {
  return (
    <figure className="flex min-w-0 flex-col gap-2 rounded-[8px] border bg-panel p-3">
      <figcaption className="text-sm font-medium">{title}</figcaption>
      <div aria-hidden className="min-w-0">
        {rows.length === 0 ? (
          <p className="flex h-[220px] items-center justify-center text-sm text-muted-foreground">
            No data in this range.
          </p>
        ) : (
          <Suspense fallback={<div className="h-[220px] animate-pulse rounded-[6px] bg-muted" />}>{children}</Suspense>
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

/** LineChart is a line chart with a data table. */
export function LineChart({
  title,
  rows,
  series,
  format,
}: {
  title: string;
  rows: ChartRow[];
  series: Series[];
  format?: (v: number) => string;
}) {
  return (
    <ChartFrame title={title} rows={rows} series={series} format={format}>
      <LinesImpl rows={rows} series={series} />
    </ChartFrame>
  );
}
