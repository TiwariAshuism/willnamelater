import "server-only";
import type {
  AuthResponse,
  LoginRequest,
  LogoutRequest,
  RefreshRequest,
  RegisterRequest,
  UserResponse,
} from "@influaudit/contracts";
import { backendFetch } from "@/lib/api/http";

/** POST /auth/register — create an account and receive tokens. */
export function register(body: RegisterRequest): Promise<AuthResponse> {
  return backendFetch<AuthResponse>("/auth/register", {
    method: "POST",
    body,
  });
}

/** POST /auth/login — exchange credentials for tokens. */
export function login(body: LoginRequest): Promise<AuthResponse> {
  return backendFetch<AuthResponse>("/auth/login", {
    method: "POST",
    body,
  });
}

/** POST /auth/refresh — exchange a refresh token for a fresh token pair. */
export function refresh(body: RefreshRequest): Promise<AuthResponse> {
  return backendFetch<AuthResponse>("/auth/refresh", {
    method: "POST",
    body,
  });
}

/** POST /auth/logout — revoke the refresh token server-side. */
export function logout(body: LogoutRequest, token: string): Promise<void> {
  return backendFetch<void>("/auth/logout", {
    method: "POST",
    body,
    token,
  });
}

/** GET /auth/me — the authenticated user. */
export function me(token: string): Promise<UserResponse> {
  return backendFetch<UserResponse>("/auth/me", { token });
}
