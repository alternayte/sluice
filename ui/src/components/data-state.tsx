import { AlertTriangle, Loader2 } from "lucide-react";
import type { ReactNode } from "react";

/** QueryLike is the part of a TanStack query result that DataState uses. */
export type QueryLike<T> = {
  isPending: boolean;
  isError: boolean;
  error: unknown;
  data: T | undefined;
  refetch: () => unknown;
};

/** DataState renders loading, error and empty states for a query (REQ-UI-010). */
export function DataState<T>({
  query,
  empty,
  emptyText = "Nothing to show.",
  children,
}: {
  query: QueryLike<T>;
  empty?: (data: T) => boolean;
  emptyText?: string;
  children: (data: T) => ReactNode;
}) {
  if (query.isError) {
    return (
      <div role="alert" className="flex flex-wrap items-center gap-2 py-6 text-sm text-destructive">
        <AlertTriangle className="h-4 w-4" aria-hidden />
        {query.error instanceof Error ? query.error.message : "Request failed"}
        <button type="button" className="underline" onClick={() => void query.refetch()}>
          Retry
        </button>
      </div>
    );
  }
  if (query.isPending || query.data === undefined) {
    return (
      <div role="status" className="flex items-center gap-2 py-6 text-sm text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
        Loading
      </div>
    );
  }
  if (empty?.(query.data)) {
    return <div className="py-6 text-sm text-muted-foreground">{emptyText}</div>;
  }
  return <>{children(query.data)}</>;
}
