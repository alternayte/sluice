import { useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useRouterState } from "@tanstack/react-router";
import * as Tooltip from "@radix-ui/react-tooltip";
import { LogOut, Menu, PanelLeftClose, PanelLeftOpen, Search, Waves, X } from "lucide-react";
import { useCallback, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { logout } from "@/api/sdk.gen";
import { CommandPalette } from "@/app/command-palette";
import { ShortcutSheet, useGlobalShortcuts } from "@/app/keyboard";
import { navItems, settingsItems, type NavItem } from "@/app/nav";
import { AssistantDrawer } from "@/features/ai";
import { Toaster } from "@/components/ui/toast";
import { useTheme, themes } from "@/lib/theme";
import { useCurrentUser } from "@/lib/auth";
import { can } from "@/lib/roles";
import { cn } from "@/lib/utils";

const collapsedStorageKey = "sluice-nav-collapsed";
const isMac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform);

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
          className="popover z-50 rounded-control bg-foreground px-2 py-1 text-xs font-medium text-background shadow-float"
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
      className={cn(
        "flex w-fit gap-0.5 rounded-control bg-foreground/[0.06] p-0.5 dark:bg-white/[0.06]",
        vertical && "flex-col",
      )}
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
            "pressable flex h-6 w-7 items-center justify-center rounded-thumb text-muted-foreground hover:text-foreground",
            theme === t.value && "bg-panel text-foreground shadow-panel dark:bg-input/80",
          )}
        >
          <t.icon className="h-3.5 w-3.5" aria-hidden />
        </button>
      ))}
    </div>
  );
}

/** NavLinks renders a group of nav links. A pill slides to the active link. */
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
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const listRef = useRef<HTMLDivElement>(null);
  const pillRef = useRef<HTMLSpanElement>(null);
  const placed = useRef(false);

  useLayoutEffect(() => {
    const pill = pillRef.current;
    const link = listRef.current?.querySelector<HTMLElement>('a[data-status="active"]');
    if (!pill) return;
    if (!link) {
      pill.style.opacity = "0";
      placed.current = false;
      return;
    }
    pill.style.transitionDuration = placed.current ? "" : "0s";
    pill.style.opacity = "1";
    pill.style.transform = `translateY(${link.offsetTop}px)`;
    pill.style.height = `${link.offsetHeight}px`;
    placed.current = true;
  }, [pathname, collapsed]);

  return (
    <div ref={listRef} className="relative flex flex-col gap-px">
      <span
        ref={pillRef}
        aria-hidden
        className="absolute top-0 right-0 left-0 rounded-control bg-foreground/[0.08] opacity-0 transition-[transform,height,opacity] duration-[420ms] ease-snappy dark:bg-white/[0.09]"
      />
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
                "group relative flex h-8 items-center gap-2.5 rounded-control text-sm text-foreground/80 transition-colors duration-100 hover:bg-foreground/[0.04] hover:text-foreground data-[status=active]:font-medium data-[status=active]:text-foreground data-[status=active]:hover:bg-transparent",
                collapsed ? "justify-center px-0" : "px-2.5",
              )}
            >
              <item.icon
                className="h-4 w-4 shrink-0 text-muted-foreground transition-colors group-data-[status=active]:text-accent"
                aria-hidden
              />
              {!collapsed && <span className="truncate">{item.label}</span>}
            </Link>
          </NavTip>
        ))}
    </div>
  );
}

function Nav({ onNavigate, collapsed = false }: { onNavigate?: () => void; collapsed?: boolean }) {
  return (
    <nav aria-label="Main" className="flex flex-col">
      <NavLinks items={navItems} onNavigate={onNavigate} collapsed={collapsed} />
      {collapsed ? (
        <div className="mx-2 my-3 border-t border-foreground/10" role="separator" />
      ) : (
        <div className="mt-5 px-2.5 pb-1 text-xs font-semibold text-muted-foreground">Settings</div>
      )}
      <NavLinks items={settingsItems} onNavigate={onNavigate} collapsed={collapsed} />
    </nav>
  );
}

function SearchButton({ onClick, collapsed = false }: { onClick: () => void; collapsed?: boolean }) {
  if (collapsed) {
    return (
      <NavTip label="Search" show>
        <button
          type="button"
          onClick={onClick}
          className="pressable flex h-8 w-full items-center justify-center rounded-control text-muted-foreground hover:bg-foreground/[0.06] hover:text-foreground"
        >
          <Search className="h-4 w-4" aria-hidden />
          <span className="sr-only">Open the command palette</span>
        </button>
      </NavTip>
    );
  }
  return (
    <button
      type="button"
      onClick={onClick}
      className="pressable flex h-8 w-full items-center gap-2 rounded-control bg-foreground/[0.05] px-2.5 text-sm text-muted-foreground shadow-[inset_0_0_0_0.5px_var(--border)] hover:bg-foreground/[0.08] dark:bg-white/[0.05]"
    >
      <Search className="h-3.5 w-3.5" aria-hidden />
      <span className="flex-1 text-left">Search</span>
      <kbd className="font-sans text-xs">{isMac ? "⌘K" : "Ctrl K"}</kbd>
    </button>
  );
}

function initials(email: string, name?: string): string {
  const src = name?.trim() || email;
  const parts = src.split(/[\s@._-]+/).filter(Boolean);
  return ((parts[0]?.[0] ?? "") + (parts[1]?.[0] ?? "")).toUpperCase() || "?";
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
            className="pressable flex h-8 w-8 items-center justify-center rounded-control text-muted-foreground hover:bg-foreground/[0.06] hover:text-foreground disabled:opacity-50"
          >
            <LogOut className="h-4 w-4" aria-hidden />
          </button>
        </NavTip>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-2.5 border-t border-foreground/[0.08] pt-3">
      <div className="flex items-center gap-2 px-1">
        <span
          aria-hidden
          className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-gradient-to-b from-accent to-accent-fill text-xs font-semibold text-accent-foreground shadow-panel"
        >
          {initials(me.email, typeof me.name === "string" ? me.name : undefined)}
        </span>
        <div className="truncate text-sm" title={me.email}>
          {me.email}
        </div>
      </div>
      <div className="flex items-center justify-between gap-2">
        <ThemeSwitch />
        <button
          type="button"
          onClick={() => void signOut()}
          disabled={busy}
          className="pressable inline-flex h-7 items-center gap-1.5 rounded-control px-2 text-xs font-medium whitespace-nowrap text-muted-foreground hover:bg-foreground/[0.06] hover:text-foreground disabled:opacity-50"
        >
          <LogOut className="h-3.5 w-3.5" aria-hidden />
          Sign out
        </button>
      </div>
    </div>
  );
}

function Brand() {
  return (
    <Link to="/" className="flex items-center gap-2 rounded-control py-1 text-base font-semibold tracking-tight">
      <span className="flex h-6 w-6 items-center justify-center rounded-control bg-gradient-to-b from-accent to-accent-fill text-accent-foreground shadow-panel">
        <Waves className="h-3.5 w-3.5" aria-hidden />
      </span>
      Sluice
    </Link>
  );
}

export function AppShell({ children }: { children: ReactNode }) {
  const [open, setOpen] = useState(false);
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [sheetOpen, setSheetOpen] = useState(false);
  const [collapsed, setCollapsedState] = useState(readCollapsed);
  const setCollapsed = (v: boolean) => {
    setCollapsedState(v);
    try {
      localStorage.setItem(collapsedStorageKey, v ? "1" : "0");
    } catch {
      // Storage is not available. The state lasts for this page load.
    }
  };
  const togglePalette = useCallback(() => setPaletteOpen((v) => !v), []);
  const openPalette = useCallback(() => setPaletteOpen(true), []);
  const openShortcuts = useCallback(() => {
    setPaletteOpen(false);
    setSheetOpen(true);
  }, []);
  useGlobalShortcuts({ togglePalette, openShortcuts });
  const Toggle = collapsed ? PanelLeftOpen : PanelLeftClose;

  return (
    <Tooltip.Provider delayDuration={200}>
      <div className="window-backdrop flex min-h-screen">
        <aside
          className={cn(
            "sticky top-0 hidden h-screen shrink-0 flex-col gap-4 overflow-y-auto border-r border-foreground/[0.08] bg-sidebar p-3 backdrop-blur-2xl backdrop-saturate-150 transition-[width] duration-300 ease-smooth md:flex",
            collapsed ? "w-14 px-2" : "w-60",
          )}
        >
          <div className={cn("flex items-center", collapsed ? "justify-center" : "justify-between pl-1")}>
            {!collapsed && <Brand />}
            <NavTip label="Expand sidebar" show={collapsed}>
              <button
                type="button"
                aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
                aria-expanded={!collapsed}
                onClick={() => setCollapsed(!collapsed)}
                className="pressable flex h-7 w-7 items-center justify-center rounded-control text-muted-foreground hover:bg-foreground/[0.06] hover:text-foreground"
              >
                <Toggle className="h-4 w-4" aria-hidden />
              </button>
            </NavTip>
          </div>
          <SearchButton onClick={openPalette} collapsed={collapsed} />
          <Nav collapsed={collapsed} />
          <div className="mt-auto">
            <UserBox collapsed={collapsed} />
          </div>
        </aside>
        <div className="flex min-w-0 flex-1 flex-col bg-background">
          <header className="sticky top-0 z-30 flex h-12 items-center gap-2 border-b bg-background/80 px-3 backdrop-blur-xl md:hidden">
            <button
              type="button"
              aria-label={open ? "Close menu" : "Open menu"}
              onClick={() => setOpen(!open)}
              className="pressable flex h-8 w-8 items-center justify-center rounded-control hover:bg-muted"
            >
              {open ? <X className="h-4 w-4" /> : <Menu className="h-4 w-4" />}
            </button>
            <Brand />
            <button
              type="button"
              onClick={openPalette}
              className="pressable ml-auto flex h-8 w-8 items-center justify-center rounded-control hover:bg-muted"
            >
              <Search className="h-4 w-4" aria-hidden />
              <span className="sr-only">Open the command palette</span>
            </button>
          </header>
          {open && (
            <div className="flex animate-enter flex-col gap-3 border-b bg-panel p-3 md:hidden">
              <Nav onNavigate={() => setOpen(false)} />
              <UserBox />
            </div>
          )}
          <main className="page-transition min-w-0 flex-1 px-4 py-5 md:px-8 md:py-7">
            <div className="mx-auto w-full max-w-[1440px]">{children}</div>
          </main>
        </div>
        <AssistantDrawer />
        <CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} onShortcuts={openShortcuts} />
        <ShortcutSheet open={sheetOpen} onClose={() => setSheetOpen(false)} />
        <Toaster />
      </div>
    </Tooltip.Provider>
  );
}
