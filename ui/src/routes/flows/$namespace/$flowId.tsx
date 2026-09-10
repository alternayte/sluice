import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { FlowPage, type FlowSearch } from "@/features/flows";

export const Route = createFileRoute("/flows/$namespace/$flowId")({
  validateSearch: (s: Record<string, unknown>): FlowSearch =>
    s.tab === "executions" || s.tab === "triggers" || s.tab === "source" || s.tab === "revisions"
      ? { tab: s.tab }
      : {},
  component: function FlowRoute() {
    const { namespace, flowId } = Route.useParams();
    const search = Route.useSearch();
    const navigate = useNavigate({ from: Route.fullPath });
    return <FlowPage namespace={namespace} flowId={flowId} search={search} navigate={navigate} />;
  },
});
