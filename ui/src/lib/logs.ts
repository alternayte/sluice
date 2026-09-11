/** LogPageLike is the part of a log page that the history loader uses. */
export type LogPageLike<T> = { lines: T[]; next_cursor?: string | null; done: boolean };

/**
 * loadLogs requests log history pages, then opens the live stream when the execution has not ended.
 * The server sends next_cursor on each page, also for an ended execution. For this reason the loop
 * stops at a page with fewer lines than the limit, or at a page with no cursor.
 * It returns "done" for an ended execution, "live" after openStream, and "stopped" when the caller stopped it.
 */
export async function loadLogs<T>({
  fetchPage,
  limit,
  onLines,
  openStream,
  stopped = () => false,
}: {
  fetchPage: (cursor: string | undefined) => Promise<LogPageLike<T>>;
  limit: number;
  onLines: (lines: T[]) => void;
  openStream: () => void;
  stopped?: () => boolean;
}): Promise<"done" | "live" | "stopped"> {
  let cursor: string | undefined;
  let done: boolean;
  for (;;) {
    const page = await fetchPage(cursor);
    if (stopped()) return "stopped";
    onLines(page.lines);
    done = page.done;
    if (!page.next_cursor || page.lines.length < limit) break;
    cursor = page.next_cursor;
  }
  if (done) return "done";
  openStream();
  return "live";
}
