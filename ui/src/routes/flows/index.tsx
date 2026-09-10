import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { api, unwrap } from "@/api/client";
import { DataState } from "@/components/data-state";
import { LoadMore } from "@/components/load-more";
import { DisabledBadge, ExecutionStateBadge, ValidBadge } from "@/components/state-badges";
import { Field } from "@/components/ui/field";
import { Select } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import { formatTime } from "@/lib/utils";

type FlowsSearch = { namespace?: string };

export const Route = createFileRoute("/flows/")({
  validateSearch: (s: Record<string, unknown>): FlowsSearch =>
    typeof s.namespace === "string" && s.namespace !== "" ? { namespace: s.namespace } : {},
  component: FlowsPage,
});

function FlowsPage() {
  const search = Route.useSearch();
  const navigate = useNavigate({ from: Route.fullPath });
  const namespaces = useQuery({
    queryKey: ["namespaces"],
    queryFn: async () => unwrap(await api.GET("/api/v1/namespaces")),
  });
  const flows = useInfiniteQuery({
    queryKey: ["flows", "list", search.namespace ?? ""],
    initialPageParam: undefined as string | undefined,
    queryFn: async ({ pageParam }) =>
      unwrap(
        await api.GET("/api/v1/flows", {
          params: { query: { namespace: search.namespace, cursor: pageParam } },
        }),
      ),
    getNextPageParam: (last) => last.next_cursor || undefined,
    select: (d) => d.pages.flatMap((p) => p.items),
  });

  const names = namespaces.data?.items.map((n) => n.name) ?? [];
  if (search.namespace && !names.includes(search.namespace)) names.unshift(search.namespace);

  return (
    <div className="flex flex-col gap-4">
      <PageHeader title="Flows" description="Flows of all namespaces with their state and last execution." />
      <div className="max-w-xs">
        <Field id="flows-namespace" label="Namespace" hint="Shows the namespace and its children.">
          <Select
            value={search.namespace ?? ""}
            onChange={(e) => void navigate({ search: e.target.value ? { namespace: e.target.value } : {} })}
          >
            <option value="">All namespaces</option>
            {names.map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </Select>
        </Field>
      </div>
      <DataState query={flows} empty={(d) => d.length === 0} emptyText="No flows match the filter.">
        {(items) => (
          <>
            <Table>
              <THead>
                <Tr>
                  <Th>Namespace</Th>
                  <Th>Flow ID</Th>
                  <Th>Path</Th>
                  <Th>State</Th>
                  <Th>Last execution</Th>
                  <Th>Description</Th>
                </Tr>
              </THead>
              <TBody>
                {items.map((f) => (
                  <Tr key={f.id}>
                    <Td>{f.namespace}</Td>
                    <Td>
                      <Link
                        to="/flows/$namespace/$flowId"
                        params={{ namespace: f.namespace, flowId: f.flow_id }}
                        className="font-medium text-accent hover:underline"
                      >
                        {f.flow_id}
                      </Link>
                    </Td>
                    <Td className="font-mono text-xs">{f.path}</Td>
                    <Td>
                      <span className="inline-flex items-center gap-3">
                        <ValidBadge valid={f.valid} />
                        {f.disabled && <DisabledBadge />}
                      </span>
                    </Td>
                    <Td>
                      {f.last_execution ? (
                        <span title={formatTime(f.last_execution.created_at)}>
                          <ExecutionStateBadge state={f.last_execution.state} />
                        </span>
                      ) : (
                        <span className="text-muted-foreground">—</span>
                      )}
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
