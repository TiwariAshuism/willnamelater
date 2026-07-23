import type { FactorPresentation } from "@influaudit/contracts";

/**
 * The per-metric result cards (PRD §8.4): one card per hireability factor with a
 * tier-relative status pill, a provenance tag, and a two-line "to improve"
 * diagnosis. Everything here is DERIVED by the backend presentation layer — a
 * factor that could not be measured shows "not measured" / "not available at your
 * size" with no pill and no line, never a fabricated 0 or band.
 */

const BAND_LABEL: Record<string, string> = {
  strong: "Strong",
  solid: "Solid",
  needs_work: "Needs work",
  not_assessed: "Not assessed",
};

const BAND_CLASS: Record<string, string> = {
  strong:
    "bg-[color-mix(in_oklab,green_16%,transparent)] text-green-700 dark:text-green-400",
  solid: "bg-[var(--surface-2)] text-[var(--ink)]",
  needs_work:
    "bg-[color-mix(in_oklab,orange_16%,transparent)] text-amber-700 dark:text-amber-400",
  not_assessed: "bg-[var(--surface-2)] text-[var(--muted)]",
};

const AVAILABILITY_LABEL: Record<string, string> = {
  verified: "Verified — pulled from Instagram",
  modeled: "Modeled on your pulled data",
  declared: "Self-reported",
  not_measured: "Not measured",
  not_available_at_size: "Not available at your follower size",
};

export function FactorCards({ factors }: { factors: FactorPresentation[] }) {
  if (!factors || factors.length === 0) return null;
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      {factors.map((f) => (
        <div
          key={f.key}
          className="flex flex-col rounded-xl border border-[var(--line)] bg-[var(--surface)] p-4"
        >
          <div className="flex items-start justify-between gap-2">
            <h3 className="text-sm font-semibold">{f.label}</h3>
            <span
              className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-semibold ${
                BAND_CLASS[f.band] ?? BAND_CLASS.not_assessed
              }`}
            >
              {BAND_LABEL[f.band] ?? f.band}
            </span>
          </div>
          <p className="mt-1 text-xs text-[var(--muted)]">
            {AVAILABILITY_LABEL[f.availability] ?? f.availability}
          </p>
          {f.improvement_line && (
            <p className="mt-2 text-sm text-[var(--ink-secondary)]">
              {f.improvement_line}
            </p>
          )}
          {f.authenticity_note && (
            <p className="mt-1 text-xs italic text-[var(--muted)]">
              {f.authenticity_note}
            </p>
          )}
        </div>
      ))}
    </div>
  );
}
