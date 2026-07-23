"use client";

import { useState, type FormEvent } from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import { Button } from "@/components/ui/Button";
import { Field } from "@/components/ui/Field";

type Mode = "login" | "register";

interface AuthFormProps {
  mode: Mode;
  /** Where to send the user after success (defaults to /dashboard). */
  next?: string;
}

/**
 * Login / register form. It POSTs credentials to our own route handler, which
 * sets the HttpOnly session cookie server-side — the browser never sees a JWT.
 * On success we navigate; the server-set cookie authenticates the next request.
 */
export function AuthForm({ mode, next }: AuthFormProps) {
  const router = useRouter();
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);

  const endpoint =
    mode === "login" ? "/api/auth/login" : "/api/auth/register";

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError(null);
    setPending(true);

    const form = new FormData(event.currentTarget);
    const payload: Record<string, string> = {
      email: String(form.get("email") ?? ""),
      password: String(form.get("password") ?? ""),
    };
    if (mode === "register") {
      payload.full_name = String(form.get("full_name") ?? "");
    }

    try {
      const res = await fetch(endpoint, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });

      if (!res.ok) {
        const data = (await res.json().catch(() => ({}))) as {
          message?: string;
        };
        setError(data.message ?? "Something went wrong. Please try again.");
        setPending(false);
        return;
      }

      router.push(next ?? "/dashboard");
      router.refresh();
    } catch {
      setError("Network error. Is the backend running?");
      setPending(false);
    }
  }

  return (
    <form onSubmit={onSubmit} className="flex flex-col gap-4">
      {mode === "register" && (
        <Field
          label="Full name"
          name="full_name"
          type="text"
          autoComplete="name"
          required
        />
      )}
      <Field
        label="Email"
        name="email"
        type="email"
        autoComplete="email"
        required
      />
      <Field
        label="Password"
        name="password"
        type="password"
        autoComplete={mode === "login" ? "current-password" : "new-password"}
        required
        minLength={8}
      />

      {error && (
        <p role="alert" className="text-sm text-[var(--color-critical)]">
          {error}
        </p>
      )}

      <Button type="submit" disabled={pending}>
        {pending
          ? "Please wait…"
          : mode === "login"
            ? "Sign in"
            : "Create account"}
      </Button>

      <p className="text-sm text-[var(--ink-secondary)]">
        {mode === "login" ? (
          <>
            No account?{" "}
            <Link href="/register" className="text-[var(--color-brand)] underline">
              Create one
            </Link>
          </>
        ) : (
          <>
            Already have an account?{" "}
            <Link href="/login" className="text-[var(--color-brand)] underline">
              Sign in
            </Link>
          </>
        )}
      </p>
    </form>
  );
}
