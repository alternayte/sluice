import { useEffect, useLayoutEffect, useRef, type KeyboardEvent } from "react";
import { cn } from "@/lib/utils";

/**
 * SegmentedControl is a macOS segmented control: a radio group on a recessed track with a
 * raised thumb that springs to the selected segment. Arrow keys move the selection.
 */
export function SegmentedControl<T extends string>({
  label,
  options,
  value,
  onChange,
  size = "md",
  className,
}: {
  label: string;
  options: { value: T; label: string }[];
  value: T;
  onChange: (value: T) => void;
  size?: "sm" | "md";
  className?: string;
}) {
  const trackRef = useRef<HTMLDivElement>(null);
  const thumbRef = useRef<HTMLSpanElement>(null);
  const placed = useRef(false);

  const place = () => {
    const thumb = thumbRef.current;
    const seg = trackRef.current?.querySelector<HTMLElement>('[aria-checked="true"]');
    if (!thumb) return;
    if (!seg) {
      thumb.style.opacity = "0";
      return;
    }
    thumb.style.transitionDuration = placed.current ? "" : "0s";
    thumb.style.opacity = "1";
    thumb.style.transform = `translateX(${seg.offsetLeft}px)`;
    thumb.style.width = `${seg.offsetWidth}px`;
    placed.current = true;
  };

  useLayoutEffect(place);

  useEffect(() => {
    const track = trackRef.current;
    if (!track || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(() => place());
    track.querySelectorAll('[role="radio"]').forEach((s) => ro.observe(s));
    return () => ro.disconnect();
  }, [options.length]);

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    const i = options.findIndex((o) => o.value === value);
    const step =
      e.key === "ArrowRight" || e.key === "ArrowDown" ? 1 : e.key === "ArrowLeft" || e.key === "ArrowUp" ? -1 : 0;
    if (!step) return;
    e.preventDefault();
    const next = options[(i + step + options.length) % options.length];
    if (!next) return;
    onChange(next.value);
    trackRef.current?.querySelector<HTMLElement>(`[data-value="${CSS.escape(next.value)}"]`)?.focus();
  };

  return (
    <div
      ref={trackRef}
      role="radiogroup"
      aria-label={label}
      onKeyDown={onKeyDown}
      className={cn(
        "relative inline-flex w-fit max-w-full shrink-0 items-center overflow-x-auto rounded-control bg-muted p-0.5 shadow-[inset_0_0_0_0.5px_var(--border)]",
        className,
      )}
    >
      <span
        ref={thumbRef}
        aria-hidden
        className="absolute top-0.5 bottom-0.5 left-0 w-0 rounded-thumb bg-panel opacity-0 shadow-panel transition-[transform,width] duration-[420ms] ease-snappy dark:bg-input/80"
      />
      {options.map((o) => {
        const selected = o.value === value;
        return (
          <button
            key={o.value}
            type="button"
            role="radio"
            aria-checked={selected}
            data-value={o.value}
            tabIndex={selected || (!options.some((x) => x.value === value) && o === options[0]) ? 0 : -1}
            onClick={() => onChange(o.value)}
            className={cn(
              "relative z-10 shrink-0 rounded-thumb font-medium whitespace-nowrap transition-colors duration-150",
              size === "sm" ? "h-6 px-2.5 text-xs" : "h-7 px-3 text-sm",
              selected ? "text-foreground" : "text-muted-foreground hover:text-foreground",
            )}
          >
            {o.label}
          </button>
        );
      })}
    </div>
  );
}
