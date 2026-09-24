import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Workflow } from "lucide-react";
import { listFlowsInfiniteOptions, listNamespacesOptions } from "@/api/@tanstack/react-query.gen";
import { DataState } from "@/components/data-state";
import { LoadMore } from "@/components/load-more";
import { DisabledBadge, ExecutionStateBadge, ValidBadge } from "@/components/state-badges";
import { Select } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import { formatRelative, formatTime } from "@/lib/utils";

export function FlowsPage({
  search,
  navigate,
}: {
  search: { namespace?: string };
  navigate: (opts: { search: { namespace?: string } }) => void;
}) {
  const namespaces = useQuery(listNamespacesOptions());
  const flows = useInfiniteQuery({
    ...listFlowsInfiniteOptions({ query: { namespace: search.namespace } }),
    initialPageParam: {},
    getNextPageParam: (last) => (last.next_cursor ? { query: { cursor: last.next_cursor } } : undefined),
    select: (d) => d.pages.flatMap((p) => p.items),
  });

  const names = namespaces.data?.items.map((n) => n.name) ?? [];
  if (search.namespace && !names.includes(search.namespace)) names.unshift(search.namespace);

  return (
    <div className="flex flex-col gap-4">
      <PageHeader title="Flows" description="Flows of all namespaces with their state and last execution." />
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
        <label htmlFor="flows-namespace" className="text-sm font-medium">
          Namespace
        </label>
        <Select
          id="flows-namespace"
          aria-describedby="flows-namespace-hint"
          className="w-56 max-w-full"
          value={search.namespace ?? ""}
          onChange={(e) => navigate({ search: e.target.value ? { namespace: e.target.value } : {} })}
        >
          <option value="">All namespaces</option>
          {names.map((n) => (
            <option key={n} value={n}>
              {n}
            </option>
          ))}
        </Select>
        <p id="flows-namespace-hint" className="text-xs text-muted-foreground">
          Shows the namespace and its children.
        </p>
      </div>
      <DataState
        query={flows}
        empty={(d) => d.length === 0}
        emptyText="No flows match the filter."
        emptyIcon={Workflow}
      >
        {(items) => (
          <>
            <Table>
              <THead>
                <Tr>
                  <Th>Flow ID</Th>
                  <Th>Namespace</Th>
                  <Th>State</Th>
                  <Th>Last execution</Th>
                  <Th>Path</Th>
                  <Th>Description</Th>
                </Tr>
              </THead>
              <TBody>
                {items.map((f) => (
                  <Tr key={f.id}>
                    <Td className="max-w-72 truncate" title={f.flow_id}>
                      <Link
                        to="/flows/$namespace/$flowId"
                        params={{ namespace: f.namespace, flowId: f.flow_id }}
                        className="font-medium hover:underline"
                      >
                        {f.flow_id}
                      </Link>
                    </Td>
                    <Td className="font-mono text-xs text-muted-foreground">{f.namespace}</Td>
                    <Td>
                      <span className="inline-flex items-center gap-3">
                        <ValidBadge valid={f.valid} />
                        {f.disabled && <DisabledBadge />}
                      </span>
                    </Td>
                    <Td>
                      {f.last_execution ? (
                        <span
                          className="inline-flex items-center gap-2"
                          title={formatTime(f.last_execution.created_at)}
                        >
                          <ExecutionStateBadge state={f.last_execution.state} />
                          <span className="text-xs text-muted-foreground tabular-nums">
                            {formatRelative(f.last_execution.created_at)}
                          </span>
                        </span>
                      ) : (
                        <span className="text-muted-foreground">—</span>
                      )}
                    </Td>
                    <Td className="max-w-64 truncate font-mono text-xs text-muted-foreground" title={f.path}>
                      {f.path}
                    </Td>
                    <Td className="max-w-80 truncate text-muted-foreground" title={f.description}>
                      {f.description || "—"}
                    </Td>
                  </Tr>
                ))}
              </TBody>
            </Table>
            <LoadMore query={flows} />
          </>
        )}
      </DataState>
    </div>
  );
}
