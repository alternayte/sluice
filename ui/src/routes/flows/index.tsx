import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { FlowsPage } from "@/features/flows";

type FlowsSearch = { namespace?: string };

export const Route = createFileRoute("/flows/")({
  validateSearch: (s: Record<string, unknown>): FlowsSearch =>
    typeof s.namespace === "string" && s.namespace !== "" ? { namespace: s.namespace } : {},
  component: function FlowsRoute() {
    const search = Route.useSearch();
    const navigate = useNavigate({ from: Route.fullPath });
    return <FlowsPage search={search} navigate={navigate} />;
  },
});
