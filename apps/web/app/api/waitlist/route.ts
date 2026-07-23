import { NextResponse } from "next/server";
import { captureWaitlist } from "@/lib/api/funnel";
import { ApiError } from "@/lib/api/http";

const ALLOWED_SOURCES = new Set(["connect_wall", "mediakit"]);

/**
 * POST /api/waitlist   body: { email, source }
 *
 * Captures an email for the connect-wall return path (story B3) or the media-kit
 * waitlist (story F1). Forwards to the backend, which is idempotent on
 * (email, source). The browser posts here so API_BASE_URL stays server-only.
 */
export async function POST(request: Request): Promise<NextResponse> {
  let email = "";
  let source = "";
  try {
    const body = (await request.json()) as { email?: string; source?: string };
    email = (body.email ?? "").trim();
    source = (body.source ?? "").trim();
  } catch {
    return NextResponse.json(
      { code: "invalid_body", message: "expected a JSON body" },
      { status: 400 },
    );
  }
  if (!email) {
    return NextResponse.json(
      { code: "email_required", message: "an email is required" },
      { status: 400 },
    );
  }
  if (!ALLOWED_SOURCES.has(source)) {
    return NextResponse.json(
      { code: "invalid_source", message: "unknown waitlist source" },
      { status: 400 },
    );
  }

  try {
    await captureWaitlist({ email, source });
    return new NextResponse(null, { status: 204 });
  } catch (error) {
    const status = error instanceof ApiError ? error.status : 502;
    const code = error instanceof ApiError ? error.code : "waitlist_failed";
    const message =
      error instanceof ApiError ? error.message : "could not capture your email";
    return NextResponse.json({ code, message }, { status });
  }
}
