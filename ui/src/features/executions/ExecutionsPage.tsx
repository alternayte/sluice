import { useInfiniteQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ListFilter, Play, RotateCcw } from "lucide-react";
import { useEffect, useState, type FormEvent } from "react";
import { listExecutionsInfiniteOptions } from "@/api/@tanstack/react-query.gen";
import { DataState } from "@/components/data-state";
import { LabelBadges } from "@/components/execution-bits";
import { LoadMore } from "@/components/load-more";
import { ExecutionStateBadge } from "@/components/state-badges";
import { RunChart } from "@/features/executions/run-chart";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input, Select } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { SegmentedControl } from "@/components/ui/segmented";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import {
  executionStates,
  executionTitle,
  listQuery,
  splitList,
  triggerTypes,
  validateExecutionsSearch,
  type ExecutionsSearch,
  type TriggerType,
} from "@/lib/executions";
import { stateLabel } from "@/lib/flows";
import { cn, formatDuration, formatRelative, formatTime } from "@/lib/utils";

/** useNow returns the current time, updated every second. */
function useNow(): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(t);
  }, []);
  return now;
}

export function ExecutionsPage({
  search,
  onApply,
}: {
  search: ExecutionsSearch;
  onApply: (s: ExecutionsSearch) => void;
}) {
  const query = listQuery(search);
  const now = useNow();
  const list = useInfiniteQuery({
    ...listExecutionsInfiniteOptions({ query: { ...query, limit: 50 } }),
    initialPageParam: {},
    getNextPageParam: (last) => (last.next_cursor ? { query: { cursor: last.next_cursor } } : undefined),
    // REQ-UI-012 allows 2 s for a state change. A 1 s poll keeps the list inside that limit.
    refetchInterval: 1000,
  });
  const filtered = Object.values(search).some((v) => v !== undefined && v !== "");

  return (
    <div className="flex flex-col gap-4">
      <PageHeader title="Executions" description="Every run of a flow or a namespace file, newest first." />
      <Filters key={JSON.stringify(search)} search={search} onApply={onApply} />
      <DataState
        query={{ ...list, data: list.data?.pages.flatMap((p) => p.items) }}
        empty={(d) => d.length === 0}
        emptyText={filtered ? "No executions match the filters." : "No executions yet."}
        emptyIcon={filtered ? ListFilter : Play}
        emptyAction={
          filtered ? (
            <Button variant="secondary" size="sm" onClick={() => onApply({})}>
              Clear the filters
            </Button>
          ) : (
            <Link to="/flows" className="text-sm font-medium text-accent-text hover:underline">
              Run a flow
            </Link>
          )
        }
      >
        {(items) => (
          <div className="flex flex-col gap-4">
            <RunChart items={items} now={now} />
            <div>
              <Table data-testid="executions-table">
                <THead>
                  <Tr>
                    <Th>State</Th>
                    <Th>Namespace</Th>
                    <Th>Flow</Th>
                    <Th>Trigger</Th>
                    <Th>Labels</Th>
                    <Th>Created</Th>
                    <Th className="text-right">Duration</Th>
                  </Tr>
                </THead>
                <TBody>
                  {items.map((e) => {
                    const t = executionTitle(e as typeof e & { trigger_payload?: Record<string, unknown> });
                    const started = e.started_at ? Date.parse(e.started_at) : undefined;
                    const running = e.duration_ms == null && started !== undefined && e.state === "RUNNING";
                    return (
                      <Tr key={e.id} className="animate-enter">
                        <Td>
                          <Link to="/executions/$executionId" params={{ executionId: e.id }} tabIndex={-1}>
                            <ExecutionStateBadge state={e.state} />
                          </Link>
                        </Td>
                        <Td className="font-mono text-xs text-muted-foreground">{e.namespace}</Td>
                        <Td>
                          <Link
                            to="/executions/$executionId"
                            params={{ executionId: e.id }}
                            data-nav-default
                            className="font-medium hover:text-accent-text"
                          >
                            {t.title}
                          </Link>
                          {t.path && <span className="ml-2 font-mono text-xs text-muted-foreground">{t.path}</span>}
                        </Td>
                        <Td className="text-muted-foreground">{stateLabel(e.trigger_type)}</Td>
                        <Td>
                          <LabelBadges labels={e.labels} />
                        </Td>
                        <Td title={formatTime(e.created_at)}>
                          <span className="text-foreground">{formatRelative(e.created_at, now)}</span>
                          <span className="ml-2 text-xs text-muted-foreground">{formatTime(e.created_at)}</span>
                        </Td>
                        <Td className={cn("text-right tabular-nums", running && "text-accent-text")}>
                          {formatDuration(running ? Math.max(now - started!, 0) : e.duration_ms)}
                        </Td>
                      </Tr>
                    );
                  })}
                </TBody>
              </Table>
              <LoadMore query={list} />
            </div>
          </div>
        )}
      </DataState>
    </div>
  );
}

const sortOptions = [
  { value: "created" as const, label: "Created" },
  { value: "duration" as const, label: "Duration" },
];

function Filters({ search, onApply }: { search: ExecutionsSearch; onApply: (s: ExecutionsSearch) => void }) {
  const [states, setStates] = useState<string[]>(splitList(search.state));
  const [namespace, setNamespace] = useState(search.namespace ?? "");
  const [flow, setFlow] = useState(search.flow ?? "");
  const [trigger, setTrigger] = useState<string>(search.trigger_type ?? "");
  const [label, setLabel] = useState(search.label ?? "");
  const [from, setFrom] = useState(search.from ?? "");
  const [to, setTo] = useState(search.to ?? "");
  const [waiting, setWaiting] = useState(search.waiting === "true");

  const build = (sort = search.sort): ExecutionsSearch =>
    validateExecutionsSearch({
      state: states.join(","),
      namespace: namespace.trim(),
      flow: flow.trim(),
      trigger_type: trigger,
      label: label.trim(),
      from,
      to,
      sort,
      waiting: waiting ? "true" : undefined,
    });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    onApply(build());
  };

  return (
    <form
      onSubmit={submit}
      aria-label="Filters"
      className="flex flex-col gap-3 rounded-panel border bg-panel p-3 shadow-panel"
    >
      <div className="flex flex-wrap items-center justify-between gap-3">
        <fieldset className="flex min-w-0 flex-wrap items-center gap-1.5">
          <legend className="sr-only">State</legend>
          {executionStates.map((s) => {
            const on = states.includes(s);
            return (
              <label
                key={s}
                className={cn(
                  "pressable relative inline-flex h-7 cursor-pointer items-center gap-1.5 rounded-full border px-2.5 text-xs font-medium select-none has-[:focus-visible]:outline-3 has-[:focus-visible]:outline-ring",
                  on
                    ? "border-accent/40 bg-accent-soft text-accent-text"
                    : "border-border bg-panel text-muted-foreground hover:bg-muted hover:text-foreground",
                )}
              >
                <input
                  type="checkbox"
                  className="absolute inset-0 cursor-pointer appearance-none rounded-full opacity-0"
                  checked={on}
                  onChange={(e) => setStates((prev) => (e.target.checked ? [...prev, s] : prev.filter((x) => x !== s)))}
                />
                {stateLabel(s.toLowerCase())}
              </label>
            );
          })}
          <label
            className={cn(
              "pressable relative ml-2 inline-flex h-7 cursor-pointer items-center gap-1.5 rounded-full border px-2.5 text-xs font-medium select-none has-[:focus-visible]:outline-3 has-[:focus-visible]:outline-ring",
              waiting
                ? "border-accent/40 bg-accent-soft text-accent-text"
                : "border-border bg-panel text-muted-foreground hover:bg-muted hover:text-foreground",
            )}
          >
            <input
              type="checkbox"
              className="absolute inset-0 cursor-pointer appearance-none rounded-full opacity-0"
              checked={waiting}
              onChange={(e) => setWaiting(e.target.checked)}
            />
            Waiting for an answer
          </label>
        </fieldset>
        <div className="flex items-center gap-2">
          <span aria-hidden className="text-xs text-muted-foreground">
            Sort
          </span>
          <SegmentedControl
            label="Sort"
            size="sm"
            options={sortOptions}
            value={search.sort === "duration" ? "duration" : "created"}
            onChange={(v) => onApply(build(v === "duration" ? "duration" : undefined))}
          />
        </div>
      </div>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-[repeat(3,minmax(0,1fr))_minmax(0,0.8fr)_repeat(2,minmax(0,1fr))_auto] lg:items-start">
        <Field id="f-namespace" label="Namespace">
          <Input value={namespace} onChange={(e) => setNamespace(e.target.value)} placeholder="Any" />
        </Field>
        <Field id="f-flow" label="Flow" hint="namespace/flow_id">
          <Input value={flow} onChange={(e) => setFlow(e.target.value)} placeholder="Any" />
        </Field>
        <Field id="f-label" label="Labels" hint="key=value, separated by space or comma">
          <Input value={label} onChange={(e) => setLabel(e.target.value)} placeholder="team=data" />
        </Field>
        <Field id="f-trigger" label="Trigger type">
          <Select value={trigger} onChange={(e) => setTrigger(e.target.value)}>
            <option value="">All</option>
            {triggerTypes.map((t: TriggerType) => (
              <option key={t} value={t}>
                {stateLabel(t)}
              </option>
            ))}
          </Select>
        </Field>
        <Field id="f-from" label="From">
          <Input type="datetime-local" value={from} onChange={(e) => setFrom(e.target.value)} />
        </Field>
        <Field id="f-to" label="To">
          <Input type="datetime-local" value={to} onChange={(e) => setTo(e.target.value)} />
        </Field>
        <div className="flex gap-2 lg:self-start lg:pt-[1.625rem]">
          <Button type="submit">Apply</Button>
          <Button variant="ghost" onClick={() => onApply({})}>
            <RotateCcw className="h-3.5 w-3.5" aria-hidden />
            Reset
          </Button>
        </div>
      </div>
    </form>
  );
}
