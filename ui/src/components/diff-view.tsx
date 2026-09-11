import { diffLines, type DiffLineKind } from "@/lib/diff";
import { cn } from "@/lib/utils";

const lineClass: Record<DiffLineKind, string> = {
  add: "bg-state-success/15",
  remove: "bg-state-failed/15",
  hunk: "text-accent-text",
  meta: "text-muted-foreground",
  context: "",
};

/** DiffView renders a unified diff with tinted added and removed lines. */
export function DiffView({ text, label }: { text: string; label: string }) {
  const lines = diffLines(text);
  if (lines.length === 0) return <p className="py-2 text-sm text-muted-foreground">No changes.</p>;
  return (
    <div className="overflow-x-auto rounded-[8px] border bg-panel">
      <pre aria-label={label} className="min-w-fit py-2 font-mono text-xs leading-5">
        {lines.map((l, i) => (
          <div key={i} className={cn("px-3 whitespace-pre", lineClass[l.kind])}>
            {l.text || " "}
          </div>
        ))}
      </pre>
    </div>
  );
}
