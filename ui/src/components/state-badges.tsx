import { AlertTriangle, CheckCircle2, CircleDashed, CirclePause, CirclePlay, XCircle } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { executionStateTone, stateLabel } from "@/lib/flows";

/** ValidBadge shows "Valid" or "Invalid" with an icon. */
export function ValidBadge({ valid }: { valid: boolean }) {
  return valid ? (
    <Badge tone="success" icon={CheckCircle2}>
      Valid
    </Badge>
  ) : (
    <Badge tone="failed" icon={XCircle}>
      Invalid
    </Badge>
  );
}

/** DisabledBadge marks a disabled flow. */
export function DisabledBadge() {
  return (
    <Badge tone="neutral" icon={CirclePause}>
      Disabled
    </Badge>
  );
}

const toneIcons = {
  success: CheckCircle2,
  failed: XCircle,
  warning: AlertTriangle,
  accent: CirclePlay,
  neutral: CircleDashed,
} as const;

/** ExecutionStateBadge shows an execution state with an icon and text. */
export function ExecutionStateBadge({ state }: { state: string }) {
  const s = state.toLowerCase();
  const tone = executionStateTone(s);
  return (
    <Badge tone={tone} icon={toneIcons[tone]}>
      {stateLabel(s)}
    </Badge>
  );
}
