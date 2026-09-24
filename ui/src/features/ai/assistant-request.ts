import { useSyncExternalStore } from "react";
import type { Attachment } from "@/api/types.gen";

/** AssistantRequest opens the assistant with a message, for example from "Fix with assistant". */
export type AssistantRequest = {
  id: number;
  text: string;
  attachments: Attachment[];
  /** send starts a new conversation and sends the message at once. */
  send: boolean;
};

let current: AssistantRequest | undefined;
let nextId = 1;
const listeners = new Set<() => void>();

/** openAssistant opens the assistant panel with the text and attachments in the composer. */
export function openAssistant(req: Omit<AssistantRequest, "id">) {
  current = { ...req, id: nextId++ };
  listeners.forEach((l) => l());
}

/** takeAssistantRequest returns the request once and clears it. */
export function takeAssistantRequest(id: number): AssistantRequest | undefined {
  if (current?.id !== id) return undefined;
  const r = current;
  current = undefined;
  return r;
}

const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => listeners.delete(l);
};

/** useAssistantRequest returns the pending request, if any. */
export function useAssistantRequest(): AssistantRequest | undefined {
  return useSyncExternalStore(
    subscribe,
    () => current,
    () => current,
  );
}

/** attachmentLabel names an attachment for its chip, for example "sales/nightly-load". */
export function attachmentLabel(a: Attachment): string {
  switch (a.kind) {
    case "flow":
      return `${a.namespace}/${a.flow_id}`;
    case "file":
      return `${a.namespace}:${a.path}`;
    default:
      return `execution ${(a.execution_id ?? "").slice(0, 8)}`;
  }
}

/** attachmentKey is a stable identity of an attachment. */
export function attachmentKey(a: Attachment): string {
  return [a.kind, a.namespace, a.flow_id, a.execution_id, a.path].join("|");
}
