import type { MetricsStrip } from "@/lib/api/profile";

/**
 * The verified-metrics strip (PRD §8): headline numbers the audit actually
 * measured. Each tile renders its value only when present; a metric the audit
 * could not measure shows an explicit "—" with "not measured", NEVER a 0. A zero
 * would assert a real reading nobody took.
 */

type Tile = {
  key: keyof MetricsStrip;
  label: string;
  format: (v: number) => string;
};

const intFmt = new Intl.NumberFormat("en-US");
const asPercent = (v: number) => `${(v * 100).toFixed(1)}%`;

const TILES: Tile[] = [
  { key: "followers", label: "Followers", format: (v) => intFmt.format(Math.round(v)) },
  { key: "engagement_rate", label: "Engagement rate", format: asPercent },
  { key: "reach_ratio", label: "Reach ratio", format: asPercent },
  { key: "save_rate", label: "Save rate", format: asPercent },
  { key: "share_rate", label: "Share rate", format: asPercent },
  {
    key: "posting_cadence_days",
    label: "Posting cadence",
    format: (v) => `every ${v.toFixed(1)} days`,
  },
];

export function VerifiedMetricsStrip({ strip }: { strip: MetricsStrip }) {
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
      {TILES.map((tile) => {
        const value = strip[tile.key];
        const measured = value !== undefined && value !== null;
        return (
          <div
            key={tile.key}
            className="flex flex-col rounded-xl border border-[var(--line)] bg-[var(--surface)] p-3"
          >
            <span className="text-xs text-[var(--muted)]">{tile.label}</span>
            {measured ? (
              <span className="mt-1 text-lg font-semibold text-[var(--ink)]">
                {tile.format(value)}
              </span>
            ) : (
              <span
                className="mt-1 text-lg font-semibold text-[var(--muted)]"
                title="Not measured — we only show numbers we actually pulled."
              >
                — <span className="text-xs font-normal">not measured</span>
              </span>
            )}
          </div>
        );
      })}
    </div>
  );
}
