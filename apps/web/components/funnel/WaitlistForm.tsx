"use client";

import { useState, type FormEvent } from "react";
import { Button } from "@/components/ui/Button";
import { Field } from "@/components/ui/Field";
import { track } from "@/lib/track";

/**
 * A small email-capture form used in two places:
 *   - source="connect_wall": the never-dead-end fallback for a creator who
 *     can't clear the connect prerequisites right now (story B3), and
 *   - source="mediakit": the Phase-2 media-kit waitlist (story F1), whose click
 *     is also tracked so Phase-2 demand is measured before it is built.
 */
export function WaitlistForm({
  source,
  cta,
  successText,
}: {
  source: "connect_wall" | "mediakit";
  cta: string;
  successText: string;
}) {
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    if (source === "mediakit") track("mediakit_cta_click");
    try {
      const res = await fetch("/api/waitlist", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, source }),
      });
      if (!res.ok) {
        const data = (await res.json().catch(() => ({}))) as {
          message?: string;
        };
        setError(data.message ?? "Could not save your email. Try again.");
        setBusy(false);
        return;
      }
      setDone(true);
    } catch {
      setError("Something went wrong. Please try again.");
      setBusy(false);
    }
  }

  if (done) {
    return <p className="text-sm text-[var(--color-good)]">{successText}</p>;
  }

  return (
    <form onSubmit={onSubmit} className="flex flex-col gap-3 sm:flex-row sm:items-end">
      <div className="flex-1">
        <Field
          label="Email"
          name={`waitlist-${source}`}
          type="email"
          required
          autoComplete="email"
          placeholder="you@example.com"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          disabled={busy}
        />
      </div>
      <Button type="submit" variant="secondary" disabled={busy || !email}>
        {busy ? "Saving…" : cta}
      </Button>
      {error && <p className="text-sm text-[var(--color-critical)]">{error}</p>}
    </form>
  );
}
