import { useVirtualizer } from "@tanstack/react-virtual";
import { AlertTriangle, Download, Loader2 } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { getExecutionLogs } from "@/api/sdk.gen";
import type { LogEntry } from "@/api/types.gen";
import { Field } from "@/components/ui/field";
import { Input, Select } from "@/components/ui/input";
import { errorMessage } from "@/lib/errors";
import { filterLogs } from "@/lib/executions";
import { cn } from "@/lib/utils";

const lineKey = (l: LogEntry) => `${l.task_run_id}:${l.n}`;

/** useLogLines loads the log history and then tails new lines through SSE (REQ-RUN-008). */
function useLogLines(executionId: string) {
  const [lines, setLines] = useState<LogEntry[]>([]);
  const [status, setStatus] = useState<"loading" | "live" | "done" | "error">("loading");
  const [error, setError] = useState<string>();
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let stopped = false;
    let es: EventSource | undefined;
    const seen = new Set<string>();
    const add = (batch: LogEntry[]) => {
      const fresh = batch.filter((l) => {
        const k = lineKey(l);
        if (seen.has(k)) return false;
        seen.add(k);
        return true;
      });
      if (fresh.length) setLines((prev) => prev.concat(fresh));
    };
    setLines([]);
    setStatus("loading");
    setError(undefined);

    const run = async () => {
      const limit = 5000;
      let cursor: string | undefined;
      let done: boolean | undefined;
      for (;;) {
        const { data: page } = await getExecutionLogs({
          path: { executionId },
          query: { limit, cursor },
          throwOnError: true,
        });
        if (stopped) return;
        add(page.lines);
        done = page.done;
        // The server sends a cursor on each page. A page that is not full is the end of the history.
        if (!page.next_cursor || page.lines.length < limit) break;
        cursor = page.next_cursor;
      }
      if (done) {
        setStatus("done");
        return;
      }
      setStatus("live");
      es = new EventSource(`/api/v1/executions/${encodeURIComponent(executionId)}/logs/stream`);
      es.addEventListener("line", (ev) => {
        try {
          add([JSON.parse((ev as MessageEvent<string>).data) as LogEntry]);
        } catch {
          // A malformed event carries no line to show.
        }
      });
      es.addEventListener("end", () => {
        es?.close();
        setStatus("done");
      });
    };
    run().catch((e: unknown) => {
      if (stopped) return;
      setStatus("error");
      setError(errorMessage(e));
    });
    return () => {
      stopped = true;
      es?.close();
    };
  }, [executionId, attempt]);

  return { lines, status, error, retry: () => setAttempt((n) => n + 1) };
}

/** LogViewer shows the logs of an execution in a virtualized panel with its own scroll. */
export function LogViewer({ executionId }: { executionId: string }) {
  const { lines, status, error, retry } = useLogLines(executionId);
  const [task, setTask] = useState("");
  const [search, setSearch] = useState("");
  const tasks = useMemo(() => [...new Set(lines.map((l) => l.task_key))].sort(), [lines]);
  const shown = useMemo(() => filterLogs(lines, task, search), [lines, task, search]);

  const scrollRef = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  const virtualizer = useVirtualizer({
    count: shown.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => 20,
    overscan: 20,
  });

  useEffect(() => {
    if (stick.current && shown.length > 0) virtualizer.scrollToIndex(shown.length - 1, { align: "end" });
  }, [shown.length, virtualizer]);

  const download = `/api/v1/executions/${encodeURIComponent(executionId)}/logs/download${
    task ? `?task=${encodeURIComponent(task)}` : ""
  }`;

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-end gap-3">
        <div className="w-full sm:w-48">
          <Field id="log-task" label="Task">
            <Select value={task} onChange={(e) => setTask(e.target.value)}>
              <option value="">All tasks</option>
              {tasks.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </Select>
          </Field>
        </div>
        <div className="w-full sm:w-64">
          <Field id="log-search" label="Search">
            <Input type="search" value={search} onChange={(e) => setSearch(e.target.value)} />
          </Field>
        </div>
        <a
          href={download}
          download
          className="inline-flex h-9 items-center gap-1.5 rounded-[6px] border border-input bg-panel px-3 text-sm font-medium hover:bg-muted"
        >
          <Download className="h-4 w-4" aria-hidden />
          Download
        </a>
        <span role="status" className="flex h-9 items-center gap-1.5 text-xs text-muted-foreground">
          {status === "loading" && <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden />}
          {status === "loading" ? "Loading" : status === "live" ? "Live" : status === "done" ? "Complete" : ""}
          {status !== "loading" && ` · ${shown.length} of ${lines.length} lines`}
        </span>
      </div>
      {status === "error" ? (
        <div role="alert" className="flex flex-wrap items-center gap-2 py-6 text-sm text-destructive">
          <AlertTriangle className="h-4 w-4" aria-hidden />
          {error}
          <button type="button" className="underline" onClick={retry}>
            Retry
          </button>
        </div>
      ) : (
        <div
          ref={scrollRef}
          data-testid="log-panel"
          tabIndex={0}
          aria-label="Log lines"
          onScroll={(e) => {
            const el = e.currentTarget;
            stick.current = el.scrollTop + el.clientHeight >= el.scrollHeight - 8;
          }}
          className="h-[60vh] overflow-auto rounded-[8px] border bg-panel font-mono text-xs"
        >
          {status !== "loading" && shown.length === 0 ? (
            <p className="p-3 font-sans text-sm text-muted-foreground">
              {lines.length === 0 ? "No log lines yet." : "No lines match the filter."}
            </p>
          ) : (
            <div className="relative w-full" style={{ height: virtualizer.getTotalSize() }}>
              {virtualizer.getVirtualItems().map((item) => {
                const l = shown[item.index];
                if (!l) return null;
                return (
                  <div
                    key={item.key}
                    data-index={item.index}
                    ref={virtualizer.measureElement}
                    className={cn(
                      "absolute left-0 flex w-full gap-3 px-3 py-0.5",
                      l.stream === "stderr" && "bg-state-failed/10 text-state-failed",
                      l.stream === "system" && "text-muted-foreground italic",
                    )}
                    style={{ transform: `translateY(${item.start}px)` }}
                  >
                    <span className="shrink-0 text-muted-foreground">
                      {l.task_key} #{l.attempt}
                    </span>
                    <span className="min-w-0 break-all whitespace-pre-wrap">{l.text}</span>
                  </div>
                );
              })}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
