import { createFileRoute } from "@tanstack/react-router";
import { SecretProvidersPage } from "@/features/secrets";

export const Route = createFileRoute("/settings/secret-providers")({
  component: SecretProvidersPage,
});
