import { useInfiniteQuery } from "@tanstack/react-query";
import { FileClock, X } from "lucide-react";
import { useState, type FormEvent } from "react";
import { listAuditEventsInfiniteOptions } from "@/api/@tanstack/react-query.gen";
import { DataState } from "@/components/data-state";
import { LoadMore } from "@/components/load-more";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import { formatTime } from "@/lib/utils";

export const filterKeys = ["actor", "action", "target", "from", "to"] as const;
export type FilterKey = (typeof filterKeys)[number];
export type AuditSearch = Partial<Record<FilterKey, string>>;

/** toIso converts a datetime-local value to an RFC 3339 time. */
function toIso(v: string | undefined): string | undefined {
  if (!v) return undefined;
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString();
}

export function AuditPage({ search, onApply }: { search: AuditSearch; onApply: (s: AuditSearch) => void }) {
  const events = useInfiniteQuery({
    ...listAuditEventsInfiniteOptions({
      query: {
        actor: search.actor,
        action: search.action,
        target: search.target,
        from: toIso(search.from),
        to: toIso(search.to),
      },
    }),
    initialPageParam: {},
    getNextPageParam: (last) => (last.next_cursor ? { query: { cursor: last.next_cursor } } : undefined),
    select: (d) => d.pages.flatMap((p) => p.items),
  });

  return (
    <div className="flex flex-col gap-4">
      <PageHeader title="Audit log" description="Changes and sign-ins, newest first." />
      <Filters key={JSON.stringify(search)} initial={search} onApply={onApply} />
      <DataState
        query={events}
        empty={(d) => d.length === 0}
        emptyText="No audit events match the filters."
        emptyIcon={FileClock}
      >
        {(items) => (
          <>
            <Table>
              <THead>
                <Tr>
                  <Th>Time</Th>
                  <Th>Actor</Th>
                  <Th>Action</Th>
                  <Th>Target</Th>
                  <Th>IP</Th>
                  <Th>Details</Th>
                </Tr>
              </THead>
              <TBody>
                {items.map((e) => {
                  const details = Object.keys(e.details ?? {}).length ? JSON.stringify(e.details) : "";
                  return (
                    <Tr key={e.id}>
                      <Td className="text-muted-foreground tabular-nums">{formatTime(e.ts)}</Td>
                      <Td title={`${e.actor_type}:${e.actor_id}`}>{e.actor_label}</Td>
                      <Td>
                        <span className="rounded-inner bg-muted px-1.5 py-px font-mono text-xs">{e.action}</span>
                      </Td>
                      <Td className="max-w-72 truncate font-mono text-xs" title={`${e.target_type}:${e.target_id}`}>
                        <span className="text-muted-foreground">{e.target_type}:</span>
                        {e.target_id}
                      </Td>
                      <Td className="font-mono text-xs text-muted-foreground">{e.ip || "—"}</Td>
                      <Td className="max-w-80 truncate font-mono text-xs text-muted-foreground" title={details}>
                        {details || "—"}
                      </Td>
                    </Tr>
                  );
                })}
              </TBody>
            </Table>
            <LoadMore query={events} />
          </>
        )}
      </DataState>
    </div>
  );
}

const labels: Record<FilterKey, string> = {
  actor: "Actor",
  action: "Action",
  target: "Target",
  from: "From",
  to: "To",
};

const hints: Partial<Record<FilterKey, string>> = {
  actor: "ID or email",
  target: "Type or type:id",
};

function Filters({ initial, onApply }: { initial: AuditSearch; onApply: (s: AuditSearch) => void }) {
  const [values, setValues] = useState<AuditSearch>(initial);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const out: AuditSearch = {};
    for (const k of filterKeys) {
      const v = values[k]?.trim();
      if (v) out[k] = v;
    }
    onApply(out);
  };

  const active = filterKeys.some((k) => initial[k]);

  return (
    <form
      onSubmit={submit}
      aria-label="Filters"
      className="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-panel border bg-panel px-3 py-2 shadow-panel"
    >
      {filterKeys.map((k) => {
        const time = k === "from" || k === "to";
        return (
          <div key={k} className="flex w-full items-center gap-2 sm:w-auto">
            <label htmlFor={`audit-${k}`} className="w-12 shrink-0 text-xs text-muted-foreground sm:w-auto">
              {labels[k]}
            </label>
            <Input
              id={`audit-${k}`}
              type={time ? "datetime-local" : "text"}
              placeholder={hints[k] ?? (time ? undefined : "Any")}
              value={values[k] ?? ""}
              onChange={(e) => setValues({ ...values, [k]: e.target.value })}
              className={time ? "h-7 flex-1 text-xs sm:w-48 sm:flex-none" : "h-7 flex-1 sm:w-36 sm:flex-none"}
            />
          </div>
        );
      })}
      <div className="ml-auto flex items-center gap-1.5">
        {active && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              setValues({});
              onApply({});
            }}
          >
            <X className="h-3.5 w-3.5" aria-hidden />
            Clear
          </Button>
        )}
        <Button type="submit" size="sm">
          Apply filters
        </Button>
      </div>
    </form>
  );
}
