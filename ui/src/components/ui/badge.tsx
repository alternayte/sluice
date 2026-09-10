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

/** Badge always pairs the state color with a text label and an optional icon. */
export function Badge({ tone = "neutral", icon: Icon, children }: { tone?: Tone; icon?: LucideIcon; children: ReactNode }) {
  return (
    <span className={cn("inline-flex items-center gap-1 text-xs font-medium whitespace-nowrap", tones[tone])}>
      {Icon && <Icon className="h-3.5 w-3.5" aria-hidden />}
      <span>{children}</span>
    </span>
  );
}
