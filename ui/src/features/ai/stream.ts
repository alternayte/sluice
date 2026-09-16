import { rawFetch } from "@/api-client";
import { SSEBuffer, type SSEEvent } from "@/lib/sse";

/**
 * postStream sends a POST and calls onEvent for each server-sent event of the answer.
 * EventSource cannot send a POST body, so the assistant streams use fetch (DI-40).
 * An error answer before the stream starts throws an ApiError.
 */
export async function postStream(
  url: string,
  body: unknown,
  onEvent: (e: SSEEvent) => void,
  signal?: AbortSignal,
): Promise<void> {
  const response = await rawFetch(url, {
    method: "POST",
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
  });
  const reader = response.body?.getReader();
  if (!reader) return;
  const decoder = new TextDecoder();
  const buffer = new SSEBuffer();
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    for (const ev of buffer.push(decoder.decode(value, { stream: true }))) onEvent(ev);
  }
  for (const ev of buffer.push(decoder.decode() + "\n\n")) onEvent(ev);
}
