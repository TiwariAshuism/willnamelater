import type { Readiness } from "@/lib/api/profile";

/**
 * The media-kit readiness meter (PRD §8): a completeness bar plus a checklist of
 * what a brand-ready profile needs. It is a METER, never a score — the fraction is
 * simply how many checklist items are present. A missing item is shown as an open
 * checkbox with guidance, never dressed up as complete.
 */

const FIELD_LABEL: Record<string, string> = {
  profile: "Profile captured",
  recent_posts: "Enough recent posts",
  audience_demographics: "Audience demographics available",
  sponsored_history: "Sponsored-post history",
  verified_insights: "Verified reach / save insights",
  comment_samples: "Comment samples captured",
};

export function ReadinessMeter({ readiness }: { readiness: Readiness }) {
  const pct = Math.round(Math.min(1, Math.max(0, readiness.fraction)) * 100);
  const present = readiness.fields.filter((f) => f.present).length;

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center justify-between text-sm">
        <span className="text-[var(--ink)]">Media-kit readiness</span>
        <span className="text-[var(--muted)]">
          {present}/{readiness.fields.length} · {pct}%
        </span>
      </div>
      <div className="h-2 w-full overflow-hidden rounded-full bg-[var(--surface-2)]">
        <div
          className="h-full rounded-full bg-[var(--color-brand)]"
          style={{ width: `${pct}%` }}
        />
      </div>
      <ul className="mt-1 flex flex-col gap-1.5">
        {readiness.fields.map((f) => (
          <li
            key={f.field}
            className="flex items-center gap-2 text-sm text-[var(--ink-secondary)]"
          >
            <span
              aria-hidden
              className={
                f.present
                  ? "text-[var(--color-brand)]"
                  : "text-[var(--muted)]"
              }
            >
              {f.present ? "✓" : "○"}
            </span>
            <span className={f.present ? "text-[var(--ink)]" : undefined}>
              {FIELD_LABEL[f.field] ?? f.field}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}
