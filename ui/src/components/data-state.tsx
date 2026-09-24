import { AlertTriangle, Inbox, type LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

/** QueryLike is the part of a TanStack query result that DataState uses. */
export type QueryLike<T> = {
  isPending: boolean;
  isError: boolean;
  error: unknown;
  data: T | undefined;
  refetch: () => unknown;
};

/** Skeleton is a shimmering placeholder in the shape of the content that loads. */
export function Skeleton({ shape = "table", rows = 5 }: { shape?: "table" | "panel" | "lines"; rows?: number }) {
  return (
    <div role="status" aria-live="polite" className="animate-enter">
      <span className="sr-only">Loading</span>
      {shape === "panel" && <div aria-hidden className="skeleton h-40 rounded-panel" />}
      {shape === "lines" && (
        <div aria-hidden className="flex flex-col gap-2 py-2">
          {Array.from({ length: rows }, (_, i) => (
            <div key={i} className="skeleton h-3 rounded-full" style={{ width: `${88 - ((i * 23) % 40)}%` }} />
          ))}
        </div>
      )}
      {shape === "table" && (
        <div aria-hidden className="overflow-hidden rounded-panel border bg-panel shadow-panel">
          <div className="flex h-8 items-center gap-6 border-b px-4">
            <div className="skeleton h-2.5 w-16 rounded-full" />
            <div className="skeleton h-2.5 w-24 rounded-full" />
            <div className="skeleton h-2.5 w-20 rounded-full" />
          </div>
          {Array.from({ length: rows }, (_, i) => (
            <div key={i} className="flex h-9 items-center gap-6 border-b px-4 last:border-0">
              <div className="skeleton h-3 w-16 rounded-full" />
              <div className="skeleton h-3 rounded-full" style={{ width: `${30 + ((i * 17) % 25)}%` }} />
              <div className="skeleton ml-auto h-3 w-20 rounded-full" />
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

/** EmptyState shows an icon, one line and the next action. */
export function EmptyState({
  icon: Icon = Inbox,
  text,
  action,
  className,
}: {
  icon?: LucideIcon;
  text: string;
  action?: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex animate-enter flex-col items-center justify-center gap-3 rounded-panel border border-dashed px-6 py-10 text-center",
        className,
      )}
    >
      <span className="flex h-10 w-10 items-center justify-center rounded-full bg-muted text-muted-foreground">
        <Icon className="h-5 w-5" aria-hidden />
      </span>
      <p className="text-sm text-muted-foreground">{text}</p>
      {action}
    </div>
  );
}

/** ErrorState shows a failed request with a retry. */
export function ErrorState({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <div
      role="alert"
      className="flex animate-enter flex-wrap items-center gap-2 rounded-panel border border-destructive/30 bg-destructive/5 px-4 py-3 text-sm text-destructive"
    >
      <AlertTriangle className="h-4 w-4 shrink-0" aria-hidden />
      <span className="min-w-0 flex-1">{message}</span>
      <Button variant="secondary" size="sm" onClick={onRetry}>
        Retry
      </Button>
    </div>
  );
}

/** DataState renders loading, error and empty states for a query (REQ-UI-010). */
export function DataState<T>({
  query,
  empty,
  emptyText = "Nothing to show.",
  emptyIcon,
  emptyAction,
  skeleton = "table",
  children,
}: {
  query: QueryLike<T>;
  empty?: (data: T) => boolean;
  emptyText?: string;
  emptyIcon?: LucideIcon;
  emptyAction?: ReactNode;
  skeleton?: "table" | "panel" | "lines";
  children: (data: T) => ReactNode;
}) {
  if (query.isError) {
    return (
      <ErrorState
        message={query.error instanceof Error ? query.error.message : "Request failed"}
        onRetry={() => void query.refetch()}
      />
    );
  }
  if (query.isPending || query.data === undefined) {
    return <Skeleton shape={skeleton} />;
  }
  if (empty?.(query.data)) {
    return <EmptyState icon={emptyIcon} text={emptyText} action={emptyAction} />;
  }
  return <>{children(query.data)}</>;
}
