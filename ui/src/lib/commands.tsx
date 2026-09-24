import type { LucideIcon } from "lucide-react";
import { useEffect, useSyncExternalStore } from "react";

/** Command is one entry of the command palette. */
export type Command = {
  id: string;
  label: string;
  /** group is the palette section, for example "Actions". */
  group: string;
  icon?: LucideIcon;
  /** hint is quiet text at the right, for example a namespace or a shortcut. */
  hint?: string;
  /** keywords are extra words that the search matches. */
  keywords?: string;
  run: () => void;
};

// Pages register the commands of their context, for example "Rerun" on an execution page.
// The palette reads them with useContextCommands.
const registry = new Map<number, Command[]>();
let snapshot: Command[] = [];
let nextToken = 1;
const listeners = new Set<() => void>();

function publish() {
  snapshot = [...registry.values()].flat();
  listeners.forEach((l) => l());
}

/** registerCommands adds commands until the returned function removes them. */
export function registerCommands(commands: Command[]): () => void {
  const token = nextToken++;
  registry.set(token, commands);
  publish();
  return () => {
    registry.delete(token);
    publish();
  };
}

/** useCommands registers the commands of a page while it is mounted. Memoize the list. */
export function useCommands(commands: Command[] | undefined) {
  useEffect(() => {
    if (!commands || commands.length === 0) return;
    return registerCommands(commands);
  }, [commands]);
}

const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => listeners.delete(l);
};

/** useContextCommands returns the commands that mounted pages registered. */
export function useContextCommands(): Command[] {
  return useSyncExternalStore(
    subscribe,
    () => snapshot,
    () => snapshot,
  );
}

/**
 * matchScore scores a query against a text: 0 for no match, higher for a better match. A
 * substring beats a subsequence, and a match at a word start beats one inside a word.
 */
export function matchScore(query: string, text: string): number {
  const q = query.trim().toLowerCase();
  if (!q) return 1;
  const t = text.toLowerCase();
  const at = t.indexOf(q);
  if (at >= 0) {
    const wordStart = at === 0 || /[\s/._-]/.test(t[at - 1] ?? "");
    return 1000 - at + (wordStart ? 500 : 0) - t.length / 100;
  }
  let score = 0;
  let ti = 0;
  let run = 0;
  for (const ch of q) {
    if (ch === " ") continue;
    const found = t.indexOf(ch, ti);
    if (found < 0) return 0;
    run = found === ti ? run + 1 : 0;
    score += 1 + run * 2;
    ti = found + 1;
  }
  return score - t.length / 100;
}
