import type { Metadata } from "next";
import Link from "next/link";
import { TrackView } from "@/components/funnel/TrackView";

export const metadata: Metadata = {
  title: "CreatorTrust — get your free verified Creator Score",
  description:
    "Connect your Instagram and get a verified Creator Score grading how brand-ready you are — every number pulled directly from Instagram, un-editable, so a brand can trust it.",
};

/**
 * The public landing: the acquisition front door. One clear call to action — get
 * your free verified Creator Score — plus an at-a-glance example and the trust
 * framing a brand-facing credential needs. It is public (the proxy gates only the
 * dashboard), so both new visitors and signed-in creators can see it.
 */
export default function Landing() {
  return (
    <main className="mx-auto flex min-h-screen max-w-3xl flex-col gap-16 px-6 py-16 sm:py-24">
      <TrackView event="landing_view" />

      {/* Hero */}
      <section className="flex flex-col items-center gap-6 text-center">
        <span className="rounded-full border border-[var(--line)] px-3 py-1 text-xs font-medium uppercase tracking-wide text-[var(--muted)]">
          Verified by Instagram · free
        </span>
        <h1 className="text-4xl font-bold leading-tight sm:text-5xl">
          What&rsquo;s your Creator Score?
        </h1>
        <p className="max-w-xl text-lg text-[var(--ink-secondary)]">
          Connect your Instagram and get a verified score grading how brand-ready
          you are — plus the audience and engagement data behind it. Every number
          is pulled directly from Instagram, so you can send it to a brand and
          they&rsquo;ll trust it.
        </p>
        <Link
          href="/start"
          className="inline-flex items-center justify-center rounded-md bg-[var(--color-brand)] px-6 py-3 text-base font-semibold text-white transition-colors hover:bg-[var(--color-brand-strong)]"
        >
          Get my free Creator Score
        </Link>
        <p className="text-xs text-[var(--muted)]">
          Takes under 3 minutes · no card, no posting access ·{" "}
          <Link href="/login" className="underline">
            already have an account?
          </Link>
        </p>
      </section>

      {/* Example result preview */}
      <section className="flex flex-col gap-3">
        <p className="text-center text-xs font-medium uppercase tracking-wide text-[var(--muted)]">
          Example score
        </p>
        <div className="rounded-2xl border border-[var(--line)] bg-[var(--surface)] p-8">
          <div className="flex items-center justify-between">
            <span className="rounded-full bg-[color-mix(in_oklab,green_18%,transparent)] px-2.5 py-1 text-xs font-semibold text-green-700 dark:text-green-400">
              🟢 Verified
            </span>
            <span className="text-xs text-[var(--muted)]">@yourhandle</span>
          </div>
          <div className="mt-5 flex items-end gap-3">
            <div className="text-6xl font-bold leading-none">78</div>
            <div className="pb-1 text-sm text-[var(--muted)]">
              / 100 · brand-ready
            </div>
          </div>
          <dl className="mt-6 grid grid-cols-2 gap-x-6 gap-y-3 text-sm">
            {[
              ["Engagement authenticity", "Strong"],
              ["Audience quality", "Solid"],
              ["Consistency & reliability", "Strong"],
              ["Brand-fit clarity", "Solid"],
            ].map(([label, band]) => (
              <div
                key={label}
                className="flex items-center justify-between border-b border-[var(--line)] pb-2"
              >
                <dt className="text-[var(--ink-secondary)]">{label}</dt>
                <dd className="font-medium">{band}</dd>
              </div>
            ))}
          </dl>
          <p className="mt-4 text-xs italic text-[var(--muted)]">
            Illustrative example. Your real score is computed only from your own
            connected data.
          </p>
        </div>
      </section>

      {/* Trust / privacy */}
      <section className="flex flex-col gap-4 rounded-xl border border-[var(--line)] bg-[var(--surface-2)] p-6 text-sm text-[var(--ink-secondary)]">
        <h2 className="text-sm font-semibold text-[var(--ink)]">
          Why a brand can trust it
        </h2>
        <p>
          Your numbers are pulled directly from Instagram via Meta&rsquo;s
          official API, using read-only access you grant. You cannot edit them, and
          neither can we — that&rsquo;s the whole point. We request only what we
          need: your profile, your post insights, your audience makeup, and your
          comments. We never request permission to post or message on your behalf.
        </p>
        <p>
          This is a hireability score, not a growth tool. It rewards real, un-gamed
          engagement and a clear, defined audience — the things a brand actually
          pays for.
        </p>
      </section>

      <footer className="text-center text-xs text-[var(--muted)]">
        Data pulled directly from Instagram via Meta. Creators cannot edit these
        numbers.
      </footer>
    </main>
  );
}
