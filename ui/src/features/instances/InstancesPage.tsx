import { useQuery } from "@tanstack/react-query";
import { CircleCheck, CircleOff } from "lucide-react";
import { listInstancesOptions } from "@/api/@tanstack/react-query.gen";
import { DataState } from "@/components/data-state";
import { Badge } from "@/components/ui/badge";
import { PageHeader } from "@/components/ui/page-header";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import { formatTime } from "@/lib/utils";

export function InstancesPage() {
  const instances = useQuery({ ...listInstancesOptions(), refetchInterval: 10_000 });
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Instances"
        description="Server instances in this cluster. The list refreshes every 10 seconds."
      />
      <DataState query={instances} empty={(d) => d.items.length === 0} emptyText="No instances are registered.">
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
                  <Td className="font-medium">{i.hostname}</Td>
                  <Td className="font-mono text-xs">{i.version}</Td>
                  <Td>{i.pools.join(", ") || "—"}</Td>
                  <Td>{i.executors.join(", ") || "—"}</Td>
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
                  <Td>{formatTime(i.heartbeat_at)}</Td>
                </Tr>
              ))}
            </TBody>
          </Table>
        )}
      </DataState>
    </div>
  );
}
