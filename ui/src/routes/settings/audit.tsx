import { useInfiniteQuery } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { api, unwrap } from "@/api/client";
import { AdminOnly } from "@/components/admin-only";
import { DataState } from "@/components/data-state";
import { LoadMore } from "@/components/load-more";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Table, TBody, Td, Th, THead, Tr } from "@/components/ui/table";
import { formatTime } from "@/lib/utils";

const filterKeys = ["actor", "action", "target", "from", "to"] as const;
type FilterKey = (typeof filterKeys)[number];
type AuditSearch = Partial<Record<FilterKey, string>>;

export const Route = createFileRoute("/settings/audit")({
  validateSearch: (s: Record<string, unknown>): AuditSearch => {
    const out: AuditSearch = {};
    for (const k of filterKeys) {
      const v = s[k];
      if (typeof v === "string" && v !== "") out[k] = v;
    }
    return out;
  },
  component: () => (
    <AdminOnly>
      <AuditPage />
    </AdminOnly>
  ),
});

/** toIso converts a datetime-local value to an RFC 3339 time. */
function toIso(v: string | undefined): string | undefined {
  if (!v) return undefined;
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString();
}

function AuditPage() {
  const search = Route.useSearch();
  const navigate = useNavigate({ from: "/settings/audit" });

  const events = useInfiniteQuery({
    queryKey: ["audit", search],
    initialPageParam: undefined as string | undefined,
    queryFn: async ({ pageParam }) =>
      unwrap(
        await api.GET("/api/v1/audit", {
          params: {
            query: {
              actor: search.actor,
              action: search.action,
              target: search.target,
              from: toIso(search.from),
              to: toIso(search.to),
              cursor: pageParam,
            },
          },
        }),
      ),
    getNextPageParam: (last) => last.next_cursor || undefined,
    select: (d) => d.pages.flatMap((p) => p.items),
  });

  return (
    <div className="flex flex-col gap-4">
      <PageHeader title="Audit log" description="Changes and sign-ins, newest first." />
      <Filters key={JSON.stringify(search)} initial={search} onApply={(s) => void navigate({ search: s })} />
      <DataState query={events} empty={(d) => d.length === 0} emptyText="No audit events match the filters.">
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
                      <Td>{formatTime(e.ts)}</Td>
                      <Td title={`${e.actor_type}:${e.actor_id}`}>{e.actor_label}</Td>
                      <Td className="font-mono text-xs">{e.action}</Td>
                      <Td className="font-mono text-xs">
                        {e.target_type}:{e.target_id}
                      </Td>
                      <Td className="font-mono text-xs">{e.ip || "—"}</Td>
                      <Td className="max-w-80 truncate font-mono text-xs" title={details}>
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

  return (
    <form onSubmit={submit} className="flex flex-col gap-3 rounded-[8px] border bg-panel p-4">
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-5">
        {filterKeys.map((k) => (
          <Field key={k} id={`audit-${k}`} label={labels[k]}>
            <Input
              type={k === "from" || k === "to" ? "datetime-local" : "text"}
              placeholder={hints[k]}
              value={values[k] ?? ""}
              onChange={(e) => setValues({ ...values, [k]: e.target.value })}
            />
          </Field>
        ))}
      </div>
      <div className="flex gap-2">
        <Button type="submit">Apply filters</Button>
        <Button
          variant="secondary"
          onClick={() => {
            setValues({});
            onApply({});
          }}
        >
          Clear
        </Button>
      </div>
    </form>
  );
}
