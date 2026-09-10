import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { ExecutionsPage } from "@/features/executions";
import { validateExecutionsSearch } from "@/lib/executions";

export const Route = createFileRoute("/executions/")({
  validateSearch: validateExecutionsSearch,
  component: function ExecutionsRoute() {
    const search = Route.useSearch();
    const navigate = useNavigate({ from: Route.fullPath });
    return <ExecutionsPage search={search} onApply={(s) => void navigate({ search: s })} />;
  },
});
