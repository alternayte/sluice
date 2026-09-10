import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { NamespacePage, type NamespaceSearch } from "@/features/namespaces";

export const Route = createFileRoute("/namespaces/$namespace")({
  validateSearch: (s: Record<string, unknown>): NamespaceSearch => {
    const out: NamespaceSearch = {};
    if (s.tab === "versions") out.tab = "versions";
    if (typeof s.file === "string" && s.file !== "") out.file = s.file;
    return out;
  },
  component: function NamespaceRoute() {
    const { namespace } = Route.useParams();
    const search = Route.useSearch();
    const navigate = useNavigate({ from: Route.fullPath });
    return <NamespacePage namespace={namespace} search={search} navigate={navigate} />;
  },
});
