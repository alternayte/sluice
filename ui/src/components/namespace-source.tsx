import type { QueryClient } from "@tanstack/react-query";
import { Folder, FolderPen, GitBranch, Lock } from "lucide-react";
import { listNamespacesQueryKey } from "@/api/@tanstack/react-query.gen";
import type { Namespace } from "@/api/types.gen";
import { Badge } from "@/components/ui/badge";

export type { Namespace };

/** SourceBadges shows the source type of a namespace and its read-only state. */
export function SourceBadges({ ns }: { ns: Namespace }) {
  return (
    <span className="inline-flex flex-wrap items-center gap-3">
      {ns.source_type === "managed" && (
        <Badge tone="accent" icon={FolderPen}>
          Managed
        </Badge>
      )}
      {ns.source_type === "git" && (
        <Badge tone="neutral" icon={GitBranch}>
          Git
        </Badge>
      )}
      {ns.source_type === "implicit" && (
        <Badge tone="neutral" icon={Folder}>
          Implicit parent
        </Badge>
      )}
      {(ns.read_only || ns.source_type === "git") && (
        <Badge tone="warning" icon={Lock}>
          Read-only
        </Badge>
      )}
    </span>
  );
}

/** namespaceScopedIds are the generated queries that a change of one namespace affects. */
const namespaceScopedIds = new Set(["getNamespace", "listFiles", "listVersions", "diffVersions"]);

/** flowIds are the generated queries that a change of any namespace flow affects. */
const flowIds = new Set(["listFlows", "getFlow", "listFlowRevisions", "diffFlowRevisions"]);

/** invalidateNamespace refreshes all queries that a change of the namespace affects. */
export function invalidateNamespace(qc: QueryClient, namespace: string) {
  void qc.invalidateQueries({
    predicate: (q) => {
      const key = q.queryKey[0] as { _id?: string; path?: { namespace?: string } } | undefined;
      if (!key?._id) return false;
      if (namespaceScopedIds.has(key._id)) return key.path?.namespace === namespace;
      return flowIds.has(key._id);
    },
  });
  void qc.invalidateQueries({ queryKey: listNamespacesQueryKey() });
  // The file content query is hand-written (rawFetch), not generated; it keys on ["namespace", namespace, "file", path].
  void qc.invalidateQueries({ queryKey: ["namespace", namespace] });
}
