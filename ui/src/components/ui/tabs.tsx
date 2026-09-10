import { cn } from "@/lib/utils";

/** Tabs renders a tab bar. The parent keeps the selected value (for example in the URL). */
export function Tabs<T extends string>({
  label,
  tabs,
  value,
  onChange,
}: {
  label: string;
  tabs: { value: T; label: string }[];
  value: T;
  onChange: (value: T) => void;
}) {
  return (
    <div role="tablist" aria-label={label} className="flex gap-1 overflow-x-auto border-b">
      {tabs.map((tab) => (
        <button
          key={tab.value}
          type="button"
          role="tab"
          aria-selected={tab.value === value}
          onClick={() => onChange(tab.value)}
          className={cn(
            "-mb-px h-9 shrink-0 border-b-2 border-transparent px-3 text-sm text-muted-foreground hover:text-foreground",
            tab.value === value && "border-accent font-medium text-foreground",
          )}
        >
          {tab.label}
        </button>
      ))}
    </div>
  );
}
