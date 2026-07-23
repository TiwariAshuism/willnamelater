import type { HTMLAttributes } from "react";
import { clsx } from "clsx";

export function Card({
  className,
  ...props
}: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={clsx(
        "rounded-xl border border-[var(--line)] bg-[var(--surface)] p-6",
        className,
      )}
      {...props}
    />
  );
}

export function CardTitle({
  className,
  ...props
}: HTMLAttributes<HTMLHeadingElement>) {
  return (
    <h2
      className={clsx("text-sm font-semibold text-[var(--ink)]", className)}
      {...props}
    />
  );
}
