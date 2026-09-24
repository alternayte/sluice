import { useEffect, useLayoutEffect, useRef } from "react";
import { cn } from "@/lib/utils";

/**
 * Tabs renders a tab bar with an underline that slides to the selected tab. The parent keeps
 * the selected value (for example in the URL).
 */
export function Tabs<T extends string>({
  label,
  tabs,
  value,
  onChange,
  className,
}: {
  label: string;
  tabs: { value: T; label: string }[];
  value: T;
  onChange: (value: T) => void;
  className?: string;
}) {
  const listRef = useRef<HTMLDivElement>(null);
  const barRef = useRef<HTMLSpanElement>(null);
  const placed = useRef(false);

  const place = () => {
    const bar = barRef.current;
    const tab = listRef.current?.querySelector<HTMLElement>('[aria-selected="true"]');
    if (!bar || !tab) return;
    // The first placement does not slide in from the left edge.
    bar.style.transitionDuration = placed.current ? "" : "0s";
    bar.style.transform = `translateX(${tab.offsetLeft}px)`;
    bar.style.width = `${tab.offsetWidth}px`;
    placed.current = true;
  };

  useLayoutEffect(place);

  // A font swap or a resize changes the tab widths without a render.
  useEffect(() => {
    const list = listRef.current;
    if (!list || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(() => place());
    list.querySelectorAll('[role="tab"]').forEach((t) => ro.observe(t));
    return () => ro.disconnect();
  }, [tabs.length]);

  return (
    <div
      ref={listRef}
      role="tablist"
      aria-label={label}
      className={cn("relative flex gap-1 overflow-x-auto border-b", className)}
    >
      {tabs.map((tab) => (
        <button
          key={tab.value}
          type="button"
          role="tab"
          aria-selected={tab.value === value}
          onClick={() => onChange(tab.value)}
          className={cn(
            "h-9 shrink-0 px-3 text-sm text-muted-foreground transition-colors duration-150 hover:text-foreground",
            tab.value === value && "font-medium text-foreground",
          )}
        >
          {tab.label}
        </button>
      ))}
      <span
        ref={barRef}
        aria-hidden
        className="pointer-events-none absolute bottom-0 left-0 h-0.5 w-0 rounded-full bg-accent transition-[transform,width] duration-[400ms] ease-snappy"
      />
    </div>
  );
}
