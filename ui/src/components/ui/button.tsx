import type { ButtonHTMLAttributes } from "react";
import { cn } from "@/lib/utils";

type Variant = "primary" | "secondary" | "ghost" | "destructive";
type Size = "sm" | "md" | "icon";

const variants: Record<Variant, string> = {
  primary:
    "bg-accent-fill text-accent-foreground shadow-panel hover:brightness-110 active:brightness-95 dark:hover:brightness-115",
  secondary: "border border-input/70 bg-panel text-foreground shadow-panel hover:bg-muted",
  ghost: "text-muted-foreground hover:bg-muted hover:text-foreground",
  destructive: "bg-destructive-fill text-destructive-foreground shadow-panel hover:brightness-110",
};

const sizes: Record<Size, string> = {
  sm: "h-7 px-2.5 text-xs",
  md: "h-8 px-3 text-sm",
  icon: "h-8 w-8",
};

export type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant; size?: Size };

/** buttonClass returns the classes of a button, for links that look like buttons. */
export function buttonClass(variant: Variant = "secondary", size: Size = "md") {
  return cn(
    "pressable inline-flex shrink-0 items-center justify-center gap-1.5 rounded-control font-medium whitespace-nowrap select-none disabled:pointer-events-none disabled:opacity-45 [&_svg]:shrink-0",
    variants[variant],
    sizes[size],
  );
}

export function Button({ variant = "primary", size = "md", className, type = "button", ...props }: ButtonProps) {
  return <button type={type} className={cn(buttonClass(variant, size), className)} {...props} />;
}
