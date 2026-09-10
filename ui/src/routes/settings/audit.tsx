import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { AdminOnly } from "@/features/auth/admin-only";
import { AuditPage, filterKeys, type AuditSearch } from "@/features/audit";

export const Route = createFileRoute("/settings/audit")({
  validateSearch: (s: Record<string, unknown>): AuditSearch => {
    const out: AuditSearch = {};
    for (const k of filterKeys) {
      const v = s[k];
      if (typeof v === "string" && v !== "") out[k] = v;
    }
    return out;
  },
  component: function AuditRoute() {
    const search = Route.useSearch();
    const navigate = useNavigate({ from: "/settings/audit" });
    return (
      <AdminOnly>
        <AuditPage search={search} onApply={(s) => void navigate({ search: s })} />
      </AdminOnly>
    );
  },
});
