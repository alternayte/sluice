import { useQuery } from "@tanstack/react-query";
import { GitBranch } from "lucide-react";
import { getNamespaceOptions } from "@/api/@tanstack/react-query.gen";
import { DataState } from "@/components/data-state";
import { NamespaceTree } from "@/features/namespaces/NamespaceTree";
import { SourceBadges } from "@/features/namespaces/namespace-source";
import { VersionsPanel } from "@/features/namespaces/VersionsPanel";
import type { ReactNode } from "react";
import { PageHeader } from "@/components/ui/page-header";
import { Tabs } from "@/components/ui/tabs";
import { useCurrentUser } from "@/lib/auth";
import { can } from "@/lib/roles";

export type Tab = "files" | "versions" | "variables" | "secrets";
export type NamespaceSearch = { tab?: Exclude<Tab, "files">; file?: string };

const tabs: { value: Tab; label: string }[] = [
  { value: "files", label: "Files" },
  { value: "versions", label: "Versions" },
  { value: "variables", label: "Variables" },
  { value: "secrets", label: "Secrets" },
];

export function NamespacePage({
  namespace,
  search,
  navigate,
  scopePanels,
}: {
  namespace: string;
  search: NamespaceSearch;
  navigate: (opts: { search: (prev: NamespaceSearch) => NamespaceSearch }) => void;
  /** scopePanels are the variables and secrets views of the namespace. The route passes them from their features. */
  scopePanels: { variables: ReactNode; secrets: ReactNode };
}) {
  const me = useCurrentUser();
  const info = useQuery(getNamespaceOptions({ path: { namespace } }));
  const tab: Tab = search.tab ?? "files";

  return (
    <div className="flex flex-col gap-4">
      <PageHeader title={namespace} description={info.data?.description || undefined} />
      <DataState query={info}>
        {(ns) => {
          const isGit = ns.source_type === "git" || ns.read_only === true;
          const canEdit = can(me.role, "editor") && ns.source_type === "managed" && !ns.read_only;
          return (
            <>
              <SourceBadges ns={ns} />
              {isGit && (
                <p
                  role="note"
                  className="flex items-center gap-2 rounded-[8px] border bg-panel p-3 text-sm text-muted-foreground"
                >
                  <GitBranch className="h-4 w-4 shrink-0" aria-hidden />
                  This namespace comes from git and is read-only.
                </p>
              )}
              <Tabs
                label="Namespace sections"
                tabs={tabs}
                value={tab}
                onChange={(t) => navigate({ search: (prev) => ({ ...prev, tab: t === "files" ? undefined : t }) })}
              />
              {tab === "files" && (
                <NamespaceTree
                  namespace={namespace}
                  canEdit={canEdit}
                  selected={search.file}
                  onSelect={(file) => navigate({ search: (prev) => ({ ...prev, file }) })}
                />
              )}
              {tab === "versions" && <VersionsPanel namespace={namespace} canEdit={canEdit} head={ns.head_version ?? undefined} />}
              {tab === "variables" && scopePanels.variables}
              {tab === "secrets" && scopePanels.secrets}
            </>
          );
        }}
      </DataState>
    </div>
  );
}
