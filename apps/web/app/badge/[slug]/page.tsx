import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { getPublicBadge } from "@/lib/api/report";
import { ApiError } from "@/lib/api/http";

/**
 * The PUBLIC badge page. It is deliberately outside the (dashboard) route group,
 * so it carries no auth: anyone with the link sees the frozen, public-safe
 * snapshot — the headline score and its context, never the private narrative.
 * The data comes from the unauthenticated GET /reports/{slug}.
 */

export const metadata: Metadata = {
  title: "InfluAudit — Verified Score",
};

function fmt(n: number): string {
  return (Math.round(n * 10) / 10).toString();
}

// The trust chip: a verified badge rests on live-API data (🟢), an estimated one
// includes uploaded or provider-sourced data (🟡). Anything else is shown
// neutrally rather than claiming a tier the score does not have.
function VerificationChip({ tier }: { tier?: string }) {
  if (tier === "verified") {
    return (
      <span className="rounded-full bg-[color-mix(in_oklab,green_18%,transparent)] px-2.5 py-1 text-xs font-semibold text-green-700 dark:text-green-400">
        🟢 Verified
      </span>
    );
  }
  if (tier === "estimated") {
    return (
      <span className="rounded-full bg-[color-mix(in_oklab,orange_18%,transparent)] px-2.5 py-1 text-xs font-semibold text-amber-700 dark:text-amber-400">
        🟡 Estimated
      </span>
    );
  }
  return (
    <span className="text-xs font-semibold uppercase tracking-wide text-[var(--muted)]">
      InfluAudit
    </span>
  );
}

export default async function BadgePage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = await params;

  const badge = await getPublicBadge(slug).catch((error) => {
    if (error instanceof ApiError && error.status === 404) notFound();
    throw error;
  });

  return (
    <main className="mx-auto flex min-h-screen max-w-xl flex-col items-center justify-center gap-6 px-6 py-16">
      <div className="w-full rounded-2xl border border-[var(--line)] bg-[var(--surface)] p-8 shadow-sm">
        <div className="flex items-center justify-between">
          <VerificationChip tier={badge.verification_tier} />
          {badge.generated_at && (
            <span className="text-xs text-[var(--muted)]">
              {badge.generated_at}
            </span>
          )}
        </div>

        {badge.handle && (
          <h1 className="mt-4 text-lg font-semibold">{badge.handle}</h1>
        )}

        <div className="mt-6 flex gap-10">
          <div>
            <div className="text-xs uppercase tracking-wide text-[var(--muted)]">
              Influence
            </div>
            <div className="text-5xl font-bold leading-none">
              {fmt(badge.overall)}
            </div>
          </div>
          <div>
            <div className="text-xs uppercase tracking-wide text-[var(--muted)]">
              Authenticity
            </div>
            {/* Null when the dimension rested on no measurement: the badge says so
                rather than printing a number nobody measured. */}
            <div className="text-5xl font-bold leading-none">
              {badge.authenticity != null ? fmt(badge.authenticity) : "—"}
            </div>
          </div>
        </div>

        {(badge.niche || badge.tier) && (
          <p className="mt-5 text-sm text-[var(--muted)]">
            {badge.niche}
            {badge.niche && badge.tier ? " · " : ""}
            {badge.tier ? `${badge.tier} tier` : ""}
          </p>
        )}

        {badge.benchmark_label && (
          <p className="mt-1 text-xs italic text-[var(--muted)]">
            Benchmarks: {badge.benchmark_label}. Fraud figures are estimates, not
            measured percentages.
          </p>
        )}

        {badge.pdf_url && (
          <a
            href={badge.pdf_url}
            target="_blank"
            rel="noreferrer"
            className="mt-6 inline-flex items-center justify-center rounded-md border border-[var(--line)] bg-[var(--surface-2)] px-4 py-2 text-sm font-medium transition-colors hover:bg-[var(--surface)]"
          >
            View full report (PDF)
          </a>
        )}
      </div>

      <p className="text-xs text-[var(--muted)]">
        Independently scored by InfluAudit.
      </p>
    </main>
  );
}
