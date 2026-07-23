"use client";

import { useState, useTransition } from "react";
import {
  publishReportAction,
  type PublishState,
} from "@/app/(dashboard)/audits/actions";

/**
 * Publishes the audit's report and reveals the shareable public badge link. The
 * publish call runs as a Server Action (the Bearer token stays server-side); on
 * success it shows the durable badge URL with copy-to-clipboard plus a direct
 * PDF link. Re-publishing is idempotent, so the link is stable.
 */
export function ShareReport({ auditId }: { auditId: string }) {
  const [state, setState] = useState<PublishState>({});
  const [copied, setCopied] = useState(false);
  const [pending, startTransition] = useTransition();

  function publish() {
    setCopied(false);
    startTransition(async () => {
      setState(await publishReportAction(auditId));
    });
  }

  async function copy() {
    if (!state.badgeUrl) return;
    try {
      await navigator.clipboard.writeText(state.badgeUrl);
      setCopied(true);
    } catch {
      // Clipboard permission denied; the link is still selectable in the field.
    }
  }

  return (
    <div className="flex flex-col gap-3">
      <button
        type="button"
        onClick={publish}
        disabled={pending}
        className="inline-flex w-fit items-center justify-center gap-2 rounded-md border border-[var(--line)] bg-[var(--surface)] px-4 py-2 text-sm font-medium text-[var(--ink)] transition-colors hover:bg-[var(--surface-2)] disabled:opacity-60"
      >
        {pending ? "Publishing…" : state.badgeUrl ? "Re-publish" : "Publish & share"}
      </button>

      {state.error && (
        <p className="text-sm text-[var(--color-critical)]">{state.error}</p>
      )}

      {state.badgeUrl && (
        <div className="flex flex-col gap-2 rounded-md border border-[var(--line)] bg-[var(--surface-2)] p-3">
          <p className="text-xs font-medium text-[var(--muted)]">
            Public badge link
          </p>
          <div className="flex items-center gap-2">
            <input
              readOnly
              value={state.badgeUrl}
              className="min-w-0 flex-1 rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1 text-sm"
              onFocus={(e) => e.currentTarget.select()}
            />
            <button
              type="button"
              onClick={copy}
              className="shrink-0 rounded border border-[var(--line)] px-3 py-1 text-sm hover:bg-[var(--surface)]"
            >
              {copied ? "Copied" : "Copy"}
            </button>
          </div>
          <div className="flex gap-4 text-sm">
            <a
              href={state.badgeUrl}
              target="_blank"
              rel="noreferrer"
              className="text-[var(--color-brand)] underline"
            >
              Open badge
            </a>
            {state.pdfUrl && (
              <a
                href={state.pdfUrl}
                target="_blank"
                rel="noreferrer"
                className="text-[var(--color-brand)] underline"
              >
                Direct PDF link
              </a>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
