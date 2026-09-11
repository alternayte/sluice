import { useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { LogOut, Menu, X } from "lucide-react";
import { useState, type ReactNode } from "react";
import { logout } from "@/api/sdk.gen";
import { useTheme, themes } from "@/lib/theme";
import { useCurrentUser } from "@/lib/auth";
import { can, type Role } from "@/lib/roles";
import { cn } from "@/lib/utils";

type NavItem = { to: string; label: string; min: Role };

export const navItems: NavItem[] = [
  { to: "/", label: "Dashboard", min: "viewer" },
  { to: "/executions", label: "Executions", min: "viewer" },
  { to: "/flows", label: "Flows", min: "viewer" },
  { to: "/namespaces", label: "Namespaces", min: "viewer" },
  { to: "/secrets", label: "Secrets", min: "viewer" },
  { to: "/variables", label: "Variables", min: "viewer" },
];

export const settingsItems: NavItem[] = [
  { to: "/settings/profile", label: "Profile", min: "viewer" },
  { to: "/settings/tokens", label: "API tokens", min: "viewer" },
  { to: "/settings/users", label: "Users", min: "admin" },
  { to: "/settings/git", label: "Git sources", min: "admin" },
  { to: "/settings/secret-providers", label: "Secret providers", min: "admin" },
  { to: "/settings/instances", label: "Instances", min: "admin" },
  { to: "/settings/audit", label: "Audit log", min: "admin" },
];

export function ThemeSwitch() {
  const { theme, setTheme } = useTheme();
  return (
    <div role="radiogroup" aria-label="Theme" className="flex w-fit gap-1 rounded-[6px] border p-0.5">
      {themes.map((t) => (
        <button
          key={t.value}
          type="button"
          role="radio"
          aria-checked={theme === t.value}
          aria-label={t.label}
          title={t.label}
          onClick={() => setTheme(t.value)}
          className={cn(
            "flex h-7 w-7 items-center justify-center rounded-[4px] text-muted-foreground hover:text-foreground",
            theme === t.value && "bg-accent-soft text-accent",
          )}
        >
          <t.icon className="h-4 w-4" aria-hidden />
        </button>
      ))}
    </div>
  );
}

function NavLinks({ items, onNavigate }: { items: NavItem[]; onNavigate?: () => void }) {
  const me = useCurrentUser();
  return (
    <>
      {items
        .filter((item) => can(me.role, item.min))
        .map((item) => (
          <Link
            key={item.to}
            to={item.to}
            onClick={onNavigate}
            activeOptions={{ exact: item.to === "/" }}
            className="rounded-[6px] px-3 py-1.5 text-sm text-muted-foreground hover:bg-muted hover:text-foreground"
            activeProps={{ className: "bg-accent-soft !text-accent font-medium" }}
          >
            {item.label}
          </Link>
        ))}
    </>
  );
}

function Nav({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <nav aria-label="Main" className="flex flex-col gap-0.5">
      <NavLinks items={navItems} onNavigate={onNavigate} />
      <div className="mt-4 px-3 pb-1 text-xs font-medium text-muted-foreground">Settings</div>
      <NavLinks items={settingsItems} onNavigate={onNavigate} />
    </nav>
  );
}

function UserBox() {
  const me = useCurrentUser();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);

  const signOut = async () => {
    setBusy(true);
    try {
      await logout();
    } finally {
      qc.clear();
      setBusy(false);
      void navigate({ to: "/login" });
    }
  };

  return (
    <div className="flex flex-col gap-2 px-1">
      <div className="truncate text-sm" title={me.email}>
        {me.email}
      </div>
      <div className="flex items-center justify-between gap-2">
        <ThemeSwitch />
        <button
          type="button"
          onClick={() => void signOut()}
          disabled={busy}
          className="inline-flex h-8 items-center gap-1.5 rounded-[6px] px-2 text-sm text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"
        >
          <LogOut className="h-4 w-4" aria-hidden />
          Sign out
        </button>
      </div>
    </div>
  );
}

export function AppShell({ children }: { children: ReactNode }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="flex min-h-screen">
      <aside className="hidden w-56 shrink-0 flex-col gap-4 border-r bg-panel p-3 md:flex">
        <div className="px-3 py-1 text-base font-semibold">Sluice</div>
        <Nav />
        <div className="mt-auto">
          <UserBox />
        </div>
      </aside>
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-12 items-center gap-2 border-b bg-panel px-3 md:hidden">
          <button
            type="button"
            aria-label={open ? "Close menu" : "Open menu"}
            onClick={() => setOpen(!open)}
            className="flex h-8 w-8 items-center justify-center rounded-[6px] hover:bg-muted"
          >
            {open ? <X className="h-4 w-4" /> : <Menu className="h-4 w-4" />}
          </button>
          <span className="font-semibold">Sluice</span>
        </header>
        {open && (
          <div className="flex flex-col gap-3 border-b bg-panel p-3 md:hidden">
            <Nav onNavigate={() => setOpen(false)} />
            <UserBox />
          </div>
        )}
        <main className="min-w-0 flex-1 p-4 md:p-6">{children}</main>
      </div>
    </div>
  );
}
