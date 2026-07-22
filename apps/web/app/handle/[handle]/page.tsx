import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { getPublicBadgeByHandle } from "@/lib/api/report";
import { ApiError } from "@/lib/api/http";

/**
 * The PUBLIC handle page (creatortrust.com/@handle, rewritten to /handle/handle).
 * It renders the frozen, public-safe badge — the headline score and its trust
 * context, never the private narrative — for a creator's most recent published
 * report. It carries no auth: anyone with the link (a brand, say) can open it,
 * and the backend records that external open on read (the primary success
 * metric), so this page fires no client event of its own.
 */

export const metadata: Metadata = {
  title: "Verified Creator Score — CreatorTrust",
};

function fmt(n: number): string {
  return (Math.round(n * 10) / 10).toString();
}

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
      CreatorTrust
    </span>
  );
}

export default async function HandleBadgePage({
  params,
}: {
  params: Promise<{ handle: string }>;
}) {
  const { handle } = await params;
  // The pretty URL is /@handle; a leading "@" may survive the rewrite.
  const clean = decodeURIComponent(handle).replace(/^@/, "");

  const badge = await getPublicBadgeByHandle(clean).catch((error) => {
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

        <h1 className="mt-4 text-lg font-semibold">
          {badge.handle ? `@${badge.handle.replace(/^@/, "")}` : `@${clean}`}
        </h1>

        <div className="mt-6 flex gap-10">
          <div>
            <div className="text-xs uppercase tracking-wide text-[var(--muted)]">
              Creator Score
            </div>
            <div className="text-5xl font-bold leading-none">
              {fmt(badge.overall)}
              <span className="text-base font-normal text-[var(--muted)]">
                {" "}
                / 100
              </span>
            </div>
          </div>
          <div>
            <div className="text-xs uppercase tracking-wide text-[var(--muted)]">
              Authenticity
            </div>
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

        <p className="mt-6 border-t border-[var(--line)] pt-4 text-xs text-[var(--muted)]">
          Data pulled directly from Instagram via Meta. Creators cannot edit these
          numbers.
          {badge.benchmark_label ? ` Benchmarks: ${badge.benchmark_label}.` : ""}
        </p>
      </div>

      <Link href="/start" className="text-xs text-[var(--muted)] hover:underline">
        Get your own verified Creator Score →
      </Link>
    </main>
  );
}
