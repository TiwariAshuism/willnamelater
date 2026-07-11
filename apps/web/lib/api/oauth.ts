import "server-only";
import type {
  AuthorizeResponse,
  ConnectionResponse,
} from "@influaudit/contracts";
import { backendFetch } from "@/lib/api/http";

/** GET /oauth/{provider}/authorize — begin an OAuth grant; returns the
 * provider's consent URL plus an anti-CSRF `state` value. */
export function authorize(
  provider: string,
  token: string,
): Promise<AuthorizeResponse> {
  return backendFetch<AuthorizeResponse>(
    `/oauth/${encodeURIComponent(provider)}/authorize`,
    { token },
  );
}

/** GET /oauth/{provider}/callback — exchange the provider `code` for a stored
 * connection. The backend reads `code`/`state` from the query string (they are
 * not modelled in the OpenAPI spec, but the OAuth handshake requires them). */
export function callback(
  provider: string,
  params: { code: string; state: string },
  token: string,
): Promise<ConnectionResponse> {
  return backendFetch<ConnectionResponse>(
    `/oauth/${encodeURIComponent(provider)}/callback`,
    { token, query: { code: params.code, state: params.state } },
  );
}

/** GET /oauth/connections — the caller's connected accounts. */
export function listConnections(token: string): Promise<ConnectionResponse[]> {
  return backendFetch<ConnectionResponse[]>("/oauth/connections", { token });
}

/** DELETE /oauth/{provider} — remove a connection. */
export function disconnect(provider: string, token: string): Promise<void> {
  return backendFetch<void>(`/oauth/${encodeURIComponent(provider)}`, {
    method: "DELETE",
    token,
  });
}
