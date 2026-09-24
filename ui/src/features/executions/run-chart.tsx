import { useNavigate } from "@tanstack/react-router";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { ExecutionSummary } from "@/api/types.gen";
import { executionTitle, isTerminal } from "@/lib/executions";
import { stateLabel } from "@/lib/flows";
import { cn, formatDuration, formatTime } from "@/lib/utils";

const height = 148;
const pad = { top: 10, right: 12, bottom: 22, left: 52 };

const stateVar: Record<string, string> = {
  SUCCESS: "var(--state-success)",
  FAILED: "var(--state-failed)",
  RUNNING: "var(--state-running)",
  CANCELLING: "var(--state-running)",
  TIMED_OUT: "var(--state-timed-out)",
  CANCELLED: "var(--state-cancelled)",
  SKIPPED: "var(--state-skipped)",
  QUEUED: "var(--state-queued)",
};

const legendOrder = ["SUCCESS", "FAILED", "TIMED_OUT", "RUNNING", "QUEUED", "CANCELLED", "SKIPPED"];

type Point = { e: ExecutionSummary; x: number; y: number; ms: number };

/** tickLabel formats a gridline duration without trailing zero parts, for example "1 min". */
function tickLabel(ms: number): string {
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60_000) return `${ms / 1000} s`;
  if (ms < 3_600_000) return `${ms / 60_000} min`;
  return `${ms / 3_600_000} h`;
}

/** logTicks returns durations for the gridlines of a log axis between lo and hi. */
function logTicks(lo: number, hi: number): number[] {
  const out: number[] = [];
  for (const t of [10, 100, 1_000, 10_000, 60_000, 600_000, 3_600_000, 36_000_000]) {
    if (t >= lo && t <= hi) out.push(t);
  }
  return out.slice(-4);
}

/**
 * RunChart plots the loaded executions: time on x, duration on a log scale on y, one dot
 * per execution in its state color. A click on a dot opens that execution. Running
 * executions rise while they run.
 */
export function RunChart({ items, now }: { items: ExecutionSummary[]; now: number }) {
  const navigate = useNavigate();
  const ref = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(0);
  const [hover, setHover] = useState<Point>();
  // Dots move with a transition only after the first placement, so they do not fly in from a corner.
  const [ready, setReady] = useState(false);
  useEffect(() => {
    if (width === 0) return;
    const f = requestAnimationFrame(() => setReady(true));
    return () => cancelAnimationFrame(f);
  }, [width]);

  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    setWidth(el.clientWidth);
    const ro = new ResizeObserver(() => setWidth(el.clientWidth));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const model = useMemo(() => {
    const rows = items
      .map((e) => {
        const started = e.started_at ? Date.parse(e.started_at) : undefined;
        const ms = e.duration_ms ?? (started !== undefined && !isTerminal(e.state) ? now - started : null);
        return { e, t: Date.parse(e.created_at), ms };
      })
      .filter((r): r is { e: ExecutionSummary; t: number; ms: number } => r.ms !== null && Number.isFinite(r.t));
    if (rows.length === 0 || width === 0) return undefined;
    const times = rows.map((r) => r.t).sort((a, b) => a - b);
    const t1 = times[times.length - 1]!;
    // A few old runs would squeeze the rest into the right edge. When the oldest tenth spans
    // most of the axis, the axis starts at the tenth percentile and older runs sit at the left edge.
    const tq = times[Math.floor(times.length / 10)]!;
    const clipped = times.length >= 10 && t1 - times[0]! > 4 * (t1 - tq);
    const t0 = clipped ? tq - (t1 - tq) * 0.04 : times[0]!;
    const tSpan = Math.max(t1 - t0, 60_000);
    const lo = Math.max(1, Math.min(...rows.map((r) => r.ms)) / 2);
    const hi = Math.max(lo * 10, Math.max(...rows.map((r) => r.ms)) * 1.6);
    const plotW = width - pad.left - pad.right;
    const plotH = height - pad.top - pad.bottom;
    const x = (t: number) => pad.left + Math.max(0, (t - (t1 - tSpan)) / tSpan) * plotW;
    const y = (ms: number) =>
      pad.top + plotH - ((Math.log10(Math.max(ms, lo)) - Math.log10(lo)) / (Math.log10(hi) - Math.log10(lo))) * plotH;
    const points: Point[] = rows.map((r) => ({ e: r.e, ms: r.ms, x: x(r.t), y: y(r.ms) }));
    const xTicks = [0, 1 / 3, 2 / 3, 1].map((f) => t1 - tSpan + f * tSpan);
    return {
      clipped,
      points,
      yTicks: logTicks(lo, hi).map((v) => ({ v, y: y(v) })),
      xTicks: xTicks.map((t) => ({ t, x: x(t) })),
      tSpan,
    };
  }, [items, now, width]);

  const counts = useMemo(() => {
    const c = new Map<string, number>();
    for (const e of items) c.set(e.state, (c.get(e.state) ?? 0) + 1);
    return legendOrder.filter((s) => c.has(s)).map((s) => ({ state: s, n: c.get(s)! }));
  }, [items]);

  const summary = counts.map((c) => `${c.n} ${stateLabel(c.state.toLowerCase()).toLowerCase()}`).join(", ");
  const timeLabel = (t: number) =>
    model && model.tSpan > 36 * 3_600_000
      ? new Date(t).toLocaleDateString(undefined, { month: "short", day: "numeric" })
      : new Date(t).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });

  return (
    <figure className="flex min-w-0 animate-enter flex-col gap-1 rounded-panel border bg-panel px-4 pt-3 pb-2 shadow-panel">
      <figcaption className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1">
        <span className="text-sm font-semibold">Duration over time</span>
        <span className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
          <span className="sr-only">Loaded executions: {summary}.</span>
          {counts.map((c) => (
            <span key={c.state} aria-hidden className="flex items-center gap-1.5">
              <span className="h-2 w-2 rounded-full" style={{ background: stateVar[c.state] }} />
              {stateLabel(c.state.toLowerCase())}
              <span className="tabular-nums text-foreground">{c.n}</span>
            </span>
          ))}
        </span>
      </figcaption>
      <div ref={ref} aria-hidden className="relative" style={{ height }} onMouseLeave={() => setHover(undefined)}>
        {model && (
          <svg width={width} height={height} className="block overflow-visible">
            {model.yTicks.map((t) => (
              <g key={t.v}>
                <line
                  x1={pad.left}
                  x2={width - pad.right}
                  y1={t.y}
                  y2={t.y}
                  stroke="var(--border)"
                  strokeDasharray="2 3"
                />
                <text
                  x={pad.left - 8}
                  y={t.y}
                  dy="0.32em"
                  textAnchor="end"
                  fontSize={11}
                  fill="var(--muted-foreground)"
                >
                  {tickLabel(t.v)}
                </text>
              </g>
            ))}
            {model.clipped && (
              <text x={pad.left} y={pad.top + 2} dx={-4} textAnchor="end" fontSize={11} fill="var(--muted-foreground)">
                older
              </text>
            )}
            {model.xTicks.map((t, i) => (
              <text
                key={i}
                x={t.x}
                y={height - 6}
                textAnchor={i === 0 ? "start" : i === model.xTicks.length - 1 ? "end" : "middle"}
                fontSize={11}
                fill="var(--muted-foreground)"
              >
                {timeLabel(t.t)}
              </text>
            ))}
            {model.points.map((p) => {
              const active = hover?.e.id === p.e.id;
              const running = !isTerminal(p.e.state);
              return (
                <g
                  key={p.e.id}
                  className={cn("cursor-pointer", ready && "transition-transform duration-700 ease-smooth")}
                  style={{ transform: `translate(${p.x}px, ${p.y}px)` }}
                  onMouseEnter={() => setHover(p)}
                  onClick={() => void navigate({ to: "/executions/$executionId", params: { executionId: p.e.id } })}
                >
                  <circle r={10} fill="transparent" />
                  {running && <circle r={7} fill={stateVar[p.e.state]} opacity={0.25} className="animate-breathe" />}
                  <circle
                    r={active ? 5.5 : 4}
                    fill={stateVar[p.e.state] ?? "var(--state-queued)"}
                    stroke="var(--panel)"
                    strokeWidth={1.5}
                    className="origin-center animate-pop-in transition-[r] duration-300 ease-snappy [transform-box:fill-box]"
                  />
                </g>
              );
            })}
          </svg>
        )}
        {hover && (
          <div
            className="pointer-events-none absolute z-10 w-max max-w-64 animate-pop-in rounded-control border bg-panel/95 px-2.5 py-1.5 text-xs shadow-float backdrop-blur-xl"
            style={{
              left: Math.min(Math.max(hover.x - 60, 0), Math.max(width - 200, 0)),
              top: hover.y > height / 2 ? hover.y - 64 : hover.y + 12,
            }}
          >
            <div className="truncate font-medium">
              {hover.e.namespace}/
              {executionTitle(hover.e as typeof hover.e & { trigger_payload?: Record<string, unknown> }).title}
            </div>
            <div className="flex items-center gap-1.5 text-muted-foreground">
              <span className="h-1.5 w-1.5 rounded-full" style={{ background: stateVar[hover.e.state] }} />
              {stateLabel(hover.e.state.toLowerCase())} ·{" "}
              <span className="tabular-nums">{formatDuration(hover.ms)}</span>
            </div>
            <div className="text-muted-foreground">{formatTime(hover.e.created_at)}</div>
          </div>
        )}
        {!model && width > 0 && (
          <p className={cn("flex h-full items-center justify-center text-sm text-muted-foreground")}>
            No durations to plot yet.
          </p>
        )}
      </div>
    </figure>
  );
}
