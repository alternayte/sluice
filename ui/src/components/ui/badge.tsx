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
 */
export function Badge({ tone = "neutral", icon: Icon, children }: { tone?: Tone; icon?: LucideIcon; children: ReactNode }) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 text-xs font-medium whitespace-nowrap",
        tone === "neutral" ? "text-muted-foreground" : "text-foreground",
      )}
    >
      {Icon && <Icon className={cn("h-3.5 w-3.5", tones[tone])} aria-hidden />}
      <span>{children}</span>
    </span>
  );
}
