import { createFileRoute } from "@tanstack/react-router";
import { AdminOnly } from "@/features/auth/admin-only";
import { UsersPage } from "@/features/auth";

export const Route = createFileRoute("/settings/users")({
  component: () => (
    <AdminOnly>
      <UsersPage />
    </AdminOnly>
  ),
});
