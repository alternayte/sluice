import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

type Tone = "neutral" | "success" | "failed" | "warning" | "accent";

const tones: Record<Tone, string> = {
  neutral: "text-muted-foreground",
  success: "text-state-success",
  failed: "text-state-failed",
  warning: "text-state-timed-out",
  accent: "text-accent",
};

/**
 * Badge pairs a state icon in the state color with a text label. The label uses the text
 * color, because the state colors do not reach the AA contrast for small text (REQ-UI-011).
 * A change of the label replays a short entrance, so a live state change is visible.
 */
export function Badge({
  tone = "neutral",
  icon: Icon,
  pulse = false,
  children,
}: {
  tone?: Tone;
  icon?: LucideIcon;
  /** pulse makes the icon breathe, for a state that is in progress. */
  pulse?: boolean;
  children: ReactNode;
}) {
  return (
    <span
      key={typeof children === "string" ? children : undefined}
      className={cn(
        "inline-flex animate-enter items-center gap-1 text-xs font-medium whitespace-nowrap",
        tone === "neutral" ? "text-muted-foreground" : "text-foreground",
      )}
    >
      {Icon && <Icon className={cn("h-3.5 w-3.5", tones[tone], pulse && "animate-breathe")} aria-hidden />}
      <span>{children}</span>
    </span>
  );
}
