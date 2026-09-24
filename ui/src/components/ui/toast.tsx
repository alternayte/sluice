import { useRouter } from "@tanstack/react-router";
import { AlertTriangle, CheckCircle2, Info, X } from "lucide-react";
import { useEffect, useRef, useSyncExternalStore } from "react";
import { cn } from "@/lib/utils";

type Tone = "success" | "error" | "info";

export type ToastInput = {
  title: string;
  description?: string;
  tone?: Tone;
  /** link adds a link to an app path, for example the new execution. */
  link?: { label: string; href: string };
};

type ToastItem = ToastInput & { id: number; leaving: boolean };

let items: ToastItem[] = [];
let nextId = 1;
const listeners = new Set<() => void>();

function emit() {
  items = [...items];
  listeners.forEach((l) => l());
}

/** toast shows a message in the bottom right corner, above the assistant button. It closes itself after five seconds. */
export function toast(input: ToastInput): number {
  const id = nextId++;
  items.push({ ...input, id, leaving: false });
  if (items.length > 4) items.splice(0, items.length - 4);
  emit();
  return id;
}

function dismiss(id: number) {
  const t = items.find((x) => x.id === id);
  if (!t || t.leaving) return;
  t.leaving = true;
  emit();
  window.setTimeout(() => {
    items = items.filter((x) => x.id !== id);
    emit();
  }, 180);
}

const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => listeners.delete(l);
};

const icons = { success: CheckCircle2, error: AlertTriangle, info: Info } as const;
const iconTone = { success: "text-state-success", error: "text-state-failed", info: "text-accent" } as const;

function ToastCard({ t }: { t: ToastItem }) {
  const router = useRouter();
  const timer = useRef<number | undefined>(undefined);
  const start = () => {
    timer.current = window.setTimeout(() => dismiss(t.id), 5000);
  };
  const stop = () => window.clearTimeout(timer.current);

  useEffect(() => {
    start();
    return stop;
    // The timer starts once per toast.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const tone = t.tone ?? "success";
  const Icon = icons[tone];
  return (
    <li
      role={tone === "error" ? "alert" : "status"}
      onMouseEnter={stop}
      onMouseLeave={start}
      className={cn(
        "pointer-events-auto flex w-80 max-w-[calc(100vw-2rem)] items-start gap-2.5 rounded-panel border bg-panel/85 p-3 shadow-float backdrop-blur-xl backdrop-saturate-150",
        t.leaving ? "animate-toast-out" : "animate-toast-in",
      )}
    >
      <Icon className={cn("mt-0.5 h-4 w-4 shrink-0", iconTone[tone])} aria-hidden />
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <p className="text-sm font-medium">{t.title}</p>
        {t.description && <p className="text-xs break-words text-muted-foreground">{t.description}</p>}
        {t.link && (
          <a
            href={t.link.href}
            onClick={(e) => {
              e.preventDefault();
              router.history.push(t.link!.href);
              dismiss(t.id);
            }}
            className="mt-0.5 w-fit text-xs font-medium text-accent-text hover:underline"
          >
            {t.link.label}
          </a>
        )}
      </div>
      <button
        type="button"
        aria-label="Dismiss"
        onClick={() => dismiss(t.id)}
        className="pressable -m-1 flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-muted-foreground hover:bg-muted hover:text-foreground"
      >
        <X className="h-3.5 w-3.5" aria-hidden />
      </button>
    </li>
  );
}

/**
 * Toaster renders the toasts. The app shell mounts it once. The list is a manual popover, so it
 * sits in the top layer: a toast shows above an open modal dialog and its backdrop.
 */
export function Toaster() {
  const ref = useRef<HTMLOListElement>(null);
  const list = useSyncExternalStore(
    subscribe,
    () => items,
    () => items,
  );

  // Showing the popover again moves it to the top of the top layer, above a dialog opened later.
  useEffect(() => {
    const el = ref.current;
    if (!el || typeof el.showPopover !== "function") return;
    if (el.matches(":popover-open")) el.hidePopover();
    if (list.length > 0) el.showPopover();
  }, [list.length]);

  return (
    <ol
      ref={ref}
      popover="manual"
      aria-label="Notifications"
      className="pointer-events-none fixed inset-auto right-4 bottom-20 m-0 flex flex-col items-end gap-2 overflow-visible border-0 bg-transparent p-0 [&:not(:popover-open)]:hidden"
    >
      {list.map((t) => (
        <ToastCard key={t.id} t={t} />
      ))}
    </ol>
  );
}
