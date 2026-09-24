import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { InsightPanel } from "@/features/ai";
import { ExecutionPage, type DetailSearch } from "@/features/executions";

export const Route = createFileRoute("/executions/$executionId")({
  validateSearch: (s: Record<string, unknown>): DetailSearch => {
    const out: DetailSearch = {};
    if (s.tab === "outputs" || s.tab === "metrics" || s.tab === "artifacts") out.tab = s.tab;
    if (typeof s.task === "string" && s.task !== "") out.task = s.task;
    if (typeof s.run === "string" && s.run !== "") out.run = s.run;
    return out;
  },
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
