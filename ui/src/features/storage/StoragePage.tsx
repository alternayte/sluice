import { useQuery } from "@tanstack/react-query";
import { CheckCircle2, HardDrive, XCircle } from "lucide-react";
import { getStorageStatusOptions } from "@/api/@tanstack/react-query.gen";
import { DataState } from "@/components/data-state";
import { Badge } from "@/components/ui/badge";
import { PageHeader } from "@/components/ui/page-header";

const driverLabels: Record<string, string> = {
  postgres: "Postgres",
  fs: "File system",
  s3: "S3 compatible",
  azblob: "Azure Blob",
};

/** StoragePage shows the storage driver and the result of a health round trip (REQ-UI-009). */
export function StoragePage() {
  const status = useQuery(getStorageStatusOptions());
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Storage"
        description="Object storage for file contents, bundles, logs and artifacts. Environment variables set the driver."
      />
      <DataState query={status} skeleton="panel">
        {(s) => (
          <section className="flex max-w-2xl flex-col gap-2">
            <h2 className="px-1 text-sm font-semibold">Object storage</h2>
            <dl className="divide-y rounded-panel border bg-panel text-sm shadow-panel">
              <div className="grid grid-cols-1 gap-x-6 gap-y-1 px-4 py-2.5 sm:grid-cols-[minmax(0,12rem)_minmax(0,1fr)]">
                <dt className="sm:flex sm:h-8 sm:items-center">Driver</dt>
                <dd className="flex min-h-8 items-center gap-2 text-muted-foreground">
                  <HardDrive className="h-3.5 w-3.5" aria-hidden />
                  {driverLabels[s.driver] ?? s.driver}
                </dd>
              </div>
              <div className="grid grid-cols-1 gap-x-6 gap-y-1 px-4 py-2.5 sm:grid-cols-[minmax(0,12rem)_minmax(0,1fr)]">
                <dt className="sm:flex sm:h-8 sm:items-center">Health</dt>
                <dd className="flex min-h-8 min-w-0 flex-col justify-center gap-1">
                  {s.healthy ? (
                    <Badge tone="success" icon={CheckCircle2}>
                      Healthy
                    </Badge>
                  ) : (
                    <Badge tone="failed" icon={XCircle}>
                      Unhealthy
                    </Badge>
                  )}
                  {s.error && <span className="font-mono text-xs break-words text-muted-foreground">{s.error}</span>}
                </dd>
              </div>
            </dl>
            <p className="px-1 text-xs text-muted-foreground">
              The health check writes, reads and deletes a small object each time this page opens.
            </p>
          </section>
        )}
      </DataState>
    </div>
  );
}
