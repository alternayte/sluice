import type { UseQueryResult } from "@tanstack/react-query";
import { AlertTriangle, Loader2 } from "lucide-react";
import type { ReactNode } from "react";

/** DataState renders loading, error and empty states for a query (REQ-UI-010). */
export function DataState<T>({
  query,
  empty,
  emptyText = "Nothing to show.",
  children,
}: {
  query: UseQueryResult<T>;
  empty?: (data: T) => boolean;
  emptyText?: string;
  children: (data: T) => ReactNode;
}) {
  if (query.isPending) {
    return (
      <div role="status" className="flex items-center gap-2 py-6 text-sm text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
        Loading
      </div>
    );
  }
  if (query.isError) {
    return (
      <div role="alert" className="flex items-center gap-2 py-6 text-sm text-destructive">
        <AlertTriangle className="h-4 w-4" aria-hidden />
        {query.error instanceof Error ? query.error.message : "Request failed"}
        <button type="button" className="underline" onClick={() => void query.refetch()}>
          Retry
        </button>
      </div>
    );
  }
  if (empty?.(query.data)) {
    return <div className="py-6 text-sm text-muted-foreground">{emptyText}</div>;
  }
  return <>{children(query.data)}</>;
}
