import type { QueryClient } from "@tanstack/react-query";
import { createRootRouteWithContext } from "@tanstack/react-router";
import { RootShell } from "@/app/root-shell";

export const Route = createRootRouteWithContext<{ queryClient: QueryClient }>()({
  component: RootShell,
});
