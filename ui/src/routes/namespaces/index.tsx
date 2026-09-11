import { createFileRoute } from "@tanstack/react-router";
import { NamespacesPage } from "@/features/namespaces";

export const Route = createFileRoute("/namespaces/")({
  component: NamespacesPage,
});
