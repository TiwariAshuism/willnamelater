import Link from "next/link";

/**
 * A plain navigation to the OAuth start route handler. It is a link (not a
 * fetch) on purpose: the start handler issues a 302 to the provider's consent
 * screen, which the browser must follow as a top-level navigation.
 */
export function ConnectAccountButton({
  provider,
  label,
}: {
  provider: string;
  label: string;
}) {
  return (
    <Link
      href={`/api/oauth/${provider}/start`}
      className="inline-flex items-center justify-center gap-2 rounded-md bg-[var(--color-brand)] px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-[var(--color-brand-strong)]"
    >
      {label}
    </Link>
  );
}
