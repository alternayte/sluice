import { Link } from "@tanstack/react-router";
import { ExecutionStateBadge } from "@/components/state-badges";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import { formatDuration, formatTime } from "@/lib/utils";

/** LabelBadges shows labels as compact key=value chips. */
export function LabelBadges({ labels }: { labels: Record<string, string> | null | undefined }) {
  const entries = Object.entries(labels ?? {});
  if (entries.length === 0) return <span className="text-muted-foreground">—</span>;
  return (
    <span className="flex flex-wrap gap-1">
      {entries.map(([k, v]) => (
        <span
          key={k}
          className="rounded-control bg-muted px-1.5 font-mono text-xs leading-5 whitespace-nowrap text-foreground/85 shadow-[inset_0_0_0_0.5px_var(--border)]"
        >
          {k}={v}
        </span>
      ))}
    </span>
  );
}

type Row = { id: string; state: string; created_at: string; duration_ms?: number | null };

/** CompactExecutionTable lists executions with state, created time and duration. */
export function CompactExecutionTable({ items }: { items: Row[] }) {
  return (
    <Table>
      <THead>
        <Tr>
          <Th>State</Th>
          <Th>Created</Th>
          <Th className="text-right">Duration</Th>
        </Tr>
      </THead>
      <TBody>
        {items.map((e) => (
          <Tr key={e.id}>
            <Td>
              <Link to="/executions/$executionId" params={{ executionId: e.id }} className="inline-flex">
                <ExecutionStateBadge state={e.state} />
              </Link>
            </Td>
            <Td>
              <Link
                to="/executions/$executionId"
                params={{ executionId: e.id }}
                className="tabular-nums hover:underline"
              >
                {formatTime(e.created_at)}
              </Link>
            </Td>
            <Td className="text-right text-muted-foreground tabular-nums">{formatDuration(e.duration_ms)}</Td>
          </Tr>
        ))}
      </TBody>
    </Table>
  );
}
