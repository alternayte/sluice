/** SSEEvent is one server-sent event. data is parsed JSON, or the raw text when it is not JSON. */
export type SSEEvent = { event: string; data: unknown };

/** parseSSEChunk parses one event block (the text between two blank lines). */
export function parseSSEChunk(chunk: string): SSEEvent | undefined {
  let event = "message";
  const data: string[] = [];
  for (const line of chunk.split(/\r?\n/)) {
    if (line.startsWith(":")) continue;
    if (line.startsWith("event:")) event = line.slice(6).trim();
    else if (line.startsWith("data:")) data.push(line.slice(5).replace(/^ /, ""));
  }
  if (data.length === 0) return undefined;
  const text = data.join("\n");
  try {
    return { event, data: JSON.parse(text) as unknown };
  } catch {
    return { event, data: text };
  }
}

/** SSEBuffer collects streamed text and returns the complete events. */
export class SSEBuffer {
  private buf = "";

  push(text: string): SSEEvent[] {
    this.buf += text.replace(/\r\n/g, "\n");
    const out: SSEEvent[] = [];
    let i = this.buf.indexOf("\n\n");
    while (i >= 0) {
      const ev = parseSSEChunk(this.buf.slice(0, i));
      if (ev) out.push(ev);
      this.buf = this.buf.slice(i + 2);
      i = this.buf.indexOf("\n\n");
    }
    return out;
  }
}
