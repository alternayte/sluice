import { useNavigate, useRouterState } from "@tanstack/react-router";
import { useEffect, useRef } from "react";
import { navItems, settingsItems } from "@/app/nav";
import { Dialog } from "@/components/ui/dialog";
import { useCurrentUser } from "@/lib/auth";
import { can } from "@/lib/roles";

/** isTyping reports whether a key event goes to a text field or the code editor. */
export function isTyping(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  if (target.isContentEditable || target.closest(".cm-editor")) return true;
  const tag = target.tagName;
  if (tag === "TEXTAREA" || tag === "SELECT") return true;
  if (tag === "INPUT") {
    const type = (target as HTMLInputElement).type;
    return !["checkbox", "radio", "button", "submit", "reset"].includes(type);
  }
  return false;
}

const navRowSelector = "main tbody tr, main [data-nav-row]";

function navRows(): HTMLElement[] {
  return [...document.querySelectorAll<HTMLElement>(navRowSelector)].filter(
    (el) => el.offsetParent !== null && !el.closest("[data-nav-skip]"),
  );
}

function setActiveRow(rows: HTMLElement[], index: number) {
  rows.forEach((r) => r.removeAttribute("data-nav-active"));
  const row = rows[index];
  if (!row) return;
  row.setAttribute("data-nav-active", "");
  row.scrollIntoView({ block: "nearest" });
}

/** openRow follows the first link of a row, or clicks the row. */
function openRow(row: HTMLElement) {
  const target = row.querySelector<HTMLElement>("[data-nav-default]") ?? row.querySelector<HTMLElement>("a[href]");
  (target ?? row).click();
}

/**
 * useGlobalShortcuts wires the app shortcuts: Cmd+K for the palette, ? for the shortcut
 * sheet, "g" chords for pages, and j, k, Enter and Escape in the list of the page.
 */
export function useGlobalShortcuts({
  togglePalette,
  openShortcuts,
}: {
  togglePalette: () => void;
  openShortcuts: () => void;
}) {
  const navigate = useNavigate();
  const me = useCurrentUser();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const chordAt = useRef(0);
  const active = useRef(-1);

  useEffect(() => {
    active.current = -1;
  }, [pathname]);

  useEffect(() => {
    const chords = new Map(
      [...navItems, ...settingsItems].filter((i) => i.chord && can(me.role, i.min)).map((i) => [i.chord!, i.to]),
    );
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && !e.altKey && e.key.toLowerCase() === "k") {
        e.preventDefault();
        togglePalette();
        return;
      }
      if (e.defaultPrevented || e.metaKey || e.ctrlKey || e.altKey || isTyping(e.target)) return;
      if (document.querySelector("dialog[open]")) return;

      if (Date.now() - chordAt.current < 1200) {
        chordAt.current = 0;
        const to = chords.get(e.key.toLowerCase());
        if (to) {
          e.preventDefault();
          void navigate({ to });
        }
        return;
      }
      if (e.key === "g") {
        chordAt.current = Date.now();
        return;
      }
      if (e.key === "?") {
        e.preventDefault();
        openShortcuts();
        return;
      }
      if (e.key === "j" || e.key === "k") {
        const rows = navRows();
        if (rows.length === 0) return;
        e.preventDefault();
        const current = rows.findIndex((r) => r.hasAttribute("data-nav-active"));
        const from = current >= 0 ? current : active.current;
        const next = from < 0 ? 0 : Math.min(Math.max(from + (e.key === "j" ? 1 : -1), 0), rows.length - 1);
        active.current = next;
        setActiveRow(rows, next);
        return;
      }
      if (e.key === "Enter" || e.key === "o") {
        const row = document.querySelector<HTMLElement>("[data-nav-active]");
        if (!row || (e.target instanceof HTMLElement && e.target.closest("a, button"))) return;
        e.preventDefault();
        openRow(row);
        return;
      }
      if (e.key === "Escape") {
        navRows().forEach((r) => r.removeAttribute("data-nav-active"));
        active.current = -1;
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [me.role, navigate, togglePalette, openShortcuts]);
}

const isMac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform);
const mod = isMac ? "⌘" : "Ctrl";

function Keys({ keys }: { keys: string[] }) {
  return (
    <span className="flex shrink-0 items-center gap-1">
      {keys.map((k, i) => (
        <kbd
          key={i}
          className="inline-flex h-6 min-w-6 items-center justify-center rounded-inner border bg-muted px-1.5 font-sans text-xs font-medium text-foreground shadow-[0_1px_0_var(--border)]"
        >
          {k}
        </kbd>
      ))}
    </span>
  );
}

/** ShortcutSheet lists the keyboard shortcuts. */
export function ShortcutSheet({ open, onClose }: { open: boolean; onClose: () => void }) {
  const me = useCurrentUser();
  const pages = [...navItems, ...settingsItems].filter((i) => i.chord && can(me.role, i.min));
  const sections: { title: string; rows: { label: string; keys: string[] }[] }[] = [
    {
      title: "Everywhere",
      rows: [
        { label: "Open the command palette", keys: [mod, "K"] },
        { label: "Show this sheet", keys: ["?"] },
      ],
    },
    {
      title: "Lists",
      rows: [
        { label: "Next row", keys: ["J"] },
        { label: "Previous row", keys: ["K"] },
        { label: "Open the selected row", keys: ["↵"] },
        { label: "Clear the selection", keys: ["esc"] },
      ],
    },
    {
      title: "Editor",
      rows: [
        { label: "Save", keys: [mod, "S"] },
        { label: "Run the script file", keys: [mod, "↵"] },
      ],
    },
    { title: "Go to", rows: pages.map((p) => ({ label: p.label, keys: ["G", p.chord!.toUpperCase()] })) },
  ];
  return (
    <Dialog open={open} onClose={onClose} title="Keyboard shortcuts" size="lg">
      <div className="grid gap-x-8 gap-y-5 sm:grid-cols-2">
        {sections.map((s) => (
          <section key={s.title} className="flex flex-col gap-1.5">
            <h3 className="text-xs font-medium text-muted-foreground">{s.title}</h3>
            <ul className="flex flex-col">
              {s.rows.map((r) => (
                <li
                  key={r.label}
                  className="flex h-8 items-center justify-between gap-3 border-b last:border-0 text-sm"
                >
                  <span>{r.label}</span>
                  <Keys keys={r.keys} />
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>
    </Dialog>
  );
}
