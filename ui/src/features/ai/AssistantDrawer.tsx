import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  AtSign,
  Check,
  CheckCircle2,
  ChevronRight,
  Loader2,
  Send,
  ShieldQuestion,
  Sparkles,
  SquarePen,
  Wrench,
  X,
  XCircle,
} from "lucide-react";
import { useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import {
  createAiConversationMutation,
  getAiConversationOptions,
  getAiConversationQueryKey,
  listAiConversationsOptions,
  listAiConversationsQueryKey,
} from "@/api/@tanstack/react-query.gen";
import { getAiConversation } from "@/api/sdk.gen";
import type { Attachment, Block, PendingAction, StoredMessage } from "@/api/types.gen";
import { DiffView } from "@/components/diff-view";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { FormError } from "@/components/ui/field";
import { Select } from "@/components/ui/input";
import { errorMessage } from "@/lib/errors";
import { cn } from "@/lib/utils";
import {
  attachmentKey,
  attachmentLabel,
  takeAssistantRequest,
  useAssistantRequest,
  type AssistantRequest,
} from "./assistant-request";
import { MentionMenu, mentionQuery, useMentions, type Mention } from "./mentions";
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
function proposalDiffs(
  text: string,
): { path: string; diff: string; issues: { message: string; line: number }[] }[] | undefined {
  try {
    const v = JSON.parse(text) as {
      files?: { path: string; diff: string; issues?: { message: string; line: number }[] }[];
    };
    return v.files?.map((f) => ({ path: f.path, diff: f.diff, issues: f.issues ?? [] }));
  } catch {
    return undefined;
  }
}

function ToolCall({ name, input, mutating }: { name: string; input: unknown; mutating: boolean }) {
  return (
    <div className="flex max-w-full flex-col gap-1 rounded-control border bg-panel/70 px-2.5 py-2 text-xs">
      <span className="flex flex-wrap items-center gap-1.5 font-medium">
        <Wrench className="h-3.5 w-3.5 text-muted-foreground" aria-hidden />
        Tool call {name}
        {mutating && <Badge tone="accent">Needs confirmation</Badge>}
      </span>
      <code className="line-clamp-3 block font-mono break-all text-muted-foreground">{compact(input)}</code>
    </div>
  );
}

function ToolResult({ name, isError, text }: { name: string; isError: boolean; text: string }) {
  const diffs = name === "propose_change" && !isError ? proposalDiffs(text) : undefined;
  return (
    <div className="flex max-w-full flex-col gap-1.5 rounded-control border bg-panel/70 px-2.5 py-2 text-xs">
      <span className="flex flex-wrap items-center gap-1.5 font-medium">
        {isError ? (
          <XCircle className="h-3.5 w-3.5 text-state-failed" aria-hidden />
        ) : (
          <CheckCircle2 className="h-3.5 w-3.5 text-state-success" aria-hidden />
        )}
        Result of {name}
        {isError && <Badge tone="failed">Error</Badge>}
      </span>
      {diffs ? (
        diffs.map((d) => (
          <div key={d.path} className="flex flex-col gap-1">
            <DiffView text={d.diff} label={`Diff of ${d.path}`} />
            {d.issues.length > 0 && (
              <ul aria-label={`Issues of ${d.path}`} className="flex flex-col gap-0.5 text-destructive">
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
        <details className="group">
          <summary className="flex w-fit cursor-pointer list-none items-center gap-1 text-muted-foreground hover:text-foreground [&::-webkit-details-marker]:hidden">
            <ChevronRight className="h-3 w-3 transition-transform duration-200 group-open:rotate-90" aria-hidden />
            Show result
          </summary>
          <code className="mt-1 block max-h-48 overflow-auto rounded-inner bg-muted/60 p-2 font-mono break-all whitespace-pre-wrap">
            {text}
          </code>
        </details>
      )}
    </div>
  );
}

const bubble = "w-fit max-w-[88%] rounded-panel px-3 py-2 text-sm break-words whitespace-pre-wrap";
const userBubble = cn(bubble, "self-end rounded-br-inner bg-accent-fill text-accent-foreground");
const assistantBubble = cn(bubble, "self-start rounded-bl-inner bg-muted text-foreground");

function SavedMessage({ m }: { m: StoredMessage }) {
  const blocks: Block[] = m.content ?? [];
  if (m.role === "user") {
    const attached = blocks.filter((b) => b.type === "attachment");
    return (
      <li className="flex animate-enter flex-col items-end gap-1">
        <span className={userBubble}>
          <span className="sr-only">You: </span>
          {blocks
            .filter((b) => b.type === "text")
            .map((b) => b.text)
            .join("")}
        </span>
        {attached.length > 0 && (
          <span className="flex max-w-[88%] flex-wrap justify-end gap-1">
            {attached.map((b, i) => (
              <Chip key={i} label={b.label ?? "attachment"} />
            ))}
          </span>
        )}
      </li>
    );
  }
  return (
    <li className="flex flex-col gap-1.5">
      {blocks.map((b, i) => {
        if (b.type === "text" && b.text) {
          return (
            <p key={i} className={assistantBubble}>
              <span className="sr-only">Assistant: </span>
              {b.text}
            </p>
          );
        }
        if (b.type === "tool_use")
          return <ToolCall key={i} name={b.tool_name ?? ""} input={b.input} mutating={false} />;
        if (b.type === "tool_result")
          return <ToolResult key={i} name={b.tool_name ?? ""} isError={!!b.is_error} text={b.text ?? ""} />;
        return null;
      })}
    </li>
  );
}

function ActionCard({
  action,
  busy,
  onDecide,
}: {
  action: PendingAction;
  busy: boolean;
  onDecide: (confirm: boolean) => void;
}) {
  return (
    <li
      className="flex animate-enter flex-col gap-2.5 rounded-panel border border-accent/40 bg-accent-soft/50 p-3 text-sm"
      aria-label={`Action ${action.tool}`}
    >
      <span className="flex items-start gap-2 font-medium">
        <ShieldQuestion className="mt-0.5 h-4 w-4 shrink-0 text-accent" aria-hidden />
        <span>
          The assistant wants to run <code className="font-mono text-xs">{action.tool}</code>.
        </span>
      </span>
      <code className="block max-h-40 overflow-auto rounded-control bg-panel/80 p-2 font-mono text-xs break-all">
        {compact(action.arguments)}
      </code>
      <div className="flex justify-end gap-2">
        <Button size="sm" variant="secondary" disabled={busy} onClick={() => onDecide(false)}>
          <X className="h-3.5 w-3.5" aria-hidden />
          Reject
        </Button>
        <Button size="sm" disabled={busy} onClick={() => onDecide(true)}>
          <Check className="h-3.5 w-3.5" aria-hidden />
          Confirm
        </Button>
      </div>
    </li>
  );
}

const isMac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform);

/** Chip shows an attached flow, execution or file. */
function Chip({ label, onRemove }: { label: string; onRemove?: () => void }) {
  return (
    <span className="inline-flex h-6 max-w-full animate-pop-in items-center gap-1 rounded-full border bg-panel px-2 text-xs">
      <AtSign className="h-3 w-3 shrink-0 text-accent" aria-hidden />
      <span className="truncate font-mono">{label}</span>
      {onRemove && (
        <button
          type="button"
          onClick={onRemove}
          className="pressable -mr-1 flex h-4 w-4 items-center justify-center rounded-full text-muted-foreground hover:bg-muted hover:text-foreground"
        >
          <X className="h-3 w-3" aria-hidden />
          <span className="sr-only">Remove {label}</span>
        </button>
      )}
    </span>
  );
}

/** AssistantDrawer is the assistant panel of all routes (REQ-AI-005, Appendix E). It is hidden without an AI provider. */
export function AssistantDrawer() {
  const enabled = useAiEnabled();
  const [open, setOpen] = useState(false);
  const [closing, setClosing] = useState(false);
  const request = useAssistantRequest();
  // A request, for example from "Fix with assistant", opens the panel.
  const [seen, setSeen] = useState<number>();
  if (enabled && request && request.id !== seen) {
    setSeen(request.id);
    setOpen(true);
    setClosing(false);
  }
  if (!enabled) return null;
  // The panel slides out before it unmounts. A timer covers a browser without transitions.
  const close = () => {
    setClosing(true);
    window.setTimeout(() => {
      setOpen(false);
      setClosing(false);
    }, 200);
  };
  return (
    <>
      {!open && (
        <button
          type="button"
          onClick={() => setOpen(true)}
          aria-expanded={false}
          className="pressable fixed right-4 bottom-4 z-30 inline-flex h-10 animate-enter items-center gap-2 rounded-full border bg-panel/85 pr-4 pl-2 text-sm font-medium shadow-float backdrop-blur-2xl backdrop-saturate-150 hover:bg-panel"
        >
          <span className="flex h-7 w-7 items-center justify-center rounded-full bg-accent-fill text-accent-foreground">
            <Sparkles className="h-3.5 w-3.5" aria-hidden />
          </span>
          Assistant
        </button>
      )}
      {open && <Panel closing={closing} onClose={close} requestId={seen} />}
    </>
  );
}

function Panel({ closing, onClose, requestId }: { closing: boolean; onClose: () => void; requestId?: number }) {
  const qc = useQueryClient();
  const [convId, setConvId] = useState<string | undefined>();
  const [draft, setDraft] = useState("");
  const [attached, setAttached] = useState<Attachment[]>([]);
  const [mention, setMention] = useState<{ start: number; query: string }>();
  const [mentionIndex, setMentionIndex] = useState(0);
  const mentions = useMentions(mention !== undefined, mention?.query ?? "");
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const [live, setLive] = useState<LiveItem[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | undefined>();
  const list = useQuery(listAiConversationsOptions());
  const detail = useQuery({
    ...getAiConversationOptions({ path: { conversationId: convId ?? "" } }),
    enabled: !!convId,
  });
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
          if (last?.kind === "text")
            return [...items.slice(0, -1), { kind: "text", text: last.text + String(d.delta ?? "") }];
          return [...items, { kind: "text", text: String(d.delta ?? "") }];
        }
        case "tool_call":
          return [
            ...items,
            { kind: "tool_call", id: String(d.id), name: String(d.name), input: d.input, mutating: !!d.mutating },
          ];
        case "tool_result":
          return [
            ...items,
            {
              kind: "tool_result",
              id: String(d.id),
              name: String(d.name),
              isError: !!d.is_error,
              text: String(d.text ?? ""),
            },
          ];
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

  const sendMessage = async (text: string, attachments: Attachment[], newConversation: boolean) => {
    let id = newConversation ? undefined : convId;
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
    setAttached([]);
    setMention(undefined);
    await run(id, `/api/v1/ai/conversations/${encodeURIComponent(id)}/messages`, {
      text,
      ...(attachments.length > 0 ? { attachments } : {}),
    });
  };

  const send = async (e: FormEvent) => {
    e.preventDefault();
    const text = draft.trim();
    if (!text || busy) return;
    await sendMessage(text, attached, false);
  };

  // A request from another page fills the composer, or starts a conversation with it.
  const handled = useRef<number | undefined>(undefined);
  useEffect(() => {
    if (requestId === undefined || handled.current === requestId) return;
    handled.current = requestId;
    const req: AssistantRequest | undefined = takeAssistantRequest(requestId);
    if (!req) return;
    if (req.send) {
      void sendMessage(req.text, req.attachments, true);
    } else {
      setDraft(req.text);
      setAttached(req.attachments);
      inputRef.current?.focus();
    }
    // sendMessage changes identity on every render; the request id is what matters.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [requestId]);

  const updateMention = (text: string, caret: number) => {
    const m = mentionQuery(text, caret);
    setMention(m);
    if (m?.query !== mention?.query) setMentionIndex(0);
  };

  const pickMention = (m: Mention) => {
    if (!mention) return;
    const el = inputRef.current;
    const caret = el?.selectionStart ?? draft.length;
    const next = draft.slice(0, mention.start) + draft.slice(caret);
    setDraft(next);
    setMention(undefined);
    setAttached((list) =>
      list.some((a) => attachmentKey(a) === attachmentKey(m.attachment)) || list.length >= 5
        ? list
        : [...list, m.attachment],
    );
    requestAnimationFrame(() => {
      el?.focus();
      el?.setSelectionRange(mention.start, mention.start);
    });
  };

  const onComposerKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (mention && mentions.length > 0) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        const step = e.key === "ArrowDown" ? 1 : -1;
        setMentionIndex((i) => (i + step + mentions.length) % mentions.length);
        return;
      }
      if ((e.key === "Enter" && !e.metaKey && !e.ctrlKey) || e.key === "Tab") {
        e.preventDefault();
        const m = mentions[Math.min(mentionIndex, mentions.length - 1)];
        if (m) pickMention(m);
        return;
      }
    }
    if (mention && e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      setMention(undefined);
      return;
    }
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) void send(e);
  };

  const decide = (action: PendingAction, confirm: boolean) => {
    if (!convId) return;
    const verb = confirm ? "confirm" : "reject";
    void run(
      convId,
      `/api/v1/ai/conversations/${encodeURIComponent(convId)}/actions/${encodeURIComponent(action.id)}/${verb}`,
      undefined,
    );
  };

  const pending = (detail.data?.actions ?? []).filter((a) => a.status === "pending");
  const liveActions = new Set(
    live.filter((i) => i.kind === "action").map((i) => (i as { action: PendingAction }).action.id),
  );

  const messages = detail.data?.messages ?? [];
  const empty = messages.length === 0 && live.length === 0 && pending.length === 0 && !busy;

  return (
    <aside
      aria-label="Assistant"
      onKeyDown={(e) => {
        if (e.key === "Escape") onClose();
      }}
      className={cn(
        "fixed top-3 right-3 bottom-3 z-40 flex w-[calc(100%-1.5rem)] max-w-md flex-col overflow-hidden rounded-panel border bg-panel/85 shadow-float backdrop-blur-2xl backdrop-saturate-150 transition-[translate,opacity] starting:translate-x-[calc(100%+1.5rem)] starting:opacity-0",
        closing ? "translate-x-[calc(100%+1.5rem)] opacity-0 duration-200 ease-in" : "duration-[450ms] ease-smooth",
      )}
    >
      <div className="flex flex-col gap-2 border-b px-3 pt-3 pb-2.5">
        <div className="flex items-center gap-2">
          <span className="flex h-6 w-6 items-center justify-center rounded-full bg-accent-fill text-accent-foreground">
            <Sparkles className="h-3.5 w-3.5" aria-hidden />
          </span>
          <h2 className="text-sm font-semibold">Assistant</h2>
          <div className="ml-auto flex items-center gap-0.5">
            <Button
              variant="ghost"
              size="icon"
              className="h-7 w-7"
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
              <SquarePen className="h-4 w-4" aria-hidden />
            </Button>
            <Button
              variant="ghost"
              size="icon"
              className="h-7 w-7"
              aria-label="Close assistant"
              title="Close"
              onClick={onClose}
            >
              <X className="h-4 w-4" aria-hidden />
            </Button>
          </div>
        </div>
        {(list.data?.items.length ?? 0) > 0 && (
          <Select
            aria-label="Conversation"
            className="h-7 text-xs"
            value={convId ?? ""}
            disabled={busy}
            onChange={(e) => setConvId(e.target.value)}
          >
            {list.data?.items.map((c) => (
              <option key={c.id} value={c.id}>
                {c.title || "New conversation"}
              </option>
            ))}
          </Select>
        )}
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3">
        {empty && (
          <div className="flex h-full animate-enter flex-col items-center justify-center gap-3 px-6 text-center">
            <span className="flex h-10 w-10 items-center justify-center rounded-full bg-accent-soft text-accent">
              <Sparkles className="h-5 w-5" aria-hidden />
            </span>
            <p className="text-sm text-muted-foreground">
              Ask about flows, executions and logs. Actions that change something wait for your confirmation.
            </p>
          </div>
        )}
        <ol aria-label="Conversation messages" className="flex flex-col gap-3">
          {messages.map((m) => (
            <SavedMessage key={m.id} m={m} />
          ))}
          {pending
            .filter((a) => !liveActions.has(a.id))
            .map((a) => (
              <ActionCard key={a.id} action={a} busy={busy} onDecide={(c) => decide(a, c)} />
            ))}
          {live.map((item, i) => {
            switch (item.kind) {
              case "text":
                return (
                  <li key={i} className={assistantBubble} aria-live="polite">
                    {item.text}
                  </li>
                );
              case "tool_call":
                return (
                  <li key={i} className="animate-enter">
                    <ToolCall name={item.name} input={item.input} mutating={item.mutating} />
                  </li>
                );
              case "tool_result":
                return (
                  <li key={i} className="animate-enter">
                    <ToolResult name={item.name} isError={item.isError} text={item.text} />
                  </li>
                );
              case "action":
                return <ActionCard key={i} action={item.action} busy={busy} onDecide={(c) => decide(item.action, c)} />;
              case "error":
                return (
                  <li key={i} role="alert" className="flex items-start gap-2 text-sm text-destructive">
                    <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
                    {item.code === "step_limit_reached" ? "The assistant stopped after 20 tool steps." : item.message}
                  </li>
                );
            }
          })}
        </ol>
        {busy && (
          <p role="status" className="mt-3 flex items-center gap-2 text-xs text-muted-foreground">
            <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden />
            The assistant is answering.
          </p>
        )}
        <div ref={endRef} />
      </div>
      <form onSubmit={(e) => void send(e)} className="flex flex-col gap-2 border-t p-3">
        {error && <FormError>{error}</FormError>}
        <div className="relative flex flex-col rounded-panel border border-input/80 bg-panel shadow-[inset_0_1px_1px_rgb(0_0_0/0.04)] transition-[border-color,box-shadow] duration-150 focus-within:border-accent focus-within:shadow-[0_0_0_3px_var(--ring)] dark:bg-background/60">
          {mention && (
            <MentionMenu items={mentions} active={mentionIndex} onPick={pickMention} onHover={setMentionIndex} />
          )}
          {attached.length > 0 && (
            <div className="flex flex-wrap gap-1 px-2.5 pt-2.5" aria-label="Attachments">
              {attached.map((a) => (
                <Chip
                  key={attachmentKey(a)}
                  label={attachmentLabel(a)}
                  onRemove={() => setAttached((list) => list.filter((x) => attachmentKey(x) !== attachmentKey(a)))}
                />
              ))}
            </div>
          )}
          <label htmlFor="assistant-message" className="sr-only">
            Message
          </label>
          <textarea
            id="assistant-message"
            ref={inputRef}
            // Focus moves to the composer when the panel opens.
            autoFocus
            value={draft}
            role="combobox"
            aria-expanded={mention !== undefined}
            aria-controls={mention ? "mention-list" : undefined}
            aria-activedescendant={mention && mentions.length > 0 ? `mention-${mentionIndex}` : undefined}
            aria-autocomplete="list"
            onChange={(e) => {
              setDraft(e.target.value);
              updateMention(e.target.value, e.target.selectionStart);
            }}
            onSelect={(e) => updateMention(e.currentTarget.value, e.currentTarget.selectionStart)}
            onBlur={() => setMention(undefined)}
            onKeyDown={onComposerKey}
            rows={3}
            maxLength={20000}
            placeholder="Ask about flows, executions and logs. Type @ to attach one."
            className="w-full resize-none bg-transparent px-3 pt-2.5 text-sm text-foreground placeholder:text-muted-foreground/80 focus-visible:outline-none"
          />
          <div className="flex items-center justify-between gap-2 px-2 pb-2">
            <span className="pl-1 text-xs text-muted-foreground">{isMac ? "⌘↩" : "Ctrl+Enter"} to send</span>
            <Button type="submit" size="sm" disabled={busy || create.isPending || pending.length > 0 || !draft.trim()}>
              <Send className="h-3.5 w-3.5" aria-hidden />
              Send
            </Button>
          </div>
        </div>
      </form>
    </aside>
  );
}
