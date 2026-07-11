"use server";

import { redirect } from "next/navigation";
import { requireToken } from "@/lib/auth";
import { submitAudit } from "@/lib/api/audits";
import { publishReport } from "@/lib/api/report";
import { appBaseUrl } from "@/lib/env";
import { ApiError } from "@/lib/api/http";

export interface RunAuditState {
  error?: string;
}

/**
 * Server Action: submit an audit for an influencer. Generates the
 * idempotency key server-side so a double submit is a no-op on the backend,
 * then redirects to the new audit's status page.
 */
export async function runAuditAction(
  _prev: RunAuditState,
  formData: FormData,
): Promise<RunAuditState> {
  const token = await requireToken();

  const influencerId = String(formData.get("influencer_id") ?? "").trim();
  if (!influencerId) {
    return { error: "An influencer is required." };
  }
  const requestedPlatforms = formData
    .getAll("platforms")
    .map((value) => String(value))
    .filter(Boolean);

  let auditId: string;
  try {
    const audit = await submitAudit(
      {
        influencer_id: influencerId,
        idempotency_key: crypto.randomUUID(),
        requested_platforms:
          requestedPlatforms.length > 0 ? requestedPlatforms : undefined,
      },
      token,
    );
    auditId = audit.id;
  } catch (error) {
    if (error instanceof ApiError) {
      return { error: error.message };
    }
    return { error: "Could not submit the audit." };
  }

  redirect(`/audits/${auditId}`);
}

export interface PublishState {
  slug?: string;
  /** Absolute, shareable URL to the public badge page on this web app. */
  badgeUrl?: string;
  /** Presigned, expiring link to the rendered PDF. */
  pdfUrl?: string;
  error?: string;
}

/**
 * Server Action: publish an audit's report. Renders + stores the PDF and mints
 * the durable public badge on the backend, then returns the absolute badge URL
 * (built from this app's own base URL) so the client can show and copy a link
 * that never breaks across re-publishes.
 */
export async function publishReportAction(
  auditId: string,
): Promise<PublishState> {
  const token = await requireToken();
  try {
    const result = await publishReport(auditId, token);
    return {
      slug: result.public_slug,
      badgeUrl: `${appBaseUrl()}/badge/${result.public_slug}`,
      pdfUrl: result.pdf_url,
    };
  } catch (error) {
    if (error instanceof ApiError) {
      return { error: error.message };
    }
    return { error: "Could not publish the report." };
  }
}
