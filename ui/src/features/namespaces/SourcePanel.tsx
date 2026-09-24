import { useQuery } from "@tanstack/react-query";
import { ArrowRight, FileMinus, FilePen, FilePlus, GitCompare, type LucideIcon } from "lucide-react";
import { useState } from "react";
import { diffVersionsOptions } from "@/api/@tanstack/react-query.gen";
import type { FileDiff } from "@/api/types.gen";
import { DataState, EmptyState } from "@/components/data-state";
import { DiffView } from "@/components/diff-view";
import { Badge } from "@/components/ui/badge";
import { Select } from "@/components/ui/input";
import { fileIcon } from "@/features/namespaces/FileTree";

const fileStatus: Record<
  FileDiff["status"],
  { label: string; tone: "success" | "failed" | "accent"; icon: LucideIcon }
> = {
  added: { label: "Added", tone: "success", icon: FilePlus },
  removed: { label: "Removed", tone: "failed", icon: FileMinus },
  modified: { label: "Modified", tone: "accent", icon: FilePen },
};

function VersionSelect({
  id,
  label,
  value,
  versions,
  onChange,
}: {
  id: string;
  label: string;
  value: number | undefined;
  versions: number[];
  onChange: (v: number) => void;
}) {
  return (
    <span className="flex items-center gap-1.5">
      <label htmlFor={id} className="text-xs text-muted-foreground">
        {label}
      </label>
      <Select
        id={id}
        className="w-20 font-mono text-xs"
        value={value ?? ""}
        onChange={(e) => onChange(Number(e.target.value))}
      >
        {versions.map((v) => (
          <option key={v} value={v}>
            v{v}
          </option>
        ))}
      </Select>
    </span>
  );
}

/** SourcePanel compares the source of a namespace between two versions. */
export function SourcePanel({ namespace, versions }: { namespace: string; versions: number[] }) {
  const [from, setFrom] = useState<number | undefined>(versions[1]);
  const [to, setTo] = useState<number | undefined>(versions[0]);
  const enabled = from !== undefined && to !== undefined && from !== to;
  const diff = useQuery({
    ...diffVersionsOptions({ path: { namespace }, query: { from: from ?? 1, to: to ?? 1 } }),
    enabled,
  });

  return (
    <section aria-labelledby="diff-heading" className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <h2 id="diff-heading" className="text-sm font-semibold">
          Compare versions
        </h2>
        {versions.length >= 2 && (
          <div className="flex items-center gap-2">
            <VersionSelect id="diff-from" label="From" value={from} versions={versions} onChange={setFrom} />
            <ArrowRight className="h-3.5 w-3.5 text-muted-foreground" aria-hidden />
            <VersionSelect id="diff-to" label="To" value={to} versions={versions} onChange={setTo} />
          </div>
        )}
      </div>
      {versions.length < 2 ? (
        <EmptyState icon={GitCompare} text="Two versions are necessary to show a diff." />
      ) : !enabled ? (
        <EmptyState icon={GitCompare} text="Select two different versions." />
      ) : (
        <DataState
          query={diff}
          empty={(d) => d.files.length === 0}
          emptyText="The versions have the same files."
          emptyIcon={GitCompare}
          skeleton="panel"
        >
          {(d) => (
            <div className="flex flex-col gap-4">
              <p className="text-xs text-muted-foreground">
                {d.files.length === 1 ? "1 file changed" : `${d.files.length} files changed`}
              </p>
              {d.files.map((f) => {
                const Icon = fileIcon(f.path);
                return (
                  <div key={f.path} className="flex min-w-0 animate-enter flex-col gap-1.5">
                    <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                      <span className="flex min-w-0 items-center gap-1.5">
                        <Icon className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden />
                        <span className="font-mono text-xs font-medium break-all">{f.path}</span>
                      </span>
                      <Badge tone={fileStatus[f.status].tone} icon={fileStatus[f.status].icon}>
                        {fileStatus[f.status].label}
                      </Badge>
                    </div>
                    {f.binary ? (
                      <p className="rounded-panel border border-dashed px-3 py-2 text-xs text-muted-foreground">
                        Binary file. The diff is not shown.
                      </p>
                    ) : (
                      <DiffView text={f.diff} label={`Diff of ${f.path}`} />
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </DataState>
      )}
    </section>
  );
}
