import { useVirtualizer } from "@tanstack/react-virtual";
import { ArrowDown, Download, Search, WrapText } from "lucide-react";
import { memo, useEffect, useMemo, useRef, useState } from "react";
import { getExecutionLogs } from "@/api/sdk.gen";
import type { LogEntry } from "@/api/types.gen";
import { EmptyState, ErrorState, Skeleton } from "@/components/data-state";
import { buttonClass } from "@/components/ui/button";
import { Input, Select } from "@/components/ui/input";
import { errorMessage } from "@/lib/errors";
import { filterLogs } from "@/lib/executions";
import { loadLogs, splitLogText } from "@/lib/logs";
import { cn } from "@/lib/utils";

const lineKey = (l: LogEntry) => `${l.task_run_id}:${l.n}`;
const wrapKey = "sluice-log-wrap";
const rowHeight = 20;

type Status = "loading" | "live" | "reconnecting" | "done" | "error";

/**
 * useLogLines loads the log history and then tails new lines through SSE (REQ-RUN-008).
 * New lines are batched per animation frame, so a fast stream does not render once per line.
 * A stream that the browser gives up on opens again with a growing delay.
 */
function useLogLines(executionId: string) {
  const [lines, setLines] = useState<LogEntry[]>([]);
  const [status, setStatus] = useState<Status>("loading");
  const [error, setError] = useState<string>();
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let stopped = false;
    let es: EventSource | undefined;
    let retry: number | undefined;
    let frame: number | undefined;
    let pending: LogEntry[] = [];
    let backoff = 1000;
    const seen = new Set<string>();

    const flush = () => {
      frame = undefined;
      if (stopped || pending.length === 0) return;
      const batch = pending;
      pending = [];
      setLines((prev) => prev.concat(batch));
    };
    const add = (batch: LogEntry[]) => {
      for (const l of batch) {
        const k = lineKey(l);
        if (seen.has(k)) continue;
        seen.add(k);
        pending.push(l);
      }
      if (pending.length && frame === undefined) frame = requestAnimationFrame(flush);
    };
    setLines([]);
    setStatus("loading");
    setError(undefined);

    const openStream = () => {
      if (stopped) return;
      setStatus("live");
      es = new EventSource(`/api/v1/executions/${encodeURIComponent(executionId)}/logs/stream`);
      es.addEventListener("open", () => {
        backoff = 1000;
        setStatus("live");
      });
      es.addEventListener("line", (ev) => {
        try {
          add([JSON.parse((ev as MessageEvent<string>).data) as LogEntry]);
        } catch {
          // A malformed event carries no line to show.
        }
      });
      es.addEventListener("end", () => {
        es?.close();
        flush();
        setStatus("done");
      });
      es.addEventListener("error", () => {
        if (stopped || !es) return;
        // The browser retries a dropped connection by itself (CONNECTING). A closed stream needs a new one.
        if (es.readyState === EventSource.CLOSED) {
          setStatus("reconnecting");
          retry = window.setTimeout(openStream, backoff);
          backoff = Math.min(backoff * 2, 15_000);
        } else {
          setStatus("reconnecting");
        }
      });
    };

    const run = async () => {
      const result = await loadLogs({
        limit: 5000,
        fetchPage: async (cursor) =>
          (await getExecutionLogs({ path: { executionId }, query: { limit: 5000, cursor }, throwOnError: true })).data,
        onLines: add,
        openStream,
        stopped: () => stopped,
      });
      if (result === "done") {
        flush();
        setStatus("done");
      }
    };
    run().catch((e: unknown) => {
      if (stopped) return;
      setStatus("error");
      setError(errorMessage(e));
    });
    return () => {
      stopped = true;
      es?.close();
      window.clearTimeout(retry);
      if (frame !== undefined) cancelAnimationFrame(frame);
    };
  }, [executionId, attempt]);

  return { lines, status, error, retry: () => setAttempt((n) => n + 1) };
}

const LineText = memo(function LineText({ text, search }: { text: string; search: string }) {
  const parts = splitLogText(text, search);
  return (
    <>
      {parts.map((p, i) =>
        p.kind === "url" ? (
          <a
            key={i}
            href={p.text}
            target="_blank"
            rel="noreferrer noopener"
            className="text-accent-text underline decoration-accent-text/40 underline-offset-2 hover:decoration-accent-text"
          >
            {p.text}
          </a>
        ) : p.kind === "match" ? (
          <mark key={i} className="rounded-[2px] bg-state-timed-out/30 text-inherit">
            {p.text}
          </mark>
        ) : (
          <span key={i}>{p.text}</span>
        ),
      )}
    </>
  );
});

const statusText: Record<Status, string> = {
  loading: "Loading",
  live: "Live",
  reconnecting: "Reconnecting",
  done: "Complete",
  error: "",
};

/**
 * LogViewer shows the logs of an execution in a virtualized panel with its own scroll. The
 * parent controls the task filter, so a selection in the waterfall filters the logs.
 */
export function LogViewer({
  executionId,
  task,
  onTaskChange,
  className,
}: {
  executionId: string;
  task: string;
  onTaskChange: (task: string) => void;
  className?: string;
}) {
  const { lines, status, error, retry } = useLogLines(executionId);
  const [search, setSearch] = useState("");
  const [follow, setFollow] = useState(true);
  // Without wrap every row is one line high, so the list needs no measuring and scrolls fast on large logs.
  const [wrap, setWrapState] = useState(() => {
    try {
      return localStorage.getItem(wrapKey) === "1";
    } catch {
      return false;
    }
  });
  const setWrap = (v: boolean) => {
    setWrapState(v);
    try {
      localStorage.setItem(wrapKey, v ? "1" : "0");
    } catch {
      // Storage is not available. The choice lasts for this page load.
    }
  };
  const [unseen, setUnseen] = useState(0);
  const tasks = useMemo(() => {
    const set = new Set(lines.map((l) => l.task_key));
    if (task) set.add(task);
    return [...set].sort();
  }, [lines, task]);
  const shown = useMemo(() => filterLogs(lines, task, search), [lines, task, search]);
  const multiTask = !task && tasks.length > 1;
  // The task column fits the longest "task #attempt" label, up to 28 characters.
  const taskWidth = useMemo(
    () => Math.min(28, Math.max(8, ...lines.slice(0, 2000).map((l) => l.task_key.length + 3))),
    [lines],
  );
  const gutter = String(shown.length).length;

  const scrollRef = useRef<HTMLDivElement>(null);
  const followRef = useRef(follow);
  followRef.current = follow;
  const lastCount = useRef(0);
  const virtualizer = useVirtualizer({
    count: shown.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => rowHeight,
    overscan: 24,
  });

  // A change of wrap changes every row height, so the cached sizes are stale.
  useEffect(() => {
    virtualizer.measure();
  }, [wrap, virtualizer]);

  useEffect(() => {
    const added = shown.length - lastCount.current;
    lastCount.current = shown.length;
    if (shown.length === 0) return;
    if (followRef.current) virtualizer.scrollToIndex(shown.length - 1, { align: "end" });
    else if (added > 0) setUnseen((n) => n + added);
  }, [shown.length, virtualizer]);

  const jumpToEnd = () => {
    setFollow(true);
    setUnseen(0);
    if (shown.length) virtualizer.scrollToIndex(shown.length - 1, { align: "end" });
  };

  const download = `/api/v1/executions/${encodeURIComponent(executionId)}/logs/download${
    task ? `?task=${encodeURIComponent(task)}` : ""
  }`;

  return (
    <div className={cn("flex min-h-0 flex-col gap-2.5", className)}>
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-0 flex-1 basis-40">
          <Search
            className="pointer-events-none absolute top-1/2 left-2.5 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground"
            aria-hidden
          />
          <Input
            id="log-search"
            aria-label="Search"
            type="search"
            placeholder="Search the logs"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="pl-8"
          />
        </div>
        <Select
          id="log-task"
          aria-label="Task"
          value={task}
          onChange={(e) => onTaskChange(e.target.value)}
          className="w-auto max-w-44 min-w-32 flex-none"
        >
          <option value="">All tasks</option>
          {tasks.map((t) => (
            <option key={t} value={t}>
              {t}
            </option>
          ))}
        </Select>
        <a href={download} download className={buttonClass("secondary", "md")}>
          <Download className="h-3.5 w-3.5" aria-hidden />
          Download
        </a>
      </div>
      <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground">
        <span role="status" className="flex items-center gap-1.5">
          {(status === "live" || status === "reconnecting") && (
            <span
              aria-hidden
              className={cn(
                "h-1.5 w-1.5 rounded-full",
                status === "live" ? "animate-breathe bg-state-success" : "animate-breathe bg-state-timed-out",
              )}
            />
          )}
          {statusText[status]}
          {status !== "loading" && status !== "error" && ` · ${shown.length} of ${lines.length} lines`}
        </span>
        <span className="flex items-center gap-1">
          <button
            type="button"
            aria-pressed={wrap}
            onClick={() => setWrap(!wrap)}
            className={cn(
              "pressable inline-flex h-6 items-center gap-1 rounded-full px-2 font-medium",
              wrap ? "bg-accent-soft text-accent-text" : "hover:bg-muted hover:text-foreground",
            )}
          >
            <WrapText className="h-3 w-3" aria-hidden />
            Wrap
          </button>
          <button
            type="button"
            aria-pressed={follow}
            onClick={() => (follow ? setFollow(false) : jumpToEnd())}
            className={cn(
              "pressable inline-flex h-6 items-center gap-1 rounded-full px-2 font-medium",
              follow ? "bg-accent-soft text-accent-text" : "hover:bg-muted hover:text-foreground",
            )}
          >
            <ArrowDown className="h-3 w-3" aria-hidden />
            Follow
          </button>
        </span>
      </div>
      {status === "error" ? (
        <ErrorState message={error ?? "The logs did not load."} onRetry={retry} />
      ) : (
        <div className="relative min-h-0 flex-1">
          <div
            ref={scrollRef}
            data-testid="log-panel"
            tabIndex={0}
            aria-label="Log lines"
            onWheel={(e) => {
              if (e.deltaY < 0 && follow) setFollow(false);
            }}
            onScroll={(e) => {
              const el = e.currentTarget;
              const atEnd = el.scrollTop + el.clientHeight >= el.scrollHeight - 8;
              if (atEnd && !follow) {
                setFollow(true);
                setUnseen(0);
              }
            }}
            onKeyDown={(e) => {
              if (["ArrowUp", "PageUp", "Home"].includes(e.key) && follow) setFollow(false);
            }}
            className="h-[60vh] min-h-72 overflow-auto lg:h-full rounded-panel border bg-panel font-mono text-xs leading-5 shadow-panel"
          >
            {status === "loading" ? (
              <div className="p-3">
                <Skeleton shape="lines" rows={8} />
              </div>
            ) : shown.length === 0 ? (
              <EmptyState
                text={lines.length === 0 ? "No log lines yet." : "No lines match the filter."}
                className="m-3 border-0 font-sans"
              />
            ) : (
              <div className="relative w-full py-1" style={{ height: virtualizer.getTotalSize() + 8 }}>
                {virtualizer.getVirtualItems().map((item) => {
                  const l = shown[item.index];
                  if (!l) return null;
                  return (
                    <div
                      key={item.key}
                      data-index={item.index}
                      ref={wrap ? virtualizer.measureElement : undefined}
                      className={cn(
                        "absolute left-0 flex gap-3 border-l-2 border-transparent pr-3 pl-2 leading-5 hover:bg-muted/50",
                        wrap ? "w-full" : "h-5 w-max min-w-full",
                        l.stream === "stderr" && "border-state-failed/70 bg-state-failed/[0.06] text-destructive",
                        l.stream === "system" && "text-muted-foreground italic",
                      )}
                      style={{ transform: `translateY(${item.start + 4}px)` }}
                    >
                      <span
                        aria-hidden
                        className="shrink-0 text-right text-muted-foreground/60 tabular-nums select-none"
                        style={{ width: `${gutter}ch` }}
                      >
                        {item.index + 1}
                      </span>
                      {multiTask && (
                        <span
                          className="shrink-0 truncate text-muted-foreground"
                          style={{ width: `${taskWidth}ch` }}
                          title={`${l.task_key} #${l.attempt}`}
                        >
                          {l.task_key} #{l.attempt}
                        </span>
                      )}
                      <span className={cn("min-w-0", wrap ? "break-all whitespace-pre-wrap" : "whitespace-pre")}>
                        <LineText text={l.text} search={search} />
                      </span>
                    </div>
                  );
                })}
              </div>
            )}
          </div>
          {!follow && unseen > 0 && (
            <button
              type="button"
              onClick={jumpToEnd}
              className="pressable absolute bottom-3 left-1/2 inline-flex h-7 -translate-x-1/2 animate-toast-in items-center gap-1.5 rounded-full bg-accent-fill px-3 text-xs font-medium text-accent-foreground shadow-float"
            >
              <ArrowDown className="h-3.5 w-3.5" aria-hidden />
              {unseen} new {unseen === 1 ? "line" : "lines"}
            </button>
          )}
        </div>
      )}
    </div>
  );
}
