import { NextResponse } from "next/server";
import { getReportPdf } from "@/lib/api/audits";
import { getAccessToken } from "@/lib/session";
import { ApiError } from "@/lib/api/http";

/**
 * GET /api/audits/{id}/report.pdf
 *
 * Streams the audit's rendered PDF from the backend to the browser as a
 * download, forwarding the caller's Bearer token server-side.
 *
 * The OpenAPI spec models this response as `application/json` with a
 * base64 (`format: byte`) string, but a PDF endpoint may equally return raw
 * `application/pdf` bytes. We handle both: if the backend sends JSON we decode
 * the base64 payload, otherwise we pass the bytes through unchanged.
 */
export async function GET(
  _request: Request,
  ctx: { params: Promise<{ id: string }> },
): Promise<NextResponse> {
  const { id } = await ctx.params;

  const token = await getAccessToken();
  if (!token) {
    return NextResponse.json({ message: "Not authenticated" }, { status: 401 });
  }

  let upstream: Response;
  try {
    upstream = await getReportPdf(id, token);
  } catch (error) {
    if (error instanceof ApiError) {
      return NextResponse.json(
        { message: error.message, code: error.code },
        { status: error.status },
      );
    }
    return NextResponse.json(
      { message: "Failed to fetch report PDF" },
      { status: 502 },
    );
  }

  const contentType = upstream.headers.get("content-type") ?? "";
  let pdfBytes: ArrayBuffer;

  if (contentType.includes("application/json")) {
    // apigen-style envelope: a base64-encoded byte string.
    const text = await upstream.text();
    let base64 = text;
    try {
      const parsed = JSON.parse(text) as unknown;
      if (typeof parsed === "string") base64 = parsed;
    } catch {
      // Not JSON-wrapped after all; treat the text itself as base64.
    }
    const buf = Buffer.from(base64, "base64");
    pdfBytes = buf.buffer.slice(
      buf.byteOffset,
      buf.byteOffset + buf.byteLength,
    ) as ArrayBuffer;
  } else {
    pdfBytes = await upstream.arrayBuffer();
  }

  return new NextResponse(new Blob([pdfBytes], { type: "application/pdf" }), {
    status: 200,
    headers: {
      "Content-Type": "application/pdf",
      "Content-Disposition": `attachment; filename="audit-${id}.pdf"`,
      "Cache-Control": "no-store",
    },
  });
}
