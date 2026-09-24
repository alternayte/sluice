import { Waves } from "lucide-react";
import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

/** BrandMark is the Sluice mark of the sidebar at the size of a sign-in sheet. */
export function BrandMark() {
  return (
    <span
      aria-hidden
      className="flex h-11 w-11 items-center justify-center rounded-control bg-gradient-to-b from-accent to-accent-fill text-accent-foreground shadow-panel"
    >
      <Waves className="h-6 w-6" />
    </span>
  );
}

/**
 * AuthSheet is a centred sheet over the window backdrop, in the manner of a macOS sign-in
 * window. The sign-in page and the full-screen states of the root shell use it.
 */
export function AuthSheet({
  title,
  description,
  children,
  className,
}: {
  title?: string;
  description?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className="window-backdrop flex min-h-screen items-center justify-center px-4 py-10">
      <div
        className={cn(
          "flex w-full max-w-sm animate-enter flex-col gap-5 rounded-panel border bg-panel/90 p-7 shadow-float backdrop-blur-2xl backdrop-saturate-150",
          className,
        )}
      >
        {title && (
          <div className="flex flex-col items-center gap-3 text-center">
            <BrandMark />
            <div className="flex flex-col gap-1">
              <h1 className="text-lg font-semibold tracking-tight">{title}</h1>
              {description && <p className="text-sm text-muted-foreground">{description}</p>}
            </div>
          </div>
        )}
        {children}
      </div>
    </div>
  );
}
