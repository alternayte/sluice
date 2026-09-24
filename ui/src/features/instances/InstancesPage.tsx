import { Fragment } from "react";
import { useQuery } from "@tanstack/react-query";
import { CircleCheck, CircleOff, Server } from "lucide-react";
import { listInstancesOptions } from "@/api/@tanstack/react-query.gen";
import { DataState } from "@/components/data-state";
import { Badge } from "@/components/ui/badge";
import { PageHeader } from "@/components/ui/page-header";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import { formatTime } from "@/lib/utils";

/** Chips shows short names, such as pools, as small tags. */
function Chips({ items }: { items: string[] }) {
  if (items.length === 0) return <span className="text-muted-foreground">—</span>;
  return (
    <div className="flex flex-wrap gap-1">
      {/* The space between chips keeps the words apart in the text, for copy and for screen readers. */}
      {items.map((x, i) => (
        <Fragment key={x}>
          {i > 0 && " "}
          <span className="rounded-inner bg-muted px-1.5 py-px font-mono text-xs">{x}</span>
        </Fragment>
      ))}
    </div>
  );
}

export function InstancesPage() {
  const instances = useQuery({ ...listInstancesOptions(), refetchInterval: 10_000 });
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Instances"
        description="Server instances in this cluster. The list refreshes every 10 seconds."
      />
      <DataState
        query={instances}
        empty={(d) => d.items.length === 0}
        emptyText="No instances are registered."
        emptyIcon={Server}
      >
        {(d) => (
          <Table>
            <THead>
              <Tr>
                <Th>Hostname</Th>
                <Th>Version</Th>
                <Th>Pools</Th>
                <Th>Executors</Th>
                <Th>State</Th>
                <Th>Last heartbeat</Th>
              </Tr>
            </THead>
            <TBody>
              {d.items.map((i) => (
                <Tr key={i.id}>
                  <Td>
                    <div className="flex items-center gap-2">
                      <Server className="h-3.5 w-3.5 text-muted-foreground" aria-hidden />
                      <span className="font-medium">{i.hostname}</span>
                    </div>
                  </Td>
                  <Td className="font-mono text-xs text-muted-foreground">{i.version}</Td>
                  <Td>
                    <Chips items={i.pools} />
                  </Td>
                  <Td>
                    <Chips items={i.executors} />
                  </Td>
                  <Td>
                    {i.online ? (
                      <Badge tone="success" icon={CircleCheck}>
                        Online
                      </Badge>
                    ) : (
                      <Badge tone="neutral" icon={CircleOff}>
                        Offline
                      </Badge>
                    )}
                  </Td>
                  <Td className="text-muted-foreground tabular-nums">{formatTime(i.heartbeat_at)}</Td>
                </Tr>
              ))}
            </TBody>
          </Table>
        )}
      </DataState>
    </div>
  );
}
