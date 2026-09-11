import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Bot, Check, Loader2, Plus, Send, Wrench, X } from "lucide-react";
import { useEffect, useRef, useState, type FormEvent } from "react";
import {
  createAiConversationMutation,
  getAiConversationOptions,
  getAiConversationQueryKey,
  listAiConversationsOptions,
  listAiConversationsQueryKey,
} from "@/api/@tanstack/react-query.gen";
import { getAiConversation } from "@/api/sdk.gen";
import type { Block, PendingAction, StoredMessage } from "@/api/types.gen";
import { DiffView } from "@/components/diff-view";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { FormError } from "@/components/ui/field";
import { Select } from "@/components/ui/input";
import { errorMessage } from "@/lib/errors";
import { useAiEnabled } from "./status";
import { postStream } from "./stream";

/** LiveItem is one event of the running turn. The saved conversation replaces them when the turn ends. */
type LiveItem =
  | { kind: "text"; text: string }
  | { kind: "tool_call"; id: string; name: string; input: unknown; mutating: boolean }
  | { kind: "tool_result"; id: string; name: string; isError: boolean; text: string }
  | { kind: "action"; action: PendingAction }
  | { kind: "error"; code: string; message: string };

function compact(v: unknown): string {
  try {
    return JSON.stringify(v);
  } catch {
    return String(v);
  }
}

/** proposalDiffs returns the file diffs of a propose_change result, or undefined. */
function proposalDiffs(text: string): { path: string; diff: string; issues: { message: string; line: number }[] }[] | undefined {
  try {
    const v = JSON.parse(text) as { files?: { path: string; diff: string; issues?: { message: string; line: number }[] }[] };
    return v.files?.map((f) => ({ path: f.path, diff: f.diff, issues: f.issues ?? [] }));
  } catch {
    return undefined;
  }
}

function ToolCall({ name, input, mutating }: { name: string; input: unknown; mutating: boolean }) {
  return (
    <div className="flex flex-col gap-1 rounded-[6px] border px-2 py-1.5 text-xs">
      <span className="flex items-center gap-1.5 font-medium">
        <Wrench className="h-3.5 w-3.5" aria-hidden />
        Tool call {name}
        {mutating && <Badge tone="accent">Needs confirmation</Badge>}
      </span>
      <code className="block font-mono break-all text-muted-foreground">{compact(input)}</code>
    </div>
  );
}

function ToolResult({ name, isError, text }: { name: string; isError: boolean; text: string }) {
  const diffs = name === "propose_change" && !isError ? proposalDiffs(text) : undefined;
  return (
    <div className="flex flex-col gap-1 rounded-[6px] border px-2 py-1.5 text-xs">
      <span className="flex items-center gap-1.5 font-medium">
        Result of {name}
        {isError && <Badge tone="failed">Error</Badge>}
      </span>
      {diffs ? (
        diffs.map((d) => (
          <div key={d.path} className="flex flex-col gap-1">
            <DiffView text={d.diff} label={`Diff of ${d.path}`} />
            {d.issues.length > 0 && (
              <ul aria-label={`Issues of ${d.path}`} className="text-state-failed">
                {d.issues.map((i, n) => (
                  <li key={n}>
                    Line {i.line}: {i.message}
                  </li>
                ))}
              </ul>
            )}
          </div>
        ))
      ) : (
        <details>
          <summary className="cursor-pointer text-muted-foreground">Show result</summary>
          <code className="block max-h-48 overflow-auto font-mono break-all whitespace-pre-wrap">{text}</code>
        </details>
      )}
    </div>
  );
}

function SavedMessage({ m }: { m: StoredMessage }) {
  const blocks: Block[] = m.content ?? [];
  if (m.role === "user") {
    return (
      <li className="self-end rounded-[8px] bg-accent-soft px-3 py-2 text-sm whitespace-pre-wrap">
        <span className="sr-only">You: </span>
        {blocks.map((b) => b.text).join("")}
      </li>
    );
  }
  return (
    <li className="flex flex-col gap-1.5">
      {blocks.map((b, i) => {
        if (b.type === "text" && b.text) {
          return (
            <p key={i} className="text-sm whitespace-pre-wrap">
              <span className="sr-only">Assistant: </span>
              {b.text}
            </p>
          );
        }
        if (b.type === "tool_use") return <ToolCall key={i} name={b.tool_name ?? ""} input={b.input} mutating={false} />;
        if (b.type === "tool_result") return <ToolResult key={i} name={b.tool_name ?? ""} isError={!!b.is_error} text={b.text ?? ""} />;
        return null;
      })}
    </li>
  );
}

function ActionCard({ action, busy, onDecide }: { action: PendingAction; busy: boolean; onDecide: (confirm: boolean) => void }) {
  return (
    <li className="flex flex-col gap-2 rounded-[8px] border border-accent p-3 text-sm" aria-label={`Action ${action.tool}`}>
      <span className="font-medium">The assistant wants to run {action.tool}.</span>
      <code className="block font-mono text-xs break-all">{compact(action.arguments)}</code>
      <div className="flex gap-2">
        <Button size="sm" disabled={busy} onClick={() => onDecide(true)}>
          <Check className="h-3.5 w-3.5" aria-hidden />
          Confirm
        </Button>
        <Button size="sm" variant="secondary" disabled={busy} onClick={() => onDecide(false)}>
          Reject
        </Button>
      </div>
    </li>
  );
}

/** AssistantDrawer is the assistant panel of all routes (REQ-AI-005, Appendix E). It is hidden without an AI provider. */
export function AssistantDrawer() {
  const enabled = useAiEnabled();
  const [open, setOpen] = useState(false);
  if (!enabled) return null;
  return (
    <>
      {!open && (
        <button
          type="button"
          onClick={() => setOpen(true)}
          aria-expanded={false}
          className="fixed right-4 bottom-4 z-30 inline-flex h-10 items-center gap-2 rounded-full border bg-panel px-4 text-sm font-medium shadow-md hover:bg-muted"
        >
          <Bot className="h-4 w-4" aria-hidden />
          Assistant
        </button>
      )}
      {open && <Panel onClose={() => setOpen(false)} />}
    </>
  );
}

function Panel({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const [convId, setConvId] = useState<string | undefined>();
  const [draft, setDraft] = useState("");
  const [live, setLive] = useState<LiveItem[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | undefined>();
  const list = useQuery(listAiConversationsOptions());
  const detail = useQuery({ ...getAiConversationOptions({ path: { conversationId: convId ?? "" } }), enabled: !!convId });
  const create = useMutation(createAiConversationMutation());
  const endRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!convId && list.data?.items[0]) setConvId(list.data.items[0].id);
  }, [convId, list.data]);
  useEffect(() => {
    endRef.current?.scrollIntoView({ block: "end" });
  }, [live, detail.data]);

  const onEvent = (ev: { event: string; data: unknown }) => {
    const d = (ev.data ?? {}) as Record<string, unknown>;
    setLive((items) => {
      switch (ev.event) {
        case "text": {
          const last = items[items.length - 1];
          if (last?.kind === "text") return [...items.slice(0, -1), { kind: "text", text: last.text + String(d.delta ?? "") }];
          return [...items, { kind: "text", text: String(d.delta ?? "") }];
        }
        case "tool_call":
          return [...items, { kind: "tool_call", id: String(d.id), name: String(d.name), input: d.input, mutating: !!d.mutating }];
        case "tool_result":
          return [...items, { kind: "tool_result", id: String(d.id), name: String(d.name), isError: !!d.is_error, text: String(d.text ?? "") }];
        case "pending_action":
          return [...items, { kind: "action", action: d as unknown as PendingAction }];
        case "error":
          return [...items, { kind: "error", code: String(d.code), message: String(d.message) }];
        default:
          return items;
      }
    });
  };

  const run = async (id: string, url: string, body: unknown) => {
    setBusy(true);
    setError(undefined);
    setLive([]);
    try {
      let done = false;
      await postStream(url, body, (ev) => {
        if (ev.event === "done") done = true;
        onEvent(ev);
      });
      // Load the saved turn with a new request. A detail request can start before the turn saves
      // its messages and end after the stream. An invalidation or fetchQuery reuses that request,
      // so the answer then disappears. Cancel it, and write the result of a new request.
      const key = getAiConversationQueryKey({ path: { conversationId: id } });
      await qc.cancelQueries({ queryKey: key });
      const { data } = await getAiConversation({ path: { conversationId: id }, throwOnError: true });
      qc.setQueryData(key, data);
      setLive((items) => items.filter((i) => i.kind === "error"));
      // A stream that ends without "done" lost its connection or the server stopped the turn.
      if (!done) setError("The answer stopped before the end. The saved part of the conversation shows below.");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
      void qc.invalidateQueries({ queryKey: listAiConversationsQueryKey() });
    }
  };

  const send = async (e: FormEvent) => {
    e.preventDefault();
    const text = draft.trim();
    if (!text || busy) return;
    let id = convId;
    try {
      if (!id) {
        id = (await create.mutateAsync({ body: {} })).id;
        setConvId(id);
      }
    } catch (err) {
      setError(errorMessage(err));
      return;
    }
    setDraft("");
    await run(id, `/api/v1/ai/conversations/${encodeURIComponent(id)}/messages`, { text });
  };

  const decide = (action: PendingAction, confirm: boolean) => {
    if (!convId) return;
    const verb = confirm ? "confirm" : "reject";
    void run(convId, `/api/v1/ai/conversations/${encodeURIComponent(convId)}/actions/${encodeURIComponent(action.id)}/${verb}`, undefined);
  };

  const pending = (detail.data?.actions ?? []).filter((a) => a.status === "pending");
  const liveActions = new Set(live.filter((i) => i.kind === "action").map((i) => (i as { action: PendingAction }).action.id));

  return (
    <aside aria-label="Assistant" className="fixed inset-y-0 right-0 z-40 flex w-full max-w-md flex-col border-l bg-panel shadow-lg">
      <div className="flex items-center gap-2 border-b p-3">
        <h2 className="flex items-center gap-2 text-base font-semibold">
          <Bot className="h-4 w-4" aria-hidden />
          Assistant
        </h2>
        <div className="ml-auto flex items-center gap-1">
          <Button
            variant="ghost"
            size="sm"
            aria-label="New conversation"
            title="New conversation"
            disabled={busy || create.isPending}
            onClick={() => {
              create.mutate(
                { body: {} },
                {
                  onSuccess: (c) => {
                    setConvId(c.id);
                    setLive([]);
                    setError(undefined);
                    void qc.invalidateQueries({ queryKey: listAiConversationsQueryKey() });
                  },
                  onError: (err) => setError(errorMessage(err)),
                },
              );
            }}
          >
            <Plus className="h-4 w-4" aria-hidden />
          </Button>
          <button
            type="button"
            aria-label="Close assistant"
            onClick={onClose}
            className="flex h-8 w-8 items-center justify-center rounded-[6px] text-muted-foreground hover:bg-muted hover:text-foreground"
          >
            <X className="h-4 w-4" aria-hidden />
          </button>
        </div>
      </div>
      {(list.data?.items.length ?? 0) > 0 && (
        <div className="border-b p-3">
          <label className="flex flex-col gap-1 text-xs text-muted-foreground">
            Conversation
            <Select value={convId ?? ""} disabled={busy} onChange={(e) => setConvId(e.target.value)}>
              {list.data?.items.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.title || "New conversation"}
                </option>
              ))}
            </Select>
          </label>
        </div>
      )}
      <div className="min-h-0 flex-1 overflow-y-auto p-3">
        <ol aria-label="Conversation messages" className="flex flex-col gap-3">
          {detail.data?.messages.map((m) => <SavedMessage key={m.id} m={m} />)}
          {pending
            .filter((a) => !liveActions.has(a.id))
            .map((a) => (
              <ActionCard key={a.id} action={a} busy={busy} onDecide={(c) => decide(a, c)} />
            ))}
          {live.map((item, i) => {
            switch (item.kind) {
              case "text":
                return (
                  <li key={i} className="text-sm whitespace-pre-wrap" aria-live="polite">
                    {item.text}
                  </li>
                );
              case "tool_call":
                return (
                  <li key={i}>
                    <ToolCall name={item.name} input={item.input} mutating={item.mutating} />
                  </li>
                );
              case "tool_result":
                return (
                  <li key={i}>
                    <ToolResult name={item.name} isError={item.isError} text={item.text} />
                  </li>
                );
              case "action":
                return <ActionCard key={i} action={item.action} busy={busy} onDecide={(c) => decide(item.action, c)} />;
              case "error":
                return (
                  <li key={i} role="alert" className="text-sm text-state-failed">
                    {item.code === "step_limit_reached" ? "The assistant stopped after 20 tool steps." : item.message}
                  </li>
                );
            }
          })}
        </ol>
        {busy && (
          <p role="status" className="mt-2 flex items-center gap-2 text-xs text-muted-foreground">
            <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden />
            The assistant is answering.
          </p>
        )}
        <div ref={endRef} />
      </div>
      <form onSubmit={(e) => void send(e)} className="flex flex-col gap-2 border-t p-3">
        {error && <FormError>{error}</FormError>}
        <label htmlFor="assistant-message" className="sr-only">
          Message
        </label>
        <textarea
          id="assistant-message"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) void send(e);
          }}
          rows={3}
          maxLength={20000}
          placeholder="Ask about flows, executions and logs."
          className="w-full resize-none rounded-[6px] border border-input bg-background px-3 py-2 text-sm text-foreground placeholder:text-muted-foreground"
        />
        <div className="flex justify-end">
          <Button type="submit" disabled={busy || create.isPending || pending.length > 0 || !draft.trim()}>
            <Send className="h-4 w-4" aria-hidden />
            Send
          </Button>
        </div>
      </form>
    </aside>
  );
}
