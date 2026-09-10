import { createFileRoute } from "@tanstack/react-router";
import { AdminOnly } from "@/features/auth/admin-only";
import { InstancesPage } from "@/features/instances";

export const Route = createFileRoute("/settings/instances")({
  component: () => (
    <AdminOnly>
      <InstancesPage />
    </AdminOnly>
  ),
});
