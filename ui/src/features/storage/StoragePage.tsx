import { useQuery } from "@tanstack/react-query";
import { CheckCircle2, XCircle } from "lucide-react";
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
      <DataState query={status}>
        {(s) => (
          <dl className="grid grid-cols-1 gap-x-6 gap-y-3 rounded-[8px] border bg-panel p-4 text-sm sm:grid-cols-[10rem_minmax(0,1fr)]">
            <dt className="text-muted-foreground">Driver</dt>
            <dd>{driverLabels[s.driver] ?? s.driver}</dd>
            <dt className="text-muted-foreground">Health</dt>
            <dd className="flex flex-col gap-1">
              {s.healthy ? (
                <Badge tone="success" icon={CheckCircle2}>
                  Healthy
                </Badge>
              ) : (
                <Badge tone="failed" icon={XCircle}>
                  Unhealthy
                </Badge>
              )}
              {s.error && <span className="text-state-failed">{s.error}</span>}
            </dd>
          </dl>
        )}
      </DataState>
    </div>
  );
}
