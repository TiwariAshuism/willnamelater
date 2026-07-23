import { NextResponse } from "next/server";
import type { LoginRequest } from "@influaudit/contracts";
import { login } from "@/lib/api/auth";
import { writeSession } from "@/lib/session";
import { ApiError } from "@/lib/api/http";

/**
 * POST /api/auth/login
 *
 * Proxies credentials to the backend, then stores the returned JWT in an
 * HttpOnly cookie. The token is never returned to the browser.
 */
export async function POST(request: Request): Promise<NextResponse> {
  let body: LoginRequest;
  try {
    body = (await request.json()) as LoginRequest;
  } catch {
    return NextResponse.json({ message: "Invalid JSON body" }, { status: 400 });
  }

  if (!body.email || !body.password) {
    return NextResponse.json(
      { message: "email and password are required" },
      { status: 400 },
    );
  }

  try {
    const auth = await login(body);
    await writeSession(auth);
    return NextResponse.json({ user: auth.user });
  } catch (error) {
    if (error instanceof ApiError) {
      return NextResponse.json(
        { message: error.message, code: error.code },
        { status: error.status },
      );
    }
    return NextResponse.json({ message: "Login failed" }, { status: 502 });
  }
}
