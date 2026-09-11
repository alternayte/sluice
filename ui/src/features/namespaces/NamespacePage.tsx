import { useQuery } from "@tanstack/react-query";
import { GitBranch } from "lucide-react";
import { getNamespaceOptions } from "@/api/@tanstack/react-query.gen";
import { DataState } from "@/components/data-state";
import { NamespaceTree } from "@/features/namespaces/NamespaceTree";
import { SourceBadges } from "@/features/namespaces/namespace-source";
import { VersionsPanel } from "@/features/namespaces/VersionsPanel";
import { PageHeader } from "@/components/ui/page-header";
import { Tabs } from "@/components/ui/tabs";
import { useCurrentUser } from "@/lib/auth";
import { can } from "@/lib/roles";

export type Tab = "files" | "versions";
export type NamespaceSearch = { tab?: "versions"; file?: string };

const tabs: { value: Tab; label: string }[] = [
  { value: "files", label: "Files" },
  { value: "versions", label: "Versions" },
];

export function NamespacePage({
  namespace,
  search,
  navigate,
}: {
  namespace: string;
  search: NamespaceSearch;
  navigate: (opts: { search: (prev: NamespaceSearch) => NamespaceSearch }) => void;
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
                onChange={(t) =>
                  navigate({ search: (prev) => ({ ...prev, tab: t === "versions" ? "versions" : undefined }) })
                }
              />
              {tab === "files" ? (
                <NamespaceTree
                  namespace={namespace}
                  canEdit={canEdit}
                  selected={search.file}
                  onSelect={(file) => navigate({ search: (prev) => ({ ...prev, file }) })}
                />
              ) : (
                <VersionsPanel namespace={namespace} canEdit={canEdit} head={ns.head_version ?? undefined} />
              )}
            </>
          );
        }}
      </DataState>
    </div>
  );
}
