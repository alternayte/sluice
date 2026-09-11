import { describe, expect, it } from "vitest";
import { parseSSEChunk, SSEBuffer } from "./sse";

describe("parseSSEChunk", () => {
  it("parses the event name and JSON data", () => {
    expect(parseSSEChunk('event: text\ndata: {"delta":"Hi"}')).toEqual({ event: "text", data: { delta: "Hi" } });
  });

  it("keeps text data and ignores comments", () => {
    expect(parseSSEChunk(": keep-alive\ndata: plain")).toEqual({ event: "message", data: "plain" });
  });

  it("returns undefined without data", () => {
    expect(parseSSEChunk("event: done")).toBeUndefined();
  });
});

describe("SSEBuffer", () => {
  it("returns events only when their block is complete", () => {
    const b = new SSEBuffer();
    expect(b.push('event: text\ndata: {"delta":"a"}\n')).toEqual([]);
    expect(b.push('\nevent: done\r\ndata: {"stop":"end_turn"}\r\n\r\n')).toEqual([
      { event: "text", data: { delta: "a" } },
      { event: "done", data: { stop: "end_turn" } },
    ]);
  });
});
