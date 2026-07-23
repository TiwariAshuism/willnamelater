"use client";

import { useState, type FormEvent } from "react";
import { Button } from "@/components/ui/Button";
import { Field } from "@/components/ui/Field";
import { track } from "@/lib/track";

/**
 * The connect step of the public funnel. Captures the creator's email and starts
 * the anonymous OAuth-as-signup grant: it posts the email to our own route
 * handler (which mints the anti-CSRF state server-side) and then sends the
 * browser to the Meta consent screen. The account is created on the callback —
 * the creator types nothing but their email.
 */
export function ConnectForm() {
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function onConnect(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    track("connect_start");
    try {
      const res = await fetch("/api/oauth/signup/start", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email }),
      });
      if (!res.ok) {
        const data = (await res.json().catch(() => ({}))) as {
          message?: string;
        };
        setError(data.message ?? "Could not start the connection. Try again.");
        setBusy(false);
        return;
      }
      const { authorization_url } = (await res.json()) as {
        authorization_url: string;
      };
      // Leaving the app for the Meta consent screen.
      window.location.href = authorization_url;
    } catch {
      setError("Something went wrong. Please try again.");
      setBusy(false);
    }
  }

  return (
    <form onSubmit={onConnect} className="flex flex-col gap-3">
      <Field
        label="Your email"
        name="email"
        type="email"
        required
        autoComplete="email"
        placeholder="you@example.com"
        value={email}
        onChange={(e) => setEmail(e.target.value)}
        disabled={busy}
      />
      <Button type="submit" disabled={busy || !email}>
        {busy ? "Connecting…" : "Connect Instagram & get my score"}
      </Button>
      {error && (
        <p className="text-sm text-[var(--color-critical)]">{error}</p>
      )}
      <p className="text-xs text-[var(--muted)]">
        We only request read access to your insights. We never post, and every
        number on your score is pulled directly from Instagram — you can&rsquo;t
        edit it, and neither can we.
      </p>
    </form>
  );
}
