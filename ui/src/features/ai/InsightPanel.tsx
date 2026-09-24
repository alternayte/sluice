import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Gauge, Sparkles, Wand2 } from "lucide-react";
import {
  listExecutionInsightsOptions,
  listExecutionInsightsQueryKey,
  requestTriageMutation,
} from "@/api/@tanstack/react-query.gen";
import type { ExecutionDetail, Insight } from "@/api/types.gen";
import { EmptyState, Skeleton } from "@/components/data-state";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { FormError } from "@/components/ui/field";
import { useCurrentUser } from "@/lib/auth";
import { errorMessage } from "@/lib/errors";
import { can } from "@/lib/roles";
import { cn, formatTime } from "@/lib/utils";
import { openAssistant } from "./assistant-request";
import { useAiEnabled } from "./status";

const active = (i?: Insight) => i?.status === "pending" || i?.status === "running";

/**
 * fixWithAssistant starts a conversation about a failed execution. The server attaches the
 * execution, its latest triage and the logs of the failed task runs to the first message.
 */
function fixWithAssistant(executionId: string) {
  openAssistant({
    text: "This execution failed. Use the triage and the logs of the failed task to explain the cause, then propose a fix to the flow or the script.",
    attachments: [{ kind: "execution", execution_id: executionId }],
    send: true,
  });
}

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

  const done = latest?.status === "done";

  return (
    <section
      aria-labelledby="insight-heading"
      className="flex animate-enter flex-col overflow-hidden rounded-panel border bg-panel shadow-panel"
    >
      <div className="flex flex-wrap items-center gap-2.5 border-b px-4 py-2.5">
        <span className="flex h-6 w-6 items-center justify-center rounded-full bg-accent-soft text-accent">
          <Sparkles className={cn("h-3.5 w-3.5", active(latest) && "animate-breathe")} aria-hidden />
        </span>
        <h2 id="insight-heading" className="text-sm font-semibold">
          Failure triage
        </h2>
        {done && latest?.confidence && <ConfidenceBadge confidence={latest.confidence} />}
        <div className="ml-auto flex items-center gap-2">
          {can(me.role, "operator") && (
            <Button
              variant="secondary"
              size="sm"
              disabled={request.isPending || active(latest)}
              onClick={() => request.mutate(path)}
            >
              {latest ? "Triage again" : "Triage"}
            </Button>
          )}
          <Button size="sm" onClick={() => fixWithAssistant(execution.id)}>
            <Wand2 className="h-3.5 w-3.5" aria-hidden />
            Fix with assistant
          </Button>
        </div>
      </div>
      <div className="flex flex-col gap-3 p-4">
        {request.isError && <FormError>{errorMessage(request.error)}</FormError>}
        {insights.isError && <FormError>{errorMessage(insights.error)}</FormError>}
        {!latest && insights.isPending && <Skeleton shape="lines" rows={3} />}
        {!latest && !insights.isPending && (
          <EmptyState icon={Sparkles} text="No triage exists for this execution." className="border-0 py-4" />
        )}
        {latest && <InsightView insight={latest} />}
      </div>
    </section>
  );
}

const confidenceTone = { high: "success", medium: "accent", low: "neutral" } as const;

function ConfidenceBadge({ confidence }: { confidence: string }) {
  return (
    <Badge tone={confidenceTone[confidence as keyof typeof confidenceTone] ?? "neutral"} icon={Gauge}>
      {confidence.charAt(0).toUpperCase() + confidence.slice(1)} confidence
    </Badge>
  );
}

function InsightView({ insight: i }: { insight: Insight }) {
  if (active(i)) {
    return (
      <div className="flex flex-col gap-1">
        <p role="status" className="text-sm text-muted-foreground">
          The triage is running.
        </p>
        <div aria-hidden>
          <Skeleton shape="lines" rows={3} />
        </div>
      </div>
    );
  }
  if (i.status === "failed") {
    return <FormError>The triage failed: {i.error}</FormError>;
  }
  return (
    <div className="flex flex-col gap-4">
      <p className="text-base leading-snug break-words">{i.summary}</p>
      <dl className="grid grid-cols-1 gap-x-6 gap-y-3 text-sm sm:grid-cols-[9rem_minmax(0,1fr)]">
        <dt className="text-muted-foreground">Probable cause</dt>
        <dd className="break-words">{i.probable_cause}</dd>
        <dt className="text-muted-foreground">Suggested fix</dt>
        <dd className="break-words whitespace-pre-wrap">{i.suggested_fix}</dd>
        {!i.confidence && (
          <>
            <dt className="text-muted-foreground">Confidence</dt>
            <dd>—</dd>
          </>
        )}
        <dt className="text-muted-foreground">Evidence</dt>
        <dd className="min-w-0">
          {i.evidence.length === 0 ? (
            "—"
          ) : (
            <ul
              aria-label="Evidence"
              className="flex flex-col divide-y divide-border/70 overflow-hidden rounded-control border bg-muted/50"
            >
              {i.evidence.map((ev, n) => (
                <li key={n} className="flex min-w-0 flex-col gap-0.5 px-3 py-1.5 font-mono text-xs">
                  <span className="text-muted-foreground">
                    {ev.task}:{ev.line}
                  </span>
                  <code className="break-all">{ev.text}</code>
                </li>
              ))}
            </ul>
          )}
        </dd>
      </dl>
      <p className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
        <span className="font-mono">{i.model}</span>
        <span aria-hidden>·</span>
        <span className="tabular-nums">{formatTime(i.created_at)}</span>
      </p>
    </div>
  );
}
