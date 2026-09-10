import type { QueryClient } from "@tanstack/react-query";
import { Folder, FolderPen, GitBranch, Lock } from "lucide-react";
import type { components } from "@/api/schema";
import { Badge } from "@/components/ui/badge";

export type Namespace = components["schemas"]["Namespace"];

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

/** invalidateNamespace refreshes all queries that a change of the namespace affects. */
export function invalidateNamespace(qc: QueryClient, namespace: string) {
  void qc.invalidateQueries({ queryKey: ["namespace", namespace] });
  void qc.invalidateQueries({ queryKey: ["namespaces"] });
  void qc.invalidateQueries({ queryKey: ["flows"] });
}
