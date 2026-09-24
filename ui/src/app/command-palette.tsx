import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import {
  CornerDownLeft,
  FileCode2,
  FilePlus,
  FolderTree,
  Keyboard,
  Monitor,
  Moon,
  Play,
  Search,
  Sun,
  Workflow,
  type LucideIcon,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import {
  getFlowOptions,
  listExecutionsOptions,
  listFilesOptions,
  listFlowsOptions,
  listNamespacesOptions,
} from "@/api/@tanstack/react-query.gen";
import { triggerFlow } from "@/api/sdk.gen";
import { navItems, settingsItems } from "@/app/nav";
import { ExecutionStateBadge } from "@/components/state-badges";
import { RunFlowDialog } from "@/components/run-dialogs";
import { toast } from "@/components/ui/toast";
import { useCurrentUser } from "@/lib/auth";
import { matchScore, useContextCommands, type Command } from "@/lib/commands";
import { errorMessage } from "@/lib/errors";
import { executionTitle, flowInputs } from "@/lib/executions";
import { can } from "@/lib/roles";
import { useTheme } from "@/lib/theme";
import { cn, formatRelative } from "@/lib/utils";

type Item = Command & { render?: () => React.ReactNode; rank?: number };

const maxPerGroup = 8;
const fileNamespaceLimit = 25;

/** usePaletteItems builds the palette entries from the API data and the page commands. */
function usePaletteItems(
  open: boolean,
  query: string,
  run: (ns: string, flowId: string) => void,
  onShortcuts: () => void,
) {
  const me = useCurrentUser();
  const navigate = useNavigate();
  const { setTheme } = useTheme();
  const contextCommands = useContextCommands();
  const searching = query.trim() !== "";

  const flows = useQuery({ ...listFlowsOptions({ query: { limit: 200 } }), enabled: open, staleTime: 30_000 });
  const namespaces = useQuery({ ...listNamespacesOptions(), enabled: open, staleTime: 30_000 });
  const executions = useQuery({ ...listExecutionsOptions({ query: { limit: 20 } }), enabled: open, staleTime: 5_000 });
  const nsNames = (namespaces.data?.items ?? []).slice(0, fileNamespaceLimit).map((n) => n.name);
  const files = useQueries({
    queries: nsNames.map((ns) => ({
      ...listFilesOptions({ path: { namespace: ns } }),
      enabled: open && searching,
      staleTime: 60_000,
    })),
  });

  const filesKey = files.map((f) => f.dataUpdatedAt).join(",");

  return useMemo(() => {
    const out: Item[] = [...contextCommands];
    const go = (to: string) => () => void navigate({ to });

    out.push(
      { id: "theme-light", group: "Actions", label: "Switch to light theme", icon: Sun, run: () => setTheme("light") },
      { id: "theme-dark", group: "Actions", label: "Switch to dark theme", icon: Moon, run: () => setTheme("dark") },
      {
        id: "theme-system",
        group: "Actions",
        label: "Use the system theme",
        icon: Monitor,
        run: () => setTheme("system"),
      },
      {
        id: "shortcuts",
        group: "Actions",
        label: "Show keyboard shortcuts",
        icon: Keyboard,
        hint: "?",
        run: onShortcuts,
      },
    );

    for (const item of [...navItems, ...settingsItems]) {
      if (!can(me.role, item.min)) continue;
      out.push({
        id: `nav-${item.to}`,
        group: "Go to",
        label: item.label,
        icon: item.icon,
        hint: item.chord ? `G ${item.chord.toUpperCase()}` : undefined,
        keywords: "page open go",
        run: go(item.to),
      });
    }

    for (const e of executions.data?.items ?? []) {
      const t = executionTitle(e as typeof e & { trigger_payload?: Record<string, unknown> });
      out.push({
        id: `exec-${e.id}`,
        group: "Recent executions",
        label: `${e.namespace}/${t.title}`,
        hint: formatRelative(e.created_at),
        keywords: `${e.id} ${e.state} execution run`,
        render: () => <ExecutionStateBadge state={e.state} />,
        run: () => void navigate({ to: "/executions/$executionId", params: { executionId: e.id } }),
      });
    }

    for (const f of flows.data?.items ?? []) {
      out.push({
        id: `flow-${f.id}`,
        group: "Flows",
        label: `${f.namespace}/${f.flow_id}`,
        icon: Workflow,
        hint: f.description || undefined,
        keywords: "flow open",
        run: () =>
          void navigate({ to: "/flows/$namespace/$flowId", params: { namespace: f.namespace, flowId: f.flow_id } }),
      });
      if (can(me.role, "operator") && f.valid && !f.disabled) {
        out.push({
          id: `run-${f.id}`,
          group: "Run a flow",
          label: `Run ${f.namespace}/${f.flow_id}`,
          icon: Play,
          keywords: "run start trigger execute",
          run: () => run(f.namespace, f.flow_id),
        });
      }
    }

    for (const ns of namespaces.data?.items ?? []) {
      out.push({
        id: `ns-${ns.name}`,
        group: "Namespaces",
        label: ns.name,
        icon: FolderTree,
        hint: ns.description || undefined,
        keywords: "namespace open",
        run: () => void navigate({ to: "/namespaces/$namespace", params: { namespace: ns.name } }),
      });
      if (can(me.role, "editor") && ns.source_type === "managed" && !ns.read_only) {
        out.push({
          id: `new-${ns.name}`,
          group: "Actions",
          label: `New file in ${ns.name}`,
          icon: FilePlus,
          keywords: "create add file",
          run: () =>
            void navigate({ to: "/namespaces/$namespace", params: { namespace: ns.name }, search: { new: true } }),
        });
      }
    }

    files.forEach((q, i) => {
      const ns = nsNames[i];
      if (!ns) return;
      for (const file of q.data?.items ?? []) {
        out.push({
          id: `file-${ns}-${file.path}`,
          group: "Files",
          label: file.path,
          hint: ns,
          icon: FileCode2,
          keywords: `${ns} file`,
          run: () =>
            void navigate({ to: "/namespaces/$namespace", params: { namespace: ns }, search: { file: file.path } }),
        });
      }
    });
    return out;
    // files changes identity on every render; its data updates are what matter.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    contextCommands,
    executions.data,
    flows.data,
    namespaces.data,
    filesKey,
    me.role,
    navigate,
    setTheme,
    run,
    onShortcuts,
  ]);
}

// Groups in the order they show. Unknown groups (from pages) come first.
const groupOrder = ["Actions", "Run a flow", "Go to", "Recent executions", "Flows", "Namespaces", "Files"];
const idleGroups = new Set(["Actions", "Go to", "Recent executions"]);

function rankItems(items: Item[], query: string): { group: string; items: Item[] }[] {
  const searching = query.trim() !== "";
  const scored = items
    .filter((it) => searching || idleGroups.has(it.group) || !groupOrder.includes(it.group))
    .filter((it) => !(!searching && it.id.startsWith("new-")))
    .map((it) => ({ ...it, rank: matchScore(query, `${it.label} ${it.keywords ?? ""} ${it.hint ?? ""}`) }))
    .filter((it) => it.rank > 0);
  const groups = new Map<string, Item[]>();
  for (const it of scored) {
    const list = groups.get(it.group) ?? [];
    list.push(it);
    groups.set(it.group, list);
  }
  const order = (g: string) => (groupOrder.includes(g) ? groupOrder.indexOf(g) : -1);
  return [...groups.entries()]
    .sort(([a], [b]) => order(a) - order(b))
    .map(([group, list]) => ({
      group,
      items: (searching ? list.sort((a, b) => (b.rank ?? 0) - (a.rank ?? 0)) : list).slice(0, maxPerGroup),
    }));
}

/** CommandPalette is the Cmd+K palette: search pages, flows, executions, namespaces and files, and run actions. */
export function CommandPalette({
  open,
  onClose,
  onShortcuts,
}: {
  open: boolean;
  onClose: () => void;
  onShortcuts: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  // The content stays mounted until the close transition ends. Closed, the palette has no controls in the page.
  const [rendered, setRendered] = useState(open);
  if (open && !rendered) setRendered(true);
  const [runTarget, setRunTarget] = useState<{
    namespace: string;
    flowId: string;
    definition: Record<string, unknown>;
  }>();
  const qc = useQueryClient();

  const runFlow = useMemo(
    () => async (namespace: string, flowId: string) => {
      try {
        const flow = await qc.fetchQuery(getFlowOptions({ path: { namespace, flowId } }));
        const definition = (flow.revision?.definition ?? {}) as Record<string, unknown>;
        const needsInput = flowInputs(definition).some(
          (i) => i.required && (i.default === undefined || i.default === null),
        );
        if (needsInput) {
          setRunTarget({ namespace, flowId, definition });
          return;
        }
        const { data } = await triggerFlow({
          path: { namespace, flowId },
          body: { inputs: {}, labels: {} },
          throwOnError: true,
        });
        toast({
          title: `${namespace}/${flowId} started`,
          link: { label: "Open execution", href: `/executions/${data.id}` },
        });
      } catch (err) {
        toast({ tone: "error", title: `Could not run ${namespace}/${flowId}`, description: errorMessage(err) });
      }
    },
    [qc],
  );

  const items = usePaletteItems(open, query, (ns, f) => void runFlow(ns, f), onShortcuts);
  const groups = useMemo(() => rankItems(items, query), [items, query]);
  const flat = groups.flatMap((g) => g.items);
  const current = Math.min(active, Math.max(flat.length - 1, 0));

  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) {
      setQuery("");
      setActive(0);
      d.showModal();
      inputRef.current?.focus();
    }
    if (!open && d.open) d.close();
    if (open) return;
    const t = window.setTimeout(() => setRendered(false), 220);
    return () => window.clearTimeout(t);
  }, [open]);

  useEffect(() => {
    listRef.current?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: "nearest" });
  }, [current, query]);

  const choose = (it: Item | undefined) => {
    if (!it) return;
    onClose();
    it.run();
  };

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    const down = e.key === "ArrowDown" || (e.ctrlKey && e.key === "n");
    const up = e.key === "ArrowUp" || (e.ctrlKey && e.key === "p");
    if (down || up) {
      e.preventDefault();
      if (flat.length) setActive((current + (down ? 1 : -1) + flat.length) % flat.length);
    } else if (e.key === "Enter") {
      e.preventDefault();
      choose(flat[current]);
    }
  };

  let index = -1;
  return (
    <>
      <dialog
        ref={ref}
        onClose={onClose}
        onClick={(e) => {
          if (e.target === ref.current) onClose();
        }}
        aria-label="Command palette"
        className="sheet mx-auto mt-[12vh] mb-auto w-[calc(100%-2rem)] max-w-xl overflow-hidden rounded-panel border bg-panel/90 p-0 text-foreground shadow-float backdrop-blur-2xl backdrop-saturate-150"
      >
        {rendered && (
          <>
            <div className="flex items-center gap-2.5 border-b px-4">
              <Search className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden />
              <input
                ref={inputRef}
                role="combobox"
                aria-expanded="true"
                aria-controls="palette-list"
                aria-activedescendant={flat[current] ? `palette-${flat[current].id}` : undefined}
                aria-label="Search or run a command"
                placeholder="Search flows, executions, files or run a command"
                value={query}
                onChange={(e) => {
                  setQuery(e.target.value);
                  setActive(0);
                }}
                onKeyDown={onKeyDown}
                className="h-12 min-w-0 flex-1 bg-transparent text-base outline-none placeholder:text-muted-foreground"
                autoComplete="off"
                spellCheck={false}
              />
              <kbd className="rounded-inner border px-1.5 font-sans text-xs text-muted-foreground">esc</kbd>
            </div>
            <div
              ref={listRef}
              id="palette-list"
              role="listbox"
              aria-label="Results"
              className="max-h-[min(60vh,26rem)] overflow-y-auto p-1.5"
            >
              {flat.length === 0 && <p className="px-3 py-8 text-center text-sm text-muted-foreground">No results.</p>}
              {groups.map((g) => (
                <div key={g.group} role="group" aria-label={g.group} className="pb-1">
                  <div className="px-2.5 pt-2 pb-1 text-xs font-medium text-muted-foreground">{g.group}</div>
                  {g.items.map((it) => {
                    index++;
                    const selected = index === current;
                    const i = index;
                    const Icon: LucideIcon | undefined = it.icon;
                    return (
                      <div
                        key={it.id}
                        id={`palette-${it.id}`}
                        role="option"
                        aria-selected={selected}
                        onMouseMove={() => active !== i && setActive(i)}
                        onClick={() => choose(it)}
                        className={cn(
                          "flex h-9 cursor-default items-center gap-2.5 rounded-control px-2.5 text-sm",
                          selected ? "bg-accent-fill text-accent-foreground" : "text-foreground",
                        )}
                      >
                        {it.render ? (
                          <span
                            className={cn(
                              "flex w-4 shrink-0 justify-center overflow-hidden",
                              selected && "[&_*]:!text-accent-foreground",
                            )}
                          >
                            {it.render()}
                          </span>
                        ) : (
                          Icon && (
                            <Icon
                              className={cn("h-4 w-4 shrink-0", selected ? "" : "text-muted-foreground")}
                              aria-hidden
                            />
                          )
                        )}
                        <span className="min-w-0 flex-1 truncate">{it.label}</span>
                        {it.hint && (
                          <span
                            className={cn(
                              "max-w-[40%] shrink-0 truncate text-xs",
                              selected ? "text-accent-foreground/80" : "text-muted-foreground",
                            )}
                          >
                            {it.hint}
                          </span>
                        )}
                        {selected && <CornerDownLeft className="h-3.5 w-3.5 shrink-0 opacity-80" aria-hidden />}
                      </div>
                    );
                  })}
                </div>
              ))}
            </div>
            <div className="flex items-center gap-4 border-t px-4 py-2 text-xs text-muted-foreground">
              <span>
                <kbd className="font-sans">↑↓</kbd> move
              </span>
              <span>
                <kbd className="font-sans">↵</kbd> open
              </span>
              <span className="ml-auto">
                <kbd className="font-sans">?</kbd> shortcuts
              </span>
            </div>
          </>
        )}
      </dialog>
      {runTarget && (
        <RunFlowDialog
          namespace={runTarget.namespace}
          flowId={runTarget.flowId}
          definition={runTarget.definition}
          onClose={() => setRunTarget(undefined)}
        />
      )}
    </>
  );
}
