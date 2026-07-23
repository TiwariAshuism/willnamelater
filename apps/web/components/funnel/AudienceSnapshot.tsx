import type {
  AudienceSnapshot as AudienceSnapshotData,
  Distribution,
} from "@/lib/api/profile";

/**
 * The audience snapshot (PRD §8.2): the top age / gender / country segments the
 * platform reported for a connected account. When no dimension was pulled it shows
 * a plain empty state rather than an empty chart — Meta only exposes follower
 * demographics above 100 followers and the pull can lag ~48h. Fractions come
 * straight from the platform; nothing here is fabricated, and language is not
 * shown because Meta does not expose it.
 */

const REGION_NAMES =
  typeof Intl !== "undefined" && "DisplayNames" in Intl
    ? new Intl.DisplayNames(["en"], { type: "region" })
    : undefined;

function countryLabel(code: string): string {
  try {
    return REGION_NAMES?.of(code.toUpperCase()) ?? code;
  } catch {
    return code;
  }
}

function topBuckets(
  dist: Distribution | undefined,
  limit: number,
  label: (k: string) => string,
): { key: string; label: string; fraction: number }[] {
  if (!dist) return [];
  return Object.entries(dist)
    .sort((a, b) => b[1] - a[1])
    .slice(0, limit)
    .map(([key, fraction]) => ({ key, label: label(key), fraction }));
}

function DimensionBars({
  title,
  rows,
}: {
  title: string;
  rows: { key: string; label: string; fraction: number }[];
}) {
  if (rows.length === 0) return null;
  return (
    <div className="flex flex-col gap-2">
      <h4 className="text-xs font-semibold text-[var(--muted)]">{title}</h4>
      {rows.map((r) => (
        <div key={r.key} className="flex flex-col gap-0.5">
          <div className="flex items-center justify-between text-xs text-[var(--ink)]">
            <span>{r.label}</span>
            <span className="text-[var(--muted)]">
              {(r.fraction * 100).toFixed(0)}%
            </span>
          </div>
          <div className="h-1.5 w-full overflow-hidden rounded-full bg-[var(--surface-2)]">
            <div
              className="h-full rounded-full bg-[var(--color-brand)]"
              style={{ width: `${Math.min(100, Math.max(0, r.fraction * 100))}%` }}
            />
          </div>
        </div>
      ))}
    </div>
  );
}

export function AudienceSnapshot({
  audience,
}: {
  audience: AudienceSnapshotData;
}) {
  const age = topBuckets(audience.age, 4, (k) => k);
  const gender = topBuckets(audience.gender, 3, (k) => k.replace(/^\w/, (c) => c.toUpperCase()));
  const country = topBuckets(audience.country, 4, countryLabel);

  const hasAny = age.length > 0 || gender.length > 0 || country.length > 0;

  if (!hasAny) {
    return (
      <p className="text-sm text-[var(--ink-secondary)]">
        Audience insights need 100+ followers and can lag ~48h. Connect a Business
        or Creator account and re-run once they populate.
      </p>
    );
  }

  return (
    <div className="grid gap-5 sm:grid-cols-3">
      <DimensionBars title="Age" rows={age} />
      <DimensionBars title="Gender" rows={gender} />
      <DimensionBars title="Top countries" rows={country} />
    </div>
  );
}
