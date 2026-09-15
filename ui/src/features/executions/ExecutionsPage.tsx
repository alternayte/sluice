import { useInfiniteQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { listExecutionsInfiniteOptions } from "@/api/@tanstack/react-query.gen";
import { DataState } from "@/components/data-state";
import { LabelBadges } from "@/components/execution-bits";
import { LoadMore } from "@/components/load-more";
import { ExecutionStateBadge } from "@/components/state-badges";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input, Select } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
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
import { formatDuration, formatTime } from "@/lib/utils";

export function ExecutionsPage({
  search,
  onApply,
}: {
  search: ExecutionsSearch;
  onApply: (s: ExecutionsSearch) => void;
}) {
  const query = listQuery(search);
  const list = useInfiniteQuery({
    ...listExecutionsInfiniteOptions({ query: { ...query, limit: 50 } }),
    initialPageParam: {},
    getNextPageParam: (last) => (last.next_cursor ? { query: { cursor: last.next_cursor } } : undefined),
    // REQ-UI-012 allows 2 s for a state change. A 1 s poll keeps the list inside that limit.
    refetchInterval: 1000,
  });

  return (
    <div className="flex flex-col gap-4">
      <PageHeader title="Executions" />
      <Filters key={JSON.stringify(search)} search={search} onApply={onApply} />
      <DataState
        query={{ ...list, data: list.data?.pages.flatMap((p) => p.items) }}
        empty={(d) => d.length === 0}
        emptyText="No executions match the filters."
      >
        {(items) => (
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
                  <Th>Duration</Th>
                </Tr>
              </THead>
              <TBody>
                {items.map((e) => {
                  const t = executionTitle(e as typeof e & { trigger_payload?: Record<string, unknown> });
                  return (
                    <Tr key={e.id} className="hover:bg-muted">
                      <Td>
                        <Link to="/executions/$executionId" params={{ executionId: e.id }}>
                          <ExecutionStateBadge state={e.state} />
                        </Link>
                      </Td>
                      <Td className="font-mono text-xs">{e.namespace}</Td>
                      <Td>
                        <Link
                          to="/executions/$executionId"
                          params={{ executionId: e.id }}
                          className="font-medium hover:underline"
                        >
                          {t.title}
                        </Link>
                        {t.path && <span className="ml-2 font-mono text-xs text-muted-foreground">{t.path}</span>}
                      </Td>
                      <Td>{stateLabel(e.trigger_type)}</Td>
                      <Td>
                        <LabelBadges labels={e.labels} />
                      </Td>
                      <Td>{formatTime(e.created_at)}</Td>
                      <Td className="tabular-nums">{formatDuration(e.duration_ms)}</Td>
                    </Tr>
                  );
                })}
              </TBody>
            </Table>
            <LoadMore query={list} />
          </div>
        )}
      </DataState>
    </div>
  );
}

function Filters({ search, onApply }: { search: ExecutionsSearch; onApply: (s: ExecutionsSearch) => void }) {
  const [states, setStates] = useState<string[]>(splitList(search.state));
  const [namespace, setNamespace] = useState(search.namespace ?? "");
  const [flow, setFlow] = useState(search.flow ?? "");
  const [trigger, setTrigger] = useState<string>(search.trigger_type ?? "");
  const [label, setLabel] = useState(search.label ?? "");
  const [from, setFrom] = useState(search.from ?? "");
  const [to, setTo] = useState(search.to ?? "");

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
    });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    onApply(build());
  };

  return (
    <form onSubmit={submit} aria-label="Filters" className="flex flex-col gap-3 rounded-[8px] border bg-panel p-3">
      <fieldset className="flex flex-col gap-1.5">
        <legend className="mb-1.5 text-sm font-medium">State</legend>
        <div className="flex flex-wrap gap-x-4 gap-y-1">
          {executionStates.map((s) => (
            <label key={s} className="flex items-center gap-1.5 text-sm">
              <input
                type="checkbox"
                className="h-4 w-4"
                checked={states.includes(s)}
                onChange={(e) => setStates((prev) => (e.target.checked ? [...prev, s] : prev.filter((x) => x !== s)))}
              />
              {stateLabel(s.toLowerCase())}
            </label>
          ))}
        </div>
      </fieldset>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <Field id="f-namespace" label="Namespace">
          <Input value={namespace} onChange={(e) => setNamespace(e.target.value)} />
        </Field>
        <Field id="f-flow" label="Flow" hint="namespace/flow_id">
          <Input value={flow} onChange={(e) => setFlow(e.target.value)} />
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
        <Field id="f-label" label="Labels" hint="key=value, separated by space or comma">
          <Input value={label} onChange={(e) => setLabel(e.target.value)} />
        </Field>
        <Field id="f-from" label="From">
          <Input type="datetime-local" value={from} onChange={(e) => setFrom(e.target.value)} />
        </Field>
        <Field id="f-to" label="To">
          <Input type="datetime-local" value={to} onChange={(e) => setTo(e.target.value)} />
        </Field>
        <Field id="f-sort" label="Sort">
          <Select
            value={search.sort ?? "created"}
            onChange={(e) => onApply(build(e.target.value === "duration" ? "duration" : undefined))}
          >
            <option value="created">Created</option>
            <option value="duration">Duration</option>
          </Select>
        </Field>
        <div className="flex items-end gap-2">
          <Button type="submit">Apply</Button>
          <Button variant="secondary" onClick={() => onApply({})}>
            Reset
          </Button>
        </div>
      </div>
    </form>
  );
}
