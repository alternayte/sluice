import { lazy, Suspense } from "react";
import { Skeleton } from "@/components/data-state";
import { cn } from "@/lib/utils";
import type { CodeEditorProps } from "./code-editor-impl";

export type { Issue } from "./diagnostics";

const CodeEditorImpl = lazy(() => import("./code-editor-impl"));

/** CodeEditor loads CodeMirror on demand, so it is not part of the initial bundle. */
export function CodeEditor({ className, ...props }: CodeEditorProps & { className?: string }) {
  return (
    <div
      data-testid="file-editor"
      className={cn("overflow-hidden rounded-panel border bg-panel shadow-panel", className)}
    >
      <Suspense
        fallback={
          <div className="px-4 py-3">
            <Skeleton shape="lines" rows={8} />
          </div>
        }
      >
        <CodeEditorImpl {...props} />
      </Suspense>
    </div>
  );
}
