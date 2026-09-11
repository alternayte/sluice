import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { diffVersionsOptions } from "@/api/@tanstack/react-query.gen";
import type { FileDiff } from "@/api/types.gen";
import { DataState } from "@/components/data-state";
import { DiffView } from "@/components/diff-view";
import { Badge } from "@/components/ui/badge";
import { Field } from "@/components/ui/field";
import { Select } from "@/components/ui/input";

const fileStatus: Record<FileDiff["status"], { label: string; tone: "success" | "failed" | "accent" }> = {
  added: { label: "Added", tone: "success" },
  removed: { label: "Removed", tone: "failed" },
  modified: { label: "Modified", tone: "accent" },
};

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
      <h2 id="diff-heading" className="text-base font-semibold">
        Compare versions
      </h2>
      {versions.length < 2 ? (
        <p className="text-sm text-muted-foreground">Two versions are necessary to show a diff.</p>
      ) : (
        <>
          <div className="grid max-w-md grid-cols-2 gap-3">
            <Field id="diff-from" label="From">
              <Select value={from ?? ""} onChange={(e) => setFrom(Number(e.target.value))}>
                {versions.map((v) => (
                  <option key={v} value={v}>
                    v{v}
                  </option>
                ))}
              </Select>
            </Field>
            <Field id="diff-to" label="To">
              <Select value={to ?? ""} onChange={(e) => setTo(Number(e.target.value))}>
                {versions.map((v) => (
                  <option key={v} value={v}>
                    v{v}
                  </option>
                ))}
              </Select>
            </Field>
          </div>
          {!enabled ? (
            <p className="text-sm text-muted-foreground">Select two different versions.</p>
          ) : (
            <DataState query={diff} empty={(d) => d.files.length === 0} emptyText="The versions have the same files.">
              {(d) => (
                <div className="flex flex-col gap-4">
                  {d.files.map((f) => (
                    <div key={f.path} className="flex min-w-0 flex-col gap-1.5">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="font-mono text-sm break-all">{f.path}</span>
                        <Badge tone={fileStatus[f.status].tone}>{fileStatus[f.status].label}</Badge>
                      </div>
                      {f.binary ? (
                        <p className="text-sm text-muted-foreground">Binary file. The diff is not shown.</p>
                      ) : (
                        <DiffView text={f.diff} label={`Diff of ${f.path}`} />
                      )}
                    </div>
                  ))}
                </div>
              )}
            </DataState>
          )}
        </>
      )}
    </section>
  );
}
