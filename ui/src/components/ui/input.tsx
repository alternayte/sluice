import type { InputHTMLAttributes, SelectHTMLAttributes, TextareaHTMLAttributes } from "react";
import { cn } from "@/lib/utils";

/** controlClass is the look of every text control: a recessed field with a hairline and a focus glow. */
export const controlClass =
  "w-full min-w-0 rounded-control border border-input/80 bg-panel px-2.5 text-sm text-foreground shadow-[inset_0_1px_1px_rgb(0_0_0/0.04)] transition-[border-color,box-shadow] duration-150 placeholder:text-muted-foreground/80 focus-visible:border-accent focus-visible:shadow-[0_0_0_3px_var(--ring)] focus-visible:outline-none disabled:opacity-50 aria-[invalid=true]:border-destructive dark:bg-background/60";

export function Input({ className, ...props }: InputHTMLAttributes<HTMLInputElement>) {
  return <input className={cn(controlClass, "h-8", className)} {...props} />;
}

export function Textarea({ className, ...props }: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea className={cn(controlClass, "py-1.5", className)} {...props} />;
}

/**
 * Select is a native select element in the look of a macOS pop-up button: a raised face with
 * a chevron. It serves long option lists; short filters use SegmentedControl.
 */
export function Select({ className, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select
      className={cn(controlClass, "select-chevron h-8 cursor-default appearance-none pr-7 shadow-panel", className)}
      {...props}
    />
  );
}
