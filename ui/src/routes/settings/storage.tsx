import { createFileRoute } from "@tanstack/react-router";
import { StoragePage } from "@/features/storage";

export const Route = createFileRoute("/settings/storage")({
  component: StoragePage,
});
