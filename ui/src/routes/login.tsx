import { createFileRoute } from "@tanstack/react-router";
import { LoginPage } from "@/features/auth/LoginPage";

type LoginSearch = { redirect?: string };

export const Route = createFileRoute("/login")({
  validateSearch: (s: Record<string, unknown>): LoginSearch =>
    typeof s.redirect === "string" ? { redirect: s.redirect } : {},
  component: function LoginRoute() {
    const { redirect } = Route.useSearch();
    return <LoginPage redirect={redirect} />;
  },
});
