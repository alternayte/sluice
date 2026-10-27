import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

/** Switch is an on/off button with a visible label. */
export function Switch({
  checked,
  onChange,
  disabled,
  children,
  label,
  className,
}: {
  checked: boolean;
  onChange: (next: boolean) => void;
  disabled?: boolean;
  /** Visible text next to the track. */
  children?: ReactNode;
  /** Accessible name when the visible text does not name the target. */
  label?: string;
  className?: string;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={cn(
        "pressable group inline-flex h-8 items-center gap-2 rounded-control px-2 text-sm font-medium text-foreground hover:bg-muted disabled:opacity-50",
        className,
      )}
    >
      <span
        aria-hidden
        className={cn(
          "relative h-[1.125rem] w-8 shrink-0 rounded-full shadow-[inset_0_0_0_0.5px_rgb(0_0_0/0.12)] transition-colors duration-200",
          checked ? "bg-accent-fill" : "bg-input",
        )}
      >
        <span
          className={cn(
            "absolute top-0.5 left-0.5 h-3.5 w-3.5 rounded-full bg-white shadow-panel transition-transform duration-300 ease-snappy",
            checked && "translate-x-3.5",
          )}
        />
      </span>
      {children}
    </button>
  );
}
