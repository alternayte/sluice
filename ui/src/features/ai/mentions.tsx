import { useQueries, useQuery } from "@tanstack/react-query";
import { FileCode2, Play, Workflow, type LucideIcon } from "lucide-react";
import { useMemo } from "react";
import {
  listExecutionsOptions,
  listFilesOptions,
  listFlowsOptions,
  listNamespacesOptions,
} from "@/api/@tanstack/react-query.gen";
import type { Attachment } from "@/api/types.gen";
import { matchScore } from "@/lib/commands";
import { cn, formatRelative } from "@/lib/utils";

/** Mention is one entry of the @ menu. */
export type Mention = { key: string; label: string; hint: string; icon: LucideIcon; attachment: Attachment };

/** mentionQuery returns the text after an @ that ends at the caret, or undefined. */
export function mentionQuery(text: string, caret: number): { start: number; query: string } | undefined {
  const m = /(^|\s)@([^\s@]*)$/.exec(text.slice(0, caret));
  if (!m) return undefined;
  return { start: caret - m[2]!.length - 1, query: m[2]! };
}

const maxFileNamespaces = 10;
const maxShown = 8;

/** useMentions lists flows, recent executions and namespace files that match the query. */
export function useMentions(active: boolean, query: string): Mention[] {
  const flows = useQuery({ ...listFlowsOptions({ query: { limit: 200 } }), enabled: active, staleTime: 30_000 });
  const executions = useQuery({
    ...listExecutionsOptions({ query: { limit: 20 } }),
    enabled: active,
    staleTime: 5_000,
  });
  const namespaces = useQuery({ ...listNamespacesOptions(), enabled: active, staleTime: 30_000 });
  const nsNames = (namespaces.data?.items ?? []).filter((n) => !n.implicit).map((n) => n.name);
  const fileNs = nsNames.slice(0, maxFileNamespaces);
  const files = useQueries({
    queries: fileNs.map((ns) => ({
      ...listFilesOptions({ path: { namespace: ns } }),
      enabled: active && query.length > 0,
      staleTime: 60_000,
    })),
  });
  const filesKey = files.map((f) => f.dataUpdatedAt).join(",");

  return useMemo(() => {
    const all: Mention[] = [];
    for (const f of flows.data?.items ?? []) {
      all.push({
        key: `flow:${f.id}`,
        label: `${f.namespace}/${f.flow_id}`,
        hint: "Flow",
        icon: Workflow,
        attachment: { kind: "flow", namespace: f.namespace, flow_id: f.flow_id },
      });
    }
    for (const e of executions.data?.items ?? []) {
      all.push({
        key: `exec:${e.id}`,
        label: `${e.namespace}/${e.flow_id ?? "file run"} ${e.id.slice(0, 8)}`,
        hint: `${e.state.toLowerCase().replace("_", " ")} · ${formatRelative(e.created_at)}`,
        icon: Play,
        attachment: { kind: "execution", execution_id: e.id },
      });
    }
    files.forEach((q, i) => {
      const ns = fileNs[i];
      if (!ns) return;
      for (const f of q.data?.items ?? []) {
        all.push({
          key: `file:${ns}:${f.path}`,
          label: `${ns}:${f.path}`,
          hint: "File",
          icon: FileCode2,
          attachment: { kind: "file", namespace: ns, path: f.path },
        });
      }
    });
    return all
      .map((m) => ({ m, score: matchScore(query, m.label) }))
      .filter((x) => x.score > 0)
      .sort((a, b) => (query ? b.score - a.score : 0))
      .slice(0, maxShown)
      .map((x) => x.m);
    // files changes identity on every render; its data updates are what matter.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [flows.data, executions.data, filesKey, query]);
}

/** MentionMenu is the list of the @ menu above the composer. */
export function MentionMenu({
  items,
  active,
  onPick,
  onHover,
}: {
  items: Mention[];
  active: number;
  onPick: (m: Mention) => void;
  onHover: (i: number) => void;
}) {
  return (
    <div
      id="mention-list"
      role="listbox"
      aria-label="Mentions"
      className="popover absolute right-0 bottom-full left-0 z-10 mb-2 max-h-64 overflow-y-auto rounded-panel border bg-panel/95 p-1 shadow-float backdrop-blur-xl"
      data-state="open"
    >
      {items.length === 0 ? (
        <p className="px-2.5 py-3 text-xs text-muted-foreground">No flow, execution or file matches.</p>
      ) : (
        items.map((m, i) => (
          <div
            key={m.key}
            id={`mention-${i}`}
            role="option"
            aria-selected={i === active}
            onMouseDown={(e) => {
              // Keep the focus in the composer.
              e.preventDefault();
              onPick(m);
            }}
            onMouseMove={() => i !== active && onHover(i)}
            className={cn(
              "flex h-8 cursor-default items-center gap-2 rounded-control px-2.5 text-sm",
              i === active ? "bg-accent-fill text-accent-foreground" : "text-foreground",
            )}
          >
            <m.icon className={cn("h-3.5 w-3.5 shrink-0", i !== active && "text-muted-foreground")} aria-hidden />
            <span className="min-w-0 flex-1 truncate font-mono text-xs">{m.label}</span>
            <span
              className={cn("shrink-0 text-xs", i === active ? "text-accent-foreground/80" : "text-muted-foreground")}
            >
              {m.hint}
            </span>
          </div>
        ))
      )}
    </div>
  );
}
