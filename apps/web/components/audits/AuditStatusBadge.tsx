import { clsx } from "clsx";

// audit_status enum: queued | running | partial | succeeded | failed | canceled
const styles: Record<string, string> = {
  queued: "bg-[var(--surface-2)] text-[var(--ink-secondary)]",
  running: "bg-[var(--surface-2)] text-[var(--color-brand)]",
  partial: "bg-[var(--surface-2)] text-[var(--color-warning)]",
  succeeded: "bg-[var(--surface-2)] text-[var(--color-good)]",
  failed: "bg-[var(--surface-2)] text-[var(--color-critical)]",
  canceled: "bg-[var(--surface-2)] text-[var(--muted)]",
};

export function AuditStatusBadge({ status }: { status: string }) {
  return (
    <span
      className={clsx(
        "inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium capitalize",
        styles[status] ?? styles.queued,
      )}
    >
      {status}
    </span>
  );
}
