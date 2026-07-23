# @influaudit/web

The InfluAudit influencer dashboard — Next.js 16 (App Router, React Server
Components), strict TypeScript, Tailwind v4.

## Getting started

```bash
# from the monorepo root
pnpm install
pnpm --filter @influaudit/contracts generate   # generate the typed API client
cp apps/web/.env.example apps/web/.env          # then edit values
pnpm --filter @influaudit/web dev
```

The backend (Go modular monolith) must be running and serving the OpenAPI spec
under `/v1` — by default `http://localhost:8080/v1`.

## Configuration

All config is **server-only** (no `NEXT_PUBLIC_*`), so the backend URL and the
JWT never reach browser JS. See `.env.example`:

| Variable | Purpose |
|---|---|
| `API_BASE_URL` | Backend base URL **including** `/v1`. |
| `APP_BASE_URL` | Public URL of this app; used to build OAuth redirect URIs. |
| `SESSION_COOKIE_SECURE` | `true` to mark session cookies `Secure` (prod/staging). |

## The typed API client

Request/response types are **generated**, never hand-written. The source of
truth is `packages/contracts/openapi/influaudit.yaml`:

```bash
pnpm --filter @influaudit/contracts generate
# → packages/contracts/gen/ts/schema.d.ts (openapi-typescript)
```

`lib/api/*` are thin functions over `lib/api/http.ts` that consume those types
(`@influaudit/contracts`). There is **no mock data layer**: every call hits the
real backend.

## Session-cookie auth (server-owned)

1. The login/register form (`components/auth/AuthForm.tsx`) POSTs credentials to
   our own **route handler** (`app/api/auth/{login,register}`).
2. The route handler calls the backend, then writes the JWT access token, the
   refresh token, and the access-token expiry as **HttpOnly, SameSite=Lax**
   cookies (`Secure` when configured). The JWT is never returned to the browser.
3. Server Components read the access cookie (`lib/session.ts`) and call the
   backend with `Authorization: Bearer`.
4. **Refresh** happens in `middleware.ts`: before a protected route renders, if
   the access token is missing/expired but a refresh token exists, it exchanges
   it against the backend and writes fresh cookies onto the response (a Server
   Component cannot set cookies during render, so refresh must happen here).
5. Logout (`app/api/auth/logout`) best-effort revokes the refresh token, then
   clears all cookies.

## Flows

- **register / login / logout** — `app/login`, `app/register`, `LogoutButton`.
- **Connect YouTube (Google OAuth)** — `app/api/oauth/[provider]/start` gets the
  consent URL from the backend, stashes the `state` in a short-lived HttpOnly
  cookie (CSRF), and redirects to Google; `app/api/oauth/[provider]/callback`
  verifies `state`, completes the exchange against the backend, and returns to
  `/connections`.
- **Run / list / view audits** — `app/(dashboard)/audits`. Submitting uses a
  Server Action with a server-generated idempotency key.
- **Score + trend chart** — `components/audits/ScoreTrendChart.tsx`, built with
  the `dataviz` method (single-series line, crosshair + tooltip, light/dark).
- **Download PDF** — `app/api/audits/[id]/report.pdf` streams the backend PDF
  with the Bearer token attached server-side.

## Tests

```bash
pnpm --filter @influaudit/web test
```

Vitest + Testing Library. Route-handler tests cover cookie-setting (login) and
the OAuth callback (CSRF + exchange). The data layer is not mocked — tests stub
the transport (`global.fetch`) and the framework cookie boundary only.
