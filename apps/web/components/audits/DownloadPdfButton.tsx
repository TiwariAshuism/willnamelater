/**
 * Downloads the audit report PDF. Points at our own route handler, which
 * attaches the Bearer token server-side and streams the PDF back — the browser
 * only ever sees the /api URL, never the JWT.
 */
export function DownloadPdfButton({ auditId }: { auditId: string }) {
  return (
    <a
      href={`/api/audits/${auditId}/report.pdf`}
      className="inline-flex items-center justify-center gap-2 rounded-md border border-[var(--line)] bg-[var(--surface)] px-4 py-2 text-sm font-medium text-[var(--ink)] transition-colors hover:bg-[var(--surface-2)]"
    >
      Download PDF
    </a>
  );
}
