import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { GitSourcesPage, type GitSourcesSearch } from "@/features/gitsources";

export const Route = createFileRoute("/settings/git")({
  validateSearch: (s: Record<string, unknown>): GitSourcesSearch =>
    typeof s.edit === "string" && s.edit !== "" ? { edit: s.edit } : {},
  component: function GitRoute() {
    const search = Route.useSearch();
    const navigate = useNavigate({ from: Route.fullPath });
    return <GitSourcesPage search={search} navigate={navigate} />;
  },
});
