import type { InputHTMLAttributes } from "react";
import { clsx } from "clsx";

interface FieldProps extends InputHTMLAttributes<HTMLInputElement> {
  label: string;
}

/** A labelled text input. `name` is required so it participates in FormData. */
export function Field({ label, className, id, name, ...props }: FieldProps) {
  const fieldId = id ?? name;
  return (
    <div className="flex flex-col gap-1.5">
      <label
        htmlFor={fieldId}
        className="text-sm font-medium text-[var(--ink-secondary)]"
      >
        {label}
      </label>
      <input
        id={fieldId}
        name={name}
        className={clsx(
          "rounded-md border border-[var(--line)] bg-[var(--surface)] px-3 py-2 text-sm text-[var(--ink)] outline-none focus:border-[var(--color-brand)] focus-visible:outline-2 focus-visible:outline-[var(--color-brand)]",
          className,
        )}
        {...props}
      />
    </div>
  );
}
