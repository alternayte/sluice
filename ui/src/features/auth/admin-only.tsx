import { ShieldAlert } from "lucide-react";
import type { ReactNode } from "react";
import { EmptyState } from "@/components/data-state";
import { useCurrentUser } from "@/lib/auth";
import { can } from "@/lib/roles";

/** AdminOnly renders its children only for admins. */
export function AdminOnly({ children }: { children: ReactNode }) {
  const me = useCurrentUser();
  if (!can(me.role, "admin")) {
    return (
      <div role="alert">
        <EmptyState icon={ShieldAlert} text="You need the admin role to open this page." />
      </div>
    );
  }
  return <>{children}</>;
}
