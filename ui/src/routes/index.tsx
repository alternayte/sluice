import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { api, unwrap } from "@/api/client";
import { DataState } from "@/components/data-state";

export const Route = createFileRoute("/")({
  component: Dashboard,
});

function Dashboard() {
  const instances = useQuery({
    queryKey: ["instances"],
    queryFn: async () => unwrap(await api.GET("/api/v1/instances")),
  });
  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-xl font-semibold">Dashboard</h1>
      <section className="rounded-[8px] border bg-panel p-4">
        <h2 className="mb-2 text-sm font-medium">Instances</h2>
        <DataState query={instances} empty={(d) => d.items.length === 0} emptyText="No instances are registered.">
          {(d) => (
            <ul className="text-sm">
              {d.items.map((i) => (
                <li key={i.id} className="flex h-9 items-center gap-3 border-b last:border-0">
                  <span className="font-medium">{i.hostname}</span>
                  <span className="text-muted-foreground">{i.online ? "Online" : "Offline"}</span>
                </li>
              ))}
            </ul>
          )}
        </DataState>
      </section>
    </div>
  );
}
