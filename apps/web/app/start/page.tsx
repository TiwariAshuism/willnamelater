import type { Metadata } from "next";
import Link from "next/link";
import { PreflightGate } from "@/components/funnel/PreflightGate";
import { WaitlistForm } from "@/components/funnel/WaitlistForm";

export const metadata: Metadata = {
  title: "Connect your Instagram — CreatorTrust",
};

/** The two hard prerequisites, each with a short numbered fix so a creator who
 * isn't set up reaches OAuth already qualified rather than hitting a dead-end. */
const FIXES: { title: string; steps: string[] }[] = [
  {
    title: "1 · A Business or Creator account",
    steps: [
      "Open Instagram → Settings and privacy → Account type and tools.",
      "Tap “Switch to professional account”.",
      "Choose Business or Creator — either works.",
    ],
  },
  {
    title: "2 · Linked to a Facebook Page",
    steps: [
      "In Settings → Business tools and controls, open Page.",
      "Connect an existing Facebook Page, or create a new one (it can be empty).",
      "Confirm the Page is linked to this Instagram account.",
    ],
  },
];

/**
 * The pre-flight prerequisite screen. It screens for the two hard requirements
 * BEFORE the OAuth handoff and shows a short guided fix for each, so the creator
 * reaches Meta already qualified. If a connect attempt bounced back because no
 * Business account was linked, we land here with a guiding banner rather than a
 * raw error — never a dead-end.
 */
export default async function Start({
  searchParams,
}: {
  searchParams: Promise<{ fix?: string; error?: string }>;
}) {
  const { fix, error } = await searchParams;

  return (
    <main className="mx-auto flex min-h-screen max-w-2xl flex-col gap-8 px-6 py-16">
      <div className="flex flex-col gap-2">
        <Link href="/" className="text-xs text-[var(--muted)] hover:underline">
          ← back
        </Link>
        <h1 className="text-2xl font-bold">Two quick checks before we connect</h1>
        <p className="text-sm text-[var(--ink-secondary)]">
          Instagram only shares the insights behind a verified score with a
          Business or Creator account that&rsquo;s linked to a Facebook Page. Make
          sure you have both, then connect — it takes a couple of taps.
        </p>
      </div>

      {fix === "business_account" && (
        <div className="rounded-lg border border-[var(--color-warning)] bg-[color-mix(in_oklab,orange_10%,transparent)] p-4 text-sm">
          We couldn&rsquo;t find a Business or Creator account linked to a Facebook
          Page on that login. Fix the two items below and try again — you
          won&rsquo;t lose your place.
        </div>
      )}
      {error && (
        <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-2)] p-4 text-sm text-[var(--ink-secondary)]">
          The connection didn&rsquo;t finish ({error}). It&rsquo;s safe to try
          again below.
        </div>
      )}

      <div className="grid gap-4 sm:grid-cols-2">
        {FIXES.map((f) => (
          <div
            key={f.title}
            className="rounded-xl border border-[var(--line)] bg-[var(--surface)] p-5"
          >
            <h2 className="text-sm font-semibold">{f.title}</h2>
            <ol className="mt-3 flex flex-col gap-2 text-sm text-[var(--ink-secondary)]">
              {f.steps.map((s, i) => (
                <li key={i} className="flex gap-2">
                  <span className="text-[var(--muted)]">{i + 1}.</span>
                  <span>{s}</span>
                </li>
              ))}
            </ol>
          </div>
        ))}
      </div>

      <PreflightGate />

      <div className="rounded-xl border border-[var(--line)] bg-[var(--surface-2)] p-5">
        <h2 className="text-sm font-semibold">Can&rsquo;t set this up right now?</h2>
        <p className="mt-1 mb-3 text-sm text-[var(--ink-secondary)]">
          Leave your email and we&rsquo;ll walk you through it and remind you when
          you&rsquo;re ready. You won&rsquo;t lose your spot.
        </p>
        <WaitlistForm
          source="connect_wall"
          cta="Email me the steps"
          successText="Done — we'll be in touch with the steps. Come back any time."
        />
      </div>
    </main>
  );
}
