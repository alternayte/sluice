import { Recycle } from "lucide-react";
import { ExecutionStateBadge } from "@/components/state-badges";
import { ganttLayout, stateBarClass, type GanttRun } from "@/lib/executions";
import { stateLabel } from "@/lib/flows";
import { cn, formatDuration } from "@/lib/utils";

/** Gantt shows one bar per task run attempt on a shared time axis (D-17). */
export function Gantt({ runs, now }: { runs: GanttRun[]; now: number }) {
  if (runs.length === 0) {
    return <p className="py-6 text-sm text-muted-foreground">No task runs yet.</p>;
  }
  const { rows, start, end } = ganttLayout(runs, now);
  return (
    <div data-testid="gantt" className="overflow-x-auto rounded-[8px] border bg-panel">
      <div className="min-w-[40rem] p-3 text-sm">
        <div className="grid grid-cols-[12rem_minmax(0,1fr)_9rem] gap-x-3 pb-1 text-xs text-muted-foreground">
          <span>Task</span>
          <span className="flex justify-between">
            <span>{new Date(start).toLocaleTimeString()}</span>
            <span>+{formatDuration(end - start)}</span>
          </span>
          <span>Duration</span>
        </div>
        <ul className="flex flex-col">
          {rows.map((r) => {
            const duration = formatDuration(r.durationMs);
            return (
              <li key={r.id} className="grid h-9 grid-cols-[12rem_minmax(0,1fr)_9rem] items-center gap-x-3 border-t">
                <span className="flex min-w-0 items-center gap-1.5">
                  <span className="truncate font-mono text-xs" title={r.label}>
                    {r.label}
                  </span>
                  {r.reused && (
                    <span className="inline-flex shrink-0 items-center gap-0.5 rounded-[6px] border px-1 text-xs text-muted-foreground">
                      <Recycle className="h-3 w-3" aria-hidden />
                      reused
                    </span>
                  )}
                </span>
                <span className="relative h-4 rounded-[4px] bg-muted">
                  {r.started && (
                    <span
                      data-testid="gantt-bar"
                      title={`${r.label}, ${stateLabel(r.state.toLowerCase())}, ${duration}`}
                      className={cn("absolute top-0 h-4 rounded-[4px]", stateBarClass(r.state))}
                      style={{ left: `${r.left}%`, width: `max(${r.width}%, 3px)` }}
                    />
                  )}
                </span>
                <span className="flex min-w-0 items-center gap-2 text-xs">
                  <span className="tabular-nums">{duration}</span>
                  <ExecutionStateBadge state={r.state} />
                </span>
              </li>
            );
          })}
        </ul>
      </div>
    </div>
  );
}
