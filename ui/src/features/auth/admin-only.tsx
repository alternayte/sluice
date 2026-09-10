import { ShieldAlert } from "lucide-react";
import type { ReactNode } from "react";
import { useCurrentUser } from "@/lib/auth";
import { can } from "@/lib/roles";

/** AdminOnly renders its children only for admins. */
export function AdminOnly({ children }: { children: ReactNode }) {
  const me = useCurrentUser();
  if (!can(me.role, "admin")) {
    return (
      <div role="alert" className="flex items-center gap-2 rounded-[8px] border bg-panel p-4 text-sm text-muted-foreground">
        <ShieldAlert className="h-4 w-4" aria-hidden />
        You need the admin role to open this page.
      </div>
    );
  }
  return <>{children}</>;
}
