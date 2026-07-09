# Pallet Ross — Art Marketplace

A clean, server-rendered **Next.js 16 (App Router) + React 19 + TypeScript + Tailwind v4**
rebuild of the original static "Pallet Ross" design export. All copy and structured
content is data-driven from JSON; animations and interactivity are isolated into small
client components.

## Getting started

```bash
pnpm install
pnpm dev      # http://localhost:3000
pnpm build    # production build (prerenders / and /contact on the server)
pnpm start    # serve the production build
pnpm lint
```

## How it's built

| Concern | Approach |
| --- | --- |
| **Rendering** | App Router. Pages and sections are React **Server Components** rendered to HTML on the server; only interactive pieces opt into `"use client"`. |
| **Data** | All content lives in [`data/*.json`](./data), typed by [`lib/types.ts`](./lib/types.ts) and loaded through [`lib/content.ts`](./lib/content.ts). Edit JSON to change copy — no component edits needed. |
| **Styling** | Tailwind CSS v4 with design tokens (`--color-ink`, `--color-orange`, …) declared in [`app/globals.css`](./app/globals.css). |
| **Icons** | [`lucide-react`](https://lucide.dev) — replaces every hand-coded SVG from the original export. Brand glyphs (X, Behance…) live in `components/ui/social-icon.tsx`. |
| **Animation** | [`motion`](https://motion.dev) (Framer Motion) — scroll reveals, the hero card fan, animated stat counters, carousels and the contact form, replacing the original GSAP + bespoke runtime. |
| **Images** | Decorative art uses picsum seeds via `next/image` (`components/ui/seeded-image.tsx`); remote host is allow-listed in `next.config.ts`. |

## Structure

```
app/
  layout.tsx            # Inter font + metadata
  page.tsx              # home — composes all sections
  contact/page.tsx      # contact page (server) + form (client)
  globals.css           # Tailwind + design tokens + keyframes
components/
  layout/               # Navbar, Footer, Background, PageLoader
  sections/             # one component per home section
  ui/                   # Reveal/Headline/Stagger, Icon, Logo, SeededImage, …
  contact/contact-form.tsx
data/                   # site.json, home.json, contact.json  ← source of truth
lib/                    # types.ts, content.ts, cn.ts, words.ts
```

## Deploying to Vercel

This app lives in the **`web/` subdirectory** of the `willnamelater` repo, so the most
important setting is the **Root Directory**.

### One-time project setup (Vercel dashboard)

1. **Add New… → Project** and import the `TiwariAshuism/willnamelater` repo.
2. **Root Directory → `web`** (click *Edit* and select the `web` folder). This is required —
   without it Vercel builds the repo root and won't find the app.
3. **Framework Preset:** Next.js (auto-detected).
4. Build & install commands are auto-detected from [`vercel.json`](./vercel.json) and the
   pnpm lockfile — leave the overrides off:
   | Setting | Value |
   | --- | --- |
   | Install Command | `pnpm install` (auto) |
   | Build Command | `next build` (auto) |
   | Output Directory | `.next` (auto) |
   | Node.js Version | `22.x` (pinned via `engines` in `package.json`) |
   | Package Manager | `pnpm@10.32.1` (pinned via `packageManager`) |
5. **Environment Variables:** none required.
6. **Deploy.** Every push to `main` ships to production; other branches get preview URLs.

[`vercel.json`](./vercel.json) pins the Next.js framework and adds baseline security headers
(`X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`, `Permissions-Policy`).
Remote images (`picsum.photos`) are already allow-listed in `next.config.ts` so the Vercel
Image Optimization step works in production.

### Or deploy from the CLI

```bash
npm i -g vercel
cd web
vercel          # first run: link project, set Root Directory to "web"
vercel --prod   # promote to production
```

## Editing content

Change a headline, price, nav item or footer link by editing the matching field in
`data/site.json`, `data/home.json` or `data/contact.json`. Types in `lib/types.ts`
keep the JSON honest at build time.
