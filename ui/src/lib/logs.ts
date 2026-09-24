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

/** TextPart is a piece of a log line: plain text, a URL or a search match. */
export type TextPart = { kind: "text" | "url" | "match"; text: string };

const urlPattern = /\bhttps?:\/\/[^\s<>"'`]+[^\s<>"'`.,;:!?)\]}]/g;

/**
 * splitLogText splits a log line into URLs and search matches, so the viewer can link the
 * URLs and mark the matches. A match inside a URL stays part of the URL.
 */
export function splitLogText(text: string, search: string): TextPart[] {
  const parts: TextPart[] = [];
  const q = search.trim().toLowerCase();
  const pushText = (t: string) => {
    if (!t) return;
    if (!q) {
      parts.push({ kind: "text", text: t });
      return;
    }
    const lower = t.toLowerCase();
    let i = 0;
    for (let at = lower.indexOf(q); at >= 0; at = lower.indexOf(q, i)) {
      if (at > i) parts.push({ kind: "text", text: t.slice(i, at) });
      parts.push({ kind: "match", text: t.slice(at, at + q.length) });
      i = at + q.length;
    }
    if (i < t.length) parts.push({ kind: "text", text: t.slice(i) });
  };
  let last = 0;
  for (const m of text.matchAll(urlPattern)) {
    pushText(text.slice(last, m.index));
    parts.push({ kind: "url", text: m[0] });
    last = m.index + m[0].length;
  }
  pushText(text.slice(last));
  return parts;
}
