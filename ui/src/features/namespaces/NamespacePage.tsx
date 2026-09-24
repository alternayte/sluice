import { useQuery } from "@tanstack/react-query";
import { Lock } from "lucide-react";
import { getNamespaceOptions } from "@/api/@tanstack/react-query.gen";
import { DataState } from "@/components/data-state";
import { NamespaceTree } from "@/features/namespaces/NamespaceTree";
import { SourceBadges } from "@/features/namespaces/namespace-source";
import { VersionsPanel } from "@/features/namespaces/VersionsPanel";
import { GitSourcePanel } from "@/features/namespaces/GitSourcePanel";
import { useCallback, type ReactNode } from "react";
import { PageHeader } from "@/components/ui/page-header";
import { Tabs } from "@/components/ui/tabs";
import { useCurrentUser } from "@/lib/auth";
import { can } from "@/lib/roles";

export type Tab = "files" | "versions" | "variables" | "secrets";
/** new opens the new file dialog, for example from the command palette. */
export type NamespaceSearch = { tab?: Exclude<Tab, "files">; file?: string; new?: boolean };

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
  navigate: (opts: { search: (prev: NamespaceSearch) => NamespaceSearch; replace?: boolean }) => void;
  /** scopePanels are the variables and secrets views of the namespace. The route passes them from their features. */
  scopePanels: { variables: ReactNode; secrets: ReactNode };
}) {
  const me = useCurrentUser();
  const info = useQuery(getNamespaceOptions({ path: { namespace } }));
  const tab: Tab = search.tab ?? "files";
  const clearNew = useCallback(
    () => navigate({ search: (prev) => ({ ...prev, new: undefined }), replace: true }),
    [navigate],
  );

  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title={namespace}
        description={info.data?.description || undefined}
        actions={info.data && <SourceBadges ns={info.data} />}
      />
      <DataState query={info} skeleton="panel">
        {(ns) => {
          const canEdit = can(me.role, "editor") && ns.source_type === "managed" && !ns.read_only;
          const canPush = can(me.role, "editor") && ns.source_type === "git";
          return (
            <>
              {ns.read_only === true && ns.source_type !== "git" && (
                <p
                  role="note"
                  className="flex items-center gap-2 rounded-panel border bg-panel px-3 py-2.5 text-sm text-muted-foreground shadow-panel"
                >
                  <Lock className="h-4 w-4 shrink-0 text-accent" aria-hidden />
                  This namespace is read-only.
                </p>
              )}
              {ns.source_type === "git" && <GitSourcePanel namespace={namespace} />}
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
                  canPush={canPush}
                  selected={search.file}
                  onSelect={(file) => navigate({ search: (prev) => ({ ...prev, file }) })}
                  openNew={search.new === true}
                  onNewOpened={clearNew}
                />
              )}
              {tab === "versions" && (
                <VersionsPanel namespace={namespace} canEdit={canEdit} head={ns.head_version ?? undefined} />
              )}
              {tab === "variables" && scopePanels.variables}
              {tab === "secrets" && scopePanels.secrets}
            </>
          );
        }}
      </DataState>
    </div>
  );
}
