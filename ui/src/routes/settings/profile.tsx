import { createFileRoute } from "@tanstack/react-router";
import { ProfilePage } from "@/features/auth";

export const Route = createFileRoute("/settings/profile")({
  component: ProfilePage,
});
