import { X } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { cn } from "@/lib/utils";

/**
 * Dialog wraps the native dialog element and opens it with showModal. The content stays
 * mounted until the close transition ends, so the sheet fades out with its content.
 */
export function Dialog({
  open,
  onClose,
  title,
  children,
  size = "md",
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  children: ReactNode;
  size?: "md" | "lg";
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const [rendered, setRendered] = useState(open);
  if (open && !rendered) setRendered(true);

  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) {
      d.showModal();
      // Focus goes to the first field, like a macOS sheet. Without a field it stays on the sheet.
      const field = d.querySelector<HTMLElement>(
        "[autofocus], input:not([type=hidden]):not([disabled]), textarea:not([disabled]), select:not([disabled])",
      );
      field?.focus();
    }
    if (!open && d.open) d.close();
    if (open) return;
    // Unmount the content after the close transition. A timer covers a browser without transitions.
    const t = window.setTimeout(() => setRendered(false), 220);
    return () => window.clearTimeout(t);
  }, [open]);

  return (
    <dialog
      ref={ref}
      onClose={onClose}
      aria-label={title}
      className={cn(
        "sheet m-auto w-[calc(100%-2rem)] rounded-panel border bg-panel p-0 text-foreground shadow-float",
        size === "md" ? "max-w-md" : "max-w-2xl",
      )}
    >
      {rendered && (
        <div className="flex flex-col gap-4 p-5">
          <div className="flex items-center justify-between gap-2">
            <h2 className="text-base font-semibold">{title}</h2>
            <button
              type="button"
              aria-label="Close"
              onClick={onClose}
              className="pressable -mr-1.5 flex h-7 w-7 items-center justify-center rounded-full text-muted-foreground hover:bg-muted hover:text-foreground"
            >
              <X className="h-4 w-4" aria-hidden />
            </button>
          </div>
          {children}
        </div>
      )}
    </dialog>
  );
}
