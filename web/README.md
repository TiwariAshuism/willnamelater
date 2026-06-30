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

## Editing content

Change a headline, price, nav item or footer link by editing the matching field in
`data/site.json`, `data/home.json` or `data/contact.json`. Types in `lib/types.ts`
keep the JSON honest at build time.
