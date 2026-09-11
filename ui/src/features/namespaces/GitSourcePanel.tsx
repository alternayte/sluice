import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, Loader2, RefreshCw, XCircle } from "lucide-react";
import { getNamespaceGitOptions, listGitSyncRunsOptions, syncGitSourceMutation } from "@/api/@tanstack/react-query.gen";
import type { RunOut } from "@/api/types.gen";
import { DataState } from "@/components/data-state";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { FormError } from "@/components/ui/field";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import { useCurrentUser } from "@/lib/auth";
import { errorMessage } from "@/lib/errors";
import { can } from "@/lib/roles";
import { formatTime } from "@/lib/utils";

function RunState({ status }: { status: RunOut["status"] }) {
  if (status === "success") return <Badge tone="success" icon={CheckCircle2}>Success</Badge>;
  if (status === "failed") return <Badge tone="failed" icon={XCircle}>Failed</Badge>;
  return <Badge tone="accent" icon={Loader2}>Running</Badge>;
}

/** GitSourcePanel shows the git source of a namespace, Sync now and the last 50 sync runs (REQ-GIT-004). */
export function GitSourcePanel({ namespace }: { namespace: string }) {
  const me = useCurrentUser();
  const qc = useQueryClient();
  const info = useQuery({ ...getNamespaceGitOptions({ path: { namespace } }), retry: false });
  const sourceId = info.data?.source_id;
  const runs = useQuery({
    ...listGitSyncRunsOptions({ path: { sourceId: sourceId ?? "" } }),
    enabled: !!sourceId,
    refetchInterval: 2000,
    select: (d) => d.items,
  });
  const sync = useMutation({
    ...syncGitSourceMutation(),
    onSuccess: () => {
      void qc.invalidateQueries({ predicate: (q) => (q.queryKey[0] as { _id?: string } | undefined)?._id === "listGitSyncRuns" });
      void qc.invalidateQueries({ predicate: (q) => (q.queryKey[0] as { _id?: string } | undefined)?._id === "getNamespaceGit" });
    },
  });
  if (info.isError) return null;

  return (
    <section aria-label="Git source" className="flex flex-col gap-3 rounded-[8px] border bg-panel p-3">
      <DataState query={info}>
        {(g) => (
          <div className="flex flex-wrap items-start justify-between gap-3">
            <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-sm">
              <dt className="text-muted-foreground">Repository</dt>
              <dd className="truncate font-mono text-xs" title={g.repo_url}>
                {g.repo_url}
              </dd>
              <dt className="text-muted-foreground">Branch</dt>
              <dd className="font-mono text-xs">{g.branch}</dd>
              <dt className="text-muted-foreground">Path</dt>
              <dd className="font-mono text-xs">{g.repo_path || "/"}</dd>
              <dt className="text-muted-foreground">Last sync</dt>
              <dd>
                {g.last_sync_at ? formatTime(g.last_sync_at) : "Never"}
                {g.last_synced_sha && <span className="ml-2 font-mono text-xs">{g.last_synced_sha.slice(0, 12)}</span>}
              </dd>
              {g.last_error && (
                <>
                  <dt className="text-muted-foreground">Last error</dt>
                  <dd className="text-state-failed">{g.last_error}</dd>
                </>
              )}
            </dl>
            {can(me.role, "operator") && (
              <Button size="sm" variant="secondary" disabled={sync.isPending} onClick={() => sync.mutate({ path: { sourceId: g.source_id } })}>
                <RefreshCw className="h-3.5 w-3.5" aria-hidden />
                Sync now
              </Button>
            )}
          </div>
        )}
      </DataState>
      {sync.isError && <FormError>{errorMessage(sync.error)}</FormError>}
      {sourceId && (
        <DataState query={runs} empty={(d) => d.length === 0} emptyText="No sync runs yet.">
          {(items) => (
            <div className="max-h-72 overflow-auto">
              <Table aria-label="Sync runs">
                <THead>
                  <Tr>
                    <Th>Started</Th>
                    <Th>State</Th>
                    <Th>Commit</Th>
                    <Th className="text-right">Snapshots</Th>
                    <Th>Details</Th>
                  </Tr>
                </THead>
                <TBody>
                  {items.map((r) => (
                    <Tr key={r.id}>
                      <Td>{formatTime(r.started_at)}</Td>
                      <Td>
                        <RunState status={r.status} />
                      </Td>
                      <Td className="font-mono text-xs">{r.sha.slice(0, 12)}</Td>
                      <Td className="text-right">{r.snapshots_created}</Td>
                      <Td className="text-xs">
                        {r.error && <span className="text-state-failed">{r.error}</span>}
                        {r.warnings.length > 0 && (
                          <span className="text-muted-foreground" title={r.warnings.join("\n")}>
                            {r.error ? " · " : ""}
                            {r.warnings.length === 1 ? "1 warning" : `${r.warnings.length} warnings`}
                          </span>
                        )}
                      </Td>
                    </Tr>
                  ))}
                </TBody>
              </Table>
            </div>
          )}
        </DataState>
      )}
    </section>
  );
}
