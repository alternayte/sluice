import { createFileRoute } from "@tanstack/react-router";
import { GitSourcesPage } from "@/features/gitsources";

export const Route = createFileRoute("/settings/git")({
  component: GitSourcesPage,
});
