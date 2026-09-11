import { describe, expect, it, vi } from "vitest";
import { loadLogs, type LogPageLike } from "./logs";

/** pages returns a fetchPage stub that gives the pages in order and records the cursors. */
function pages(list: LogPageLike<number>[]) {
  const cursors: (string | undefined)[] = [];
  const fetchPage = vi.fn(async (cursor: string | undefined) => {
    cursors.push(cursor);
    const p = list[cursors.length - 1];
    if (!p) throw new Error("unexpected request");
    return p;
  });
  return { fetchPage, cursors };
}

describe("loadLogs", () => {
  it("stops at a page with fewer lines than the limit although next_cursor is set", async () => {
    const { fetchPage } = pages([{ lines: [1, 2], next_cursor: "c1", done: false }]);
    const openStream = vi.fn();
    const got: number[] = [];
    const result = await loadLogs({ fetchPage, limit: 3, onLines: (l) => got.push(...l), openStream });
    expect(fetchPage).toHaveBeenCalledTimes(1);
    expect(got).toEqual([1, 2]);
    expect(result).toBe("live");
    expect(openStream).toHaveBeenCalledTimes(1);
  });

  it("requests one more page after a page with exactly limit lines", async () => {
    const { fetchPage, cursors } = pages([
      { lines: [1, 2, 3], next_cursor: "c1", done: false },
      { lines: [4], next_cursor: "c2", done: false },
    ]);
    const got: number[] = [];
    await loadLogs({ fetchPage, limit: 3, onLines: (l) => got.push(...l), openStream: vi.fn() });
    expect(cursors).toEqual([undefined, "c1"]);
    expect(got).toEqual([1, 2, 3, 4]);
  });

  it("stops at a page with no cursor", async () => {
    const { fetchPage } = pages([{ lines: [1, 2, 3], next_cursor: null, done: false }]);
    await loadLogs({ fetchPage, limit: 3, onLines: vi.fn(), openStream: vi.fn() });
    expect(fetchPage).toHaveBeenCalledTimes(1);
  });

  it("does not open the live stream for an ended execution", async () => {
    const { fetchPage } = pages([
      { lines: [1, 2, 3], next_cursor: "c1", done: false },
      { lines: [], next_cursor: "c2", done: true },
    ]);
    const openStream = vi.fn();
    const result = await loadLogs({ fetchPage, limit: 3, onLines: vi.fn(), openStream });
    expect(result).toBe("done");
    expect(openStream).not.toHaveBeenCalled();
    expect(fetchPage).toHaveBeenCalledTimes(2);
  });

  it("returns stopped and adds no lines when the caller stops it", async () => {
    const { fetchPage } = pages([{ lines: [1], next_cursor: "c1", done: false }]);
    const onLines = vi.fn();
    const openStream = vi.fn();
    const result = await loadLogs({ fetchPage, limit: 3, onLines, openStream, stopped: () => true });
    expect(result).toBe("stopped");
    expect(onLines).not.toHaveBeenCalled();
    expect(openStream).not.toHaveBeenCalled();
  });
});
