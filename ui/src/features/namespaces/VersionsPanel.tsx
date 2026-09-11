import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RotateCcw } from "lucide-react";
import { useState } from "react";
import { listVersionsOptions, revertVersionMutation } from "@/api/@tanstack/react-query.gen";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DataState } from "@/components/data-state";
import { invalidateNamespace } from "@/features/namespaces/namespace-source";
import { SourcePanel } from "@/features/namespaces/SourcePanel";
import { Button } from "@/components/ui/button";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import { errorMessage } from "@/lib/errors";
import { formatTime } from "@/lib/utils";

export function VersionsPanel({ namespace, canEdit, head }: { namespace: string; canEdit: boolean; head?: number }) {
  const [revert, setRevert] = useState<number | null>(null);
  const versions = useQuery(listVersionsOptions({ path: { namespace }, query: { limit: 100 } }));

  return (
    <DataState query={versions} empty={(d) => d.items.length === 0} emptyText="This namespace has no versions.">
      {(d) => (
        <div className="flex flex-col gap-6">
          <Table>
            <THead>
              <Tr>
                <Th>Version</Th>
                <Th>Author</Th>
                <Th>Message</Th>
                <Th>Time</Th>
                {canEdit && (
                  <Th>
                    <span className="sr-only">Actions</span>
                  </Th>
                )}
              </Tr>
            </THead>
            <TBody>
              {d.items.map((s) => (
                <Tr key={s.id}>
                  <Td className="font-mono text-xs">
                    {s.version != null ? `v${s.version}` : (s.git_sha ?? "").slice(0, 12)}
                    {s.version != null && s.version === head && (
                      <span className="ml-2 font-sans text-muted-foreground">Head</span>
                    )}
                  </Td>
                  <Td>{s.author}</Td>
                  <Td className="max-w-96 truncate" title={s.message}>
                    {s.message}
                  </Td>
                  <Td>{formatTime(s.created_at)}</Td>
                  {canEdit && (
                    <Td className="text-right">
                      {s.version != null && s.version !== head && (
                        <Button size="sm" variant="secondary" onClick={() => setRevert(s.version ?? null)}>
                          <RotateCcw className="h-3.5 w-3.5" aria-hidden />
                          Revert to this version
                        </Button>
                      )}
                    </Td>
                  )}
                </Tr>
              ))}
            </TBody>
          </Table>
          <SourcePanel
            namespace={namespace}
            versions={d.items.flatMap((s) => (s.version == null ? [] : [s.version]))}
          />
          {revert !== null && <RevertDialog namespace={namespace} version={revert} onClose={() => setRevert(null)} />}
        </div>
      )}
    </DataState>
  );
}

function RevertDialog({ namespace, version, onClose }: { namespace: string; version: number; onClose: () => void }) {
  const qc = useQueryClient();
  const revert = useMutation({
    ...revertVersionMutation(),
    onSuccess: () => {
      invalidateNamespace(qc, namespace);
      onClose();
    },
  });
  return (
    <ConfirmDialog
      open
      title="Revert namespace"
      confirmLabel="Revert"
      pending={revert.isPending}
      error={revert.isError ? errorMessage(revert.error) : undefined}
      onConfirm={() => revert.mutate({ path: { namespace }, body: { version } })}
      onClose={onClose}
    >
      Create a new version with the content of version {version}?
    </ConfirmDialog>
  );
}
