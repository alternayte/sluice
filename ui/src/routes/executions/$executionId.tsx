import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { InsightPanel } from "@/features/ai";
import { ExecutionPage, type DetailSearch } from "@/features/executions";

export const Route = createFileRoute("/executions/$executionId")({
  validateSearch: (s: Record<string, unknown>): DetailSearch =>
    s.tab === "outputs" || s.tab === "metrics" || s.tab === "artifacts" ? { tab: s.tab } : {},
  component: function ExecutionRoute() {
    const { executionId } = Route.useParams();
    const search = Route.useSearch();
    const navigate = useNavigate({ from: Route.fullPath });
    return (
      <ExecutionPage
        executionId={executionId}
        search={search}
        navigate={navigate}
        insights={(e) => <InsightPanel execution={e} />}
      />
    );
  },
});
