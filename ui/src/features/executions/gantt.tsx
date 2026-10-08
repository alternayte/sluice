import { ChevronRight, Recycle } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { EmptyState } from "@/components/data-state";
import { ExecutionStateBadge } from "@/components/state-badges";
import { axisTicks, ganttLayout, isTerminal, stateBarClass, type GanttRow, type GanttRun } from "@/lib/executions";
import { stateLabel } from "@/lib/flows";
import { cn, formatDuration } from "@/lib/utils";

const columns = "grid grid-cols-[minmax(7rem,11rem)_minmax(0,1fr)_minmax(6.5rem,auto)] gap-x-3";

/**
 * Gantt is the waterfall of an execution: one row per task run attempt on a shared time axis
 * (D-17). The wait between queued and started shows as a faint segment. A click on a row
 * selects its task for the inspector. Bars grow in place while the execution runs.
 * The items of a task with each sit under one group row. A click on it shows or hides them.
 */
export function Gantt({
  runs,
  now,
  live = false,
  selected,
  onSelect,
}: {
  runs: GanttRun[];
  now: number;
  live?: boolean;
  /** selected is the id of the selected task run. */
  selected?: string;
  onSelect?: (run: GanttRow) => void;
}) {
  // The number of ticks follows the width of the axis, so the labels never overlap.
  const axisRef = useRef<HTMLSpanElement>(null);
  const [axisWidth, setAxisWidth] = useState(0);
  const [open, setOpen] = useState<ReadonlySet<string>>(new Set());
  const empty = runs.length === 0;
  useLayoutEffect(() => {
    const el = axisRef.current;
    if (!el) return;
    setAxisWidth(el.clientWidth);
    const ro = new ResizeObserver(() => setAxisWidth(el.clientWidth));
    ro.observe(el);
    return () => ro.disconnect();
  }, [empty]);

  if (empty) {
    return <EmptyState text="No task runs yet." className="py-8" />;
  }
  const { rows: all, start, end } = ganttLayout(runs, now);
  // A group shows its items when it is open, or when one of its items is selected.
  const selectedTask = all.find((r) => r.id === selected && r.item !== undefined)?.taskKey;
  const isOpen = (task: string) => open.has(task) || task === selectedTask;
  const rows = all.filter((r) => r.item === undefined || isOpen(r.taskKey));
  const toggle = (task: string) =>
    setOpen((prev) => {
      const next = new Set(prev);
      if (!next.delete(task)) next.add(task);
      return next;
    });
  const span = end - start;
  const ticks = axisTicks(span, Math.max(1, Math.min(4, Math.floor(axisWidth / 110))));
  return (
    <div data-testid="gantt" className="min-w-0 text-sm">
      <div className={cn(columns, "h-7 items-center border-b px-3 text-xs text-muted-foreground")}>
        <span>Task</span>
        <span ref={axisRef} className="relative h-full overflow-hidden">
          {ticks
            .filter((t) => (t === 0 && axisWidth >= 70) || (t > 0 && span > 0 && t / span < 0.82))
            .map((t) => (
              <span
                key={t}
                className="absolute top-1/2 tabular-nums whitespace-nowrap"
                style={{
                  left: `${span > 0 ? (t / span) * 100 : 0}%`,
                  transform: t === 0 ? "translateY(-50%)" : "translate(-50%, -50%)",
                }}
              >
                {t === 0 ? new Date(start).toLocaleTimeString() : `+${formatDuration(t)}`}
              </span>
            ))}
        </span>
        <span className="text-right">Duration</span>
      </div>
      <ul className="relative flex flex-col py-1">
        {rows.map((r) => (
          <Row
            key={r.id}
            row={r}
            live={live}
            ticks={ticks}
            span={span}
            selected={selected === r.id}
            expanded={r.group ? isOpen(r.taskKey) : undefined}
            onSelect={r.group ? () => toggle(r.taskKey) : onSelect}
          />
        ))}
      </ul>
    </div>
  );
}

function Row({
  row: r,
  live,
  ticks,
  span,
  selected,
  expanded,
  onSelect,
}: {
  row: GanttRow;
  live: boolean;
  ticks: number[];
  span: number;
  selected: boolean;
  /** expanded is set on a group row: whether its items show. */
  expanded?: boolean;
  onSelect?: (run: GanttRow) => void;
}) {
  const ref = useRef<HTMLLIElement>(null);
  const prevState = useRef(r.state);
  const duration = r.group ? `${r.group.done}/${r.group.count} items` : formatDuration(r.durationMs);
  const running = !isTerminal(r.state) && r.started;

  // A state change after the first render flashes the row once.
  useEffect(() => {
    if (prevState.current === r.state) return;
    prevState.current = r.state;
    const el = ref.current;
    if (!el) return;
    el.classList.remove("animate-flash");
    void el.offsetWidth;
    el.classList.add("animate-flash");
  }, [r.state]);

  useEffect(() => {
    if (selected) ref.current?.scrollIntoView({ block: "nearest" });
  }, [selected]);

  return (
    <li
      ref={ref}
      data-nav-row
      data-run-id={r.id}
      className={cn(
        "group relative mx-1 rounded-control transition-colors duration-100",
        selected ? "bg-accent-soft" : "hover:bg-muted/60",
      )}
    >
      <button
        type="button"
        data-nav-default
        aria-pressed={r.group ? undefined : selected}
        aria-expanded={expanded}
        aria-label={`${r.label}, ${stateLabel(r.state.toLowerCase())}, ${duration}`}
        onClick={() => onSelect?.(r)}
        className={cn(columns, "h-9 w-full items-center rounded-control px-2 text-left outline-offset-[-2px]")}
      >
        <span className={cn("flex min-w-0 items-center gap-1.5", r.item !== undefined && "pl-4")}>
          {r.group && (
            <ChevronRight
              aria-hidden
              className={cn("h-3 w-3 shrink-0 text-muted-foreground transition-transform", expanded && "rotate-90")}
            />
          )}
          <span className={cn("truncate font-mono text-xs", selected && "font-medium")} title={r.label}>
            {r.label}
          </span>
          {r.reused && (
            <span className="inline-flex shrink-0 items-center gap-0.5 rounded-inner bg-muted px-1 text-xs text-muted-foreground">
              <Recycle className="h-3 w-3" aria-hidden />
              reused
            </span>
          )}
        </span>
        <span className="relative h-full">
          {ticks.slice(1).map((t) => (
            <span
              key={t}
              aria-hidden
              className="absolute inset-y-0 w-px bg-border/70"
              style={{ left: `${span > 0 ? (t / span) * 100 : 0}%` }}
            />
          ))}
          <span aria-hidden className="absolute inset-x-0 top-1/2 h-px -translate-y-1/2 bg-border/60" />
          {r.waitWidth > 0 && (
            <span
              aria-hidden
              title={`${r.label} waited in the queue`}
              className={cn(
                "absolute top-1/2 h-1.5 -translate-y-1/2 rounded-full bg-state-queued/35",
                live
                  ? "transition-[left,width] duration-1000 ease-linear"
                  : "transition-[left,width] duration-500 ease-smooth",
              )}
              style={{ left: `${r.waitLeft}%`, width: `${r.waitWidth}%` }}
            />
          )}
          {r.started && (
            <span
              data-testid="gantt-bar"
              title={`${r.label}, ${stateLabel(r.state.toLowerCase())}, ${duration}`}
              className={cn(
                "absolute top-1/2 h-2.5 -translate-y-1/2 rounded-full shadow-[inset_0_0_0_0.5px_rgb(0_0_0/0.12)]",
                stateBarClass(r.state),
                running && "bar-running",
                live
                  ? "transition-[left,width,background-color] duration-1000 ease-linear"
                  : "transition-[left,width,background-color] duration-500 ease-smooth",
              )}
              style={{ left: `${r.left}%`, width: `max(${r.width}%, 6px)` }}
            />
          )}
        </span>
        <span className="flex min-w-0 items-center justify-end gap-2 text-xs">
          <span className="tabular-nums text-muted-foreground">{duration}</span>
          <ExecutionStateBadge state={r.state} />
        </span>
      </button>
    </li>
  );
}
