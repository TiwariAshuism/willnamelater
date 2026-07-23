import { WaitlistForm } from "@/components/funnel/WaitlistForm";

/**
 * The Phase-2 seam (PRD §5, story F1): "turn this into a brand-ready media kit."
 * In v1 it is a waitlist — the click is tracked so Phase-2 demand is proven before
 * the feature is built. It shows on the creator's own result, not the public page.
 */
export function MediaKitCta() {
  return (
    <div className="rounded-xl border border-[var(--line)] bg-[var(--surface-2)] p-5">
      <h2 className="text-sm font-semibold">Turn this into a brand-ready media kit</h2>
      <p className="mb-3 mt-1 text-sm text-[var(--ink-secondary)]">
        We&rsquo;re building a one-click media kit from this exact data — the
        pitch-ready artifact you send to brands. Want early access?
      </p>
      <WaitlistForm
        source="mediakit"
        cta="Notify me"
        successText="You're on the list — we'll email you when the media kit is ready."
      />
    </div>
  );
}
