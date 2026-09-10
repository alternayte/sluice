import { Link } from "@tanstack/react-router";
import { Menu, Monitor, Moon, Sun, X } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useTheme, type Theme } from "@/components/theme-provider";
import { cn } from "@/lib/utils";

type NavItem = { to: string; label: string };

export const navItems: NavItem[] = [{ to: "/", label: "Dashboard" }];

const themes: { value: Theme; label: string; icon: typeof Sun }[] = [
  { value: "light", label: "Light", icon: Sun },
  { value: "dark", label: "Dark", icon: Moon },
  { value: "system", label: "System", icon: Monitor },
];

export function ThemeSwitch() {
  const { theme, setTheme } = useTheme();
  return (
    <div role="radiogroup" aria-label="Theme" className="flex gap-1 rounded-[6px] border p-0.5">
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

function Nav({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <nav aria-label="Main" className="flex flex-col gap-0.5">
      {navItems.map((item) => (
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
    </nav>
  );
}

export function AppShell({ children }: { children: ReactNode }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="flex min-h-screen">
      <aside className="hidden w-56 shrink-0 flex-col gap-4 border-r bg-panel p-3 md:flex">
        <div className="px-3 py-1 text-base font-semibold">Sluice</div>
        <Nav />
        <div className="mt-auto px-1">
          <ThemeSwitch />
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
          <div className="border-b bg-panel p-3 md:hidden">
            <Nav onNavigate={() => setOpen(false)} />
            <div className="mt-3">
              <ThemeSwitch />
            </div>
          </div>
        )}
        <main className="min-w-0 flex-1 p-4 md:p-6">{children}</main>
      </div>
    </div>
  );
}
