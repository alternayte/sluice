import { Loader2 } from "lucide-react";
import { lazy, Suspense } from "react";
import { cn } from "@/lib/utils";
import type { CodeEditorProps } from "./code-editor-impl";

export type { Issue } from "./diagnostics";

const CodeEditorImpl = lazy(() => import("./code-editor-impl"));

/** CodeEditor loads CodeMirror on demand, so it is not part of the initial bundle. */
export function CodeEditor({ className, ...props }: CodeEditorProps & { className?: string }) {
  return (
    <div data-testid="file-editor" className={cn("overflow-hidden rounded-[8px] border bg-panel", className)}>
      <Suspense
        fallback={
          <div role="status" className="flex items-center gap-2 p-4 text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
            Loading editor
          </div>
        }
      >
        <CodeEditorImpl {...props} />
      </Suspense>
    </div>
  );
}
