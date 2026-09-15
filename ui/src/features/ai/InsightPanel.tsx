import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Sparkles } from "lucide-react";
import {
  listExecutionInsightsOptions,
  listExecutionInsightsQueryKey,
  requestTriageMutation,
} from "@/api/@tanstack/react-query.gen";
import type { ExecutionDetail, Insight } from "@/api/types.gen";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { FormError } from "@/components/ui/field";
import { useCurrentUser } from "@/lib/auth";
import { errorMessage } from "@/lib/errors";
import { can } from "@/lib/roles";
import { formatTime } from "@/lib/utils";
import { useAiEnabled } from "./status";

const active = (i?: Insight) => i?.status === "pending" || i?.status === "running";

/** InsightPanel shows the failure triage of a failed or timed out execution (REQ-AI-007). */
export function InsightPanel({ execution }: { execution: ExecutionDetail }) {
  const me = useCurrentUser();
  const qc = useQueryClient();
  const enabled = useAiEnabled() && (execution.state === "FAILED" || execution.state === "TIMED_OUT");
  const path = { path: { executionId: execution.id } };
  const insights = useQuery({
    ...listExecutionInsightsOptions(path),
    enabled,
    refetchInterval: (q) => (active(q.state.data?.items[0]) ? 2000 : false),
  });
  const request = useMutation({
    ...requestTriageMutation(),
    onSuccess: (d) => qc.setQueryData(listExecutionInsightsQueryKey(path), d),
  });
  if (!enabled) return null;
  const latest = insights.data?.items[0];

  return (
    <section aria-labelledby="insight-heading" className="flex flex-col gap-3 rounded-[8px] border bg-panel p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 id="insight-heading" className="text-base font-semibold">
          Failure triage
        </h2>
        {can(me.role, "operator") && (
          <Button
            variant="secondary"
            disabled={request.isPending || active(latest)}
            onClick={() => request.mutate(path)}
          >
            <Sparkles className="h-4 w-4" aria-hidden />
            {latest ? "Triage again" : "Triage"}
          </Button>
        )}
      </div>
      {request.isError && <FormError>{errorMessage(request.error)}</FormError>}
      {insights.isError && <FormError>{errorMessage(insights.error)}</FormError>}
      {!latest && !insights.isPending && (
        <p className="text-sm text-muted-foreground">No triage exists for this execution.</p>
      )}
      {latest && <InsightView insight={latest} />}
    </section>
  );
}

const confidenceTone = { high: "success", medium: "accent", low: "neutral" } as const;

function InsightView({ insight: i }: { insight: Insight }) {
  if (active(i)) {
    return (
      <p role="status" className="text-sm text-muted-foreground">
        The triage is running.
      </p>
    );
  }
  if (i.status === "failed") {
    return <FormError>The triage failed: {i.error}</FormError>;
  }
  return (
    <dl className="grid grid-cols-1 gap-x-6 gap-y-2 text-sm sm:grid-cols-[10rem_minmax(0,1fr)]">
      <dt className="text-muted-foreground">Summary</dt>
      <dd className="break-words">{i.summary}</dd>
      <dt className="text-muted-foreground">Probable cause</dt>
      <dd className="break-words">{i.probable_cause}</dd>
      <dt className="text-muted-foreground">Suggested fix</dt>
      <dd className="break-words whitespace-pre-wrap">{i.suggested_fix}</dd>
      <dt className="text-muted-foreground">Confidence</dt>
      <dd>
        {i.confidence ? (
          <Badge tone={confidenceTone[i.confidence as keyof typeof confidenceTone] ?? "neutral"}>{i.confidence}</Badge>
        ) : (
          "—"
        )}
      </dd>
      <dt className="text-muted-foreground">Evidence</dt>
      <dd className="min-w-0">
        {i.evidence.length === 0 ? (
          "—"
        ) : (
          <ul aria-label="Evidence" className="flex flex-col gap-1">
            {i.evidence.map((ev, n) => (
              <li key={n} className="min-w-0">
                <span className="text-xs text-muted-foreground">
                  {ev.task}, line {ev.line}
                </span>
                <code className="block font-mono text-xs break-all">{ev.text}</code>
              </li>
            ))}
          </ul>
        )}
      </dd>
      <dt className="text-muted-foreground">Model</dt>
      <dd className="font-mono text-xs">
        {i.model} <span className="text-muted-foreground">· {formatTime(i.created_at)}</span>
      </dd>
    </dl>
  );
}
