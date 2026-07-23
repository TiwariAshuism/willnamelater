import { NextResponse } from "next/server";
import type { RegisterRequest } from "@influaudit/contracts";
import { register } from "@/lib/api/auth";
import { writeSession } from "@/lib/session";
import { ApiError } from "@/lib/api/http";

/**
 * POST /api/auth/register
 *
 * Proxies registration to the backend, then stores the returned JWT in an
 * HttpOnly cookie. The token is never returned to the browser.
 */
export async function POST(request: Request): Promise<NextResponse> {
  let body: RegisterRequest;
  try {
    body = (await request.json()) as RegisterRequest;
  } catch {
    return NextResponse.json({ message: "Invalid JSON body" }, { status: 400 });
  }

  if (!body.email || !body.password || !body.full_name) {
    return NextResponse.json(
      { message: "email, password and full_name are required" },
      { status: 400 },
    );
  }

  try {
    const auth = await register(body);
    await writeSession(auth);
    return NextResponse.json({ user: auth.user });
  } catch (error) {
    if (error instanceof ApiError) {
      return NextResponse.json(
        { message: error.message, code: error.code },
        { status: error.status },
      );
    }
    return NextResponse.json(
      { message: "Registration failed" },
      { status: 502 },
    );
  }
}
