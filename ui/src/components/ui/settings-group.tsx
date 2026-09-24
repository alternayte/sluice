import { cloneElement, isValidElement, type ReactElement, type ReactNode } from "react";
import { cn } from "@/lib/utils";

type ControlProps = { id?: string; "aria-invalid"?: boolean; "aria-describedby"?: string };

/** SettingsGroup is a grouped panel in the manner of macOS System Settings: rows between hairlines. */
export function SettingsGroup({
  title,
  footer,
  children,
}: {
  title?: string;
  footer?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="flex flex-col gap-2">
      {title && <h2 className="px-1 text-sm font-semibold">{title}</h2>}
      <div className="divide-y rounded-panel border bg-panel shadow-panel">{children}</div>
      {footer && <p className="px-1 text-xs text-muted-foreground">{footer}</p>}
    </section>
  );
}

/**
 * SettingsRow puts a label in the left column and a control or a value in the right column.
 * With an id it labels the control and wires its hint and error, as Field does.
 */
export function SettingsRow({
  id,
  label,
  hint,
  error,
  trailing,
  children,
}: {
  id?: string;
  label: string;
  hint?: string;
  error?: string;
  /** trailing sits right of the control, for example its save button. */
  trailing?: ReactNode;
  children: ReactNode;
}) {
  const describedBy = id ? [hint ? `${id}-hint` : "", error ? `${id}-error` : ""].filter(Boolean).join(" ") : "";
  const control =
    id && isValidElement(children)
      ? cloneElement(children as ReactElement<ControlProps>, {
          id,
          "aria-invalid": error ? true : undefined,
          "aria-describedby": describedBy || undefined,
        })
      : children;
  const labelClass = "text-sm sm:flex sm:h-8 sm:items-center";
  return (
    <div className="grid grid-cols-1 gap-x-6 gap-y-1.5 px-4 py-2.5 sm:grid-cols-[minmax(0,12rem)_minmax(0,1fr)]">
      {id ? (
        <label htmlFor={id} className={labelClass}>
          {label}
        </label>
      ) : (
        <span className={labelClass}>{label}</span>
      )}
      <div className="flex min-w-0 flex-col gap-1">
        <div className="flex min-h-8 min-w-0 items-center gap-2">
          {control}
          {trailing}
        </div>
        {hint && (
          <p id={id ? `${id}-hint` : undefined} className="text-xs text-muted-foreground">
            {hint}
          </p>
        )}
        {error && (
          <p id={id ? `${id}-error` : undefined} className="text-xs text-destructive">
            {error}
          </p>
        )}
      </div>
    </div>
  );
}

/** SettingsActions is the last row of a group, with its buttons on the right. */
export function SettingsActions({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn("flex flex-wrap items-center justify-end gap-2 px-4 py-2.5", className)}>{children}</div>;
}
