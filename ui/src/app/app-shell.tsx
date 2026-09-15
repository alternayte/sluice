import { useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import * as Tooltip from "@radix-ui/react-tooltip";
import {
  Braces,
  FileClock,
  FolderTree,
  GitBranch,
  HardDrive,
  KeyRound,
  LayoutDashboard,
  LockKeyhole,
  LogOut,
  Menu,
  PanelLeftClose,
  PanelLeftOpen,
  Play,
  Server,
  Sparkles,
  UserRound,
  Users,
  Vault,
  Workflow,
  X,
  type LucideIcon,
} from "lucide-react";
import { useState, type ReactNode } from "react";
import { logout } from "@/api/sdk.gen";
import { AssistantDrawer } from "@/features/ai";
import { useTheme, themes } from "@/lib/theme";
import { useCurrentUser } from "@/lib/auth";
import { can, type Role } from "@/lib/roles";
import { cn } from "@/lib/utils";

type NavItem = { to: string; label: string; min: Role; icon: LucideIcon };

export const navItems: NavItem[] = [
  { to: "/", label: "Dashboard", min: "viewer", icon: LayoutDashboard },
  { to: "/executions", label: "Executions", min: "viewer", icon: Play },
  { to: "/flows", label: "Flows", min: "viewer", icon: Workflow },
  { to: "/namespaces", label: "Namespaces", min: "viewer", icon: FolderTree },
  { to: "/secrets", label: "Secrets", min: "viewer", icon: LockKeyhole },
  { to: "/variables", label: "Variables", min: "viewer", icon: Braces },
];

export const settingsItems: NavItem[] = [
  { to: "/settings/profile", label: "Profile", min: "viewer", icon: UserRound },
  {
    to: "/settings/tokens",
    label: "API tokens",
    min: "viewer",
    icon: KeyRound,
  },
  { to: "/settings/users", label: "Users", min: "admin", icon: Users },
  { to: "/settings/git", label: "Git sources", min: "admin", icon: GitBranch },
  {
    to: "/settings/secret-providers",
    label: "Secret providers",
    min: "admin",
    icon: Vault,
  },
  { to: "/settings/storage", label: "Storage", min: "admin", icon: HardDrive },
  { to: "/settings/ai", label: "AI provider", min: "admin", icon: Sparkles },
  { to: "/settings/instances", label: "Instances", min: "admin", icon: Server },
  { to: "/settings/audit", label: "Audit log", min: "admin", icon: FileClock },
];

const collapsedStorageKey = "sluice-nav-collapsed";

function readCollapsed(): boolean {
  try {
    return localStorage.getItem(collapsedStorageKey) === "1";
  } catch {
    return false;
  }
}

// NavTip shows the label on hover when the side nav shows icons only.
function NavTip({ label, show, children }: { label: string; show: boolean; children: ReactNode }) {
  if (!show) return children;
  return (
    <Tooltip.Root>
      <Tooltip.Trigger asChild>{children}</Tooltip.Trigger>
      <Tooltip.Portal>
        <Tooltip.Content
          side="right"
          sideOffset={8}
          className="z-50 rounded-[6px] bg-foreground px-2 py-1 text-xs text-background shadow"
        >
          {label}
        </Tooltip.Content>
      </Tooltip.Portal>
    </Tooltip.Root>
  );
}

export function ThemeSwitch({ vertical = false }: { vertical?: boolean }) {
  const { theme, setTheme } = useTheme();
  return (
    <div
      role="radiogroup"
      aria-label="Theme"
      className={cn("flex w-fit gap-1 rounded-[6px] border p-0.5", vertical && "flex-col")}
    >
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

function NavLinks({
  items,
  onNavigate,
  collapsed = false,
}: {
  items: NavItem[];
  onNavigate?: () => void;
  collapsed?: boolean;
}) {
  const me = useCurrentUser();
  return (
    <>
      {items
        .filter((item) => can(me.role, item.min))
        .map((item) => (
          <NavTip key={item.to} label={item.label} show={collapsed}>
            <Link
              to={item.to}
              onClick={onNavigate}
              aria-label={collapsed ? item.label : undefined}
              activeOptions={{ exact: item.to === "/" }}
              className={cn(
                "flex items-center gap-2.5 rounded-[6px] py-1.5 text-sm text-muted-foreground hover:bg-muted hover:text-foreground",
                collapsed ? "justify-center px-0" : "px-3",
              )}
              activeProps={{
                className: "bg-accent-soft !text-accent-text font-medium",
              }}
            >
              <item.icon className="h-4 w-4 shrink-0" aria-hidden />
              {!collapsed && item.label}
            </Link>
          </NavTip>
        ))}
    </>
  );
}

function Nav({ onNavigate, collapsed = false }: { onNavigate?: () => void; collapsed?: boolean }) {
  return (
    <nav aria-label="Main" className="flex flex-col gap-0.5">
      <NavLinks items={navItems} onNavigate={onNavigate} collapsed={collapsed} />
      {collapsed ? (
        <div className="mx-2 my-3 border-t" role="separator" />
      ) : (
        <div className="mt-4 px-3 pb-1 text-xs font-medium text-muted-foreground">Settings</div>
      )}
      <NavLinks items={settingsItems} onNavigate={onNavigate} collapsed={collapsed} />
    </nav>
  );
}

function UserBox({ collapsed = false }: { collapsed?: boolean }) {
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

  if (collapsed) {
    return (
      <div className="flex flex-col items-center gap-2">
        <ThemeSwitch vertical />
        <NavTip label={`Sign out ${me.email}`} show>
          <button
            type="button"
            aria-label="Sign out"
            onClick={() => void signOut()}
            disabled={busy}
            className="flex h-8 w-8 items-center justify-center rounded-[6px] text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"
          >
            <LogOut className="h-4 w-4" aria-hidden />
          </button>
        </NavTip>
      </div>
    );
  }

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
          className="inline-flex h-8 items-center gap-1.5 whitespace-nowrap rounded-[6px] px-2 text-sm text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"
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
  const [collapsed, setCollapsedState] = useState(readCollapsed);
  const setCollapsed = (v: boolean) => {
    setCollapsedState(v);
    try {
      localStorage.setItem(collapsedStorageKey, v ? "1" : "0");
    } catch {
      // Storage is not available. The state lasts for this page load.
    }
  };
  const Toggle = collapsed ? PanelLeftOpen : PanelLeftClose;
  return (
    <Tooltip.Provider delayDuration={200}>
      <div className="flex min-h-screen">
        <aside
          className={cn(
            "sticky top-0 hidden h-screen shrink-0 flex-col gap-4 overflow-y-auto border-r bg-panel p-3 md:flex",
            collapsed ? "w-14 px-2" : "w-56",
          )}
        >
          <div className={cn("flex items-center", collapsed ? "justify-center" : "justify-between pl-3")}>
            {!collapsed && <span className="py-1 text-base font-semibold">Sluice</span>}
            <NavTip label="Expand sidebar" show={collapsed}>
              <button
                type="button"
                aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
                aria-expanded={!collapsed}
                onClick={() => setCollapsed(!collapsed)}
                className="flex h-8 w-8 items-center justify-center rounded-[6px] text-muted-foreground hover:bg-muted hover:text-foreground"
              >
                <Toggle className="h-4 w-4" aria-hidden />
              </button>
            </NavTip>
          </div>
          <Nav collapsed={collapsed} />
          <div className="mt-auto">
            <UserBox collapsed={collapsed} />
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
        <AssistantDrawer />
      </div>
    </Tooltip.Provider>
  );
}
