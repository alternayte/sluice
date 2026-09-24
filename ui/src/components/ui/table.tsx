import type { HTMLAttributes, TdHTMLAttributes, ThHTMLAttributes } from "react";
import { cn } from "@/lib/utils";

/** Table puts the table in a panel that scrolls horizontally on narrow screens. */
export function Table({ className, ...props }: HTMLAttributes<HTMLTableElement>) {
  return (
    <div className="relative overflow-x-auto rounded-panel border bg-panel shadow-panel">
      <table className={cn("w-full border-collapse text-sm", className)} {...props} />
    </div>
  );
}

export function THead(props: HTMLAttributes<HTMLTableSectionElement>) {
  return <thead {...props} />;
}

export function TBody(props: HTMLAttributes<HTMLTableSectionElement>) {
  return <tbody {...props} />;
}

/**
 * Tr is a 36 px row. Rows in a body take part in keyboard navigation (j, k and Enter), and
 * the selected row shows the selection tint.
 */
export function Tr({ className, ...props }: HTMLAttributes<HTMLTableRowElement>) {
  return (
    <tr
      className={cn(
        "h-9 border-b transition-colors duration-100 last:border-0 [tbody_&]:hover:bg-muted/60 data-[nav-active]:bg-accent-soft/70",
        className,
      )}
      {...props}
    />
  );
}

export function Th({ className, ...props }: ThHTMLAttributes<HTMLTableCellElement>) {
  return (
    <th
      scope="col"
      className={cn(
        "h-8 px-3 text-left text-xs font-medium whitespace-nowrap text-muted-foreground first:pl-4 last:pr-4",
        className,
      )}
      {...props}
    />
  );
}

export function Td({ className, ...props }: TdHTMLAttributes<HTMLTableCellElement>) {
  return <td className={cn("px-3 py-1 whitespace-nowrap first:pl-4 last:pr-4", className)} {...props} />;
}
