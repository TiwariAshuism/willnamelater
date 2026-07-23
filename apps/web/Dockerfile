# Build context is the REPOSITORY ROOT, not apps/web.
#
#   docker build -f apps/web/Dockerfile .
#
# The context has to be the root because this is a pnpm workspace: the build needs
# the root lockfile and pnpm-workspace.yaml to install deterministically, and
# @influaudit/contracts is a workspace dependency whose sources live outside
# apps/web. Same reason the backend image builds from the root.

# ---- deps -------------------------------------------------------------------
# Dependencies install from the manifests alone, so this layer is cached until a
# lockfile or a package.json actually changes — not on every source edit.
FROM node:22-alpine AS deps

RUN corepack enable

WORKDIR /repo

# packages/scripts is listed in pnpm-workspace.yaml but is an empty directory
# with no package.json, so pnpm does not treat it as a workspace package and it
# is not in the lockfile. Nothing to copy.
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY apps/web/package.json ./apps/web/
COPY packages/contracts/package.json ./packages/contracts/

# --frozen-lockfile makes an out-of-date lockfile a build failure rather than a
# silent resolution to different versions than CI tested.
RUN pnpm install --frozen-lockfile

# ---- build ------------------------------------------------------------------
FROM node:22-alpine AS build

RUN corepack enable

WORKDIR /repo

COPY --from=deps /repo/node_modules ./node_modules
COPY --from=deps /repo/apps/web/node_modules ./apps/web/node_modules
COPY --from=deps /repo/packages/contracts/node_modules ./packages/contracts/node_modules

COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY packages/contracts/ ./packages/contracts/
COPY apps/web/ ./apps/web/

# Telemetry is a network call from inside a build. Refuse it.
ENV NEXT_TELEMETRY_DISABLED=1

# There is deliberately no NEXT_PUBLIC_* / ARG here. Every setting this app reads
# is resolved at runtime on the server (apps/web/lib/env.ts), so ONE image runs
# against any environment on any cloud with no rebuild — the same property the Go
# binaries have. A build-time public env var would weld the image to the
# environment it was built for and quietly end that.
RUN pnpm --filter @influaudit/web build

# ---- runtime ----------------------------------------------------------------
FROM node:22-alpine AS runtime

ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1 \
    PORT=3000 \
    HOSTNAME=0.0.0.0

WORKDIR /repo

# `output: "standalone"` traced the module graph and emitted a self-contained
# tree carrying only the reachable subset of node_modules. Copying that tree —
# rather than the workspace's — is what keeps this image small and is the only
# thing that works at all under pnpm, whose node_modules is a symlink farm into a
# store that does not survive being copied out of a build stage.
#
# Next writes it to <distDir>/standalone — apps/web/.next/standalone — and lays
# it out relative to outputFileTracingRoot (the workspace root), so the tree
# inside already has the shape ./apps/web/server.js + ./node_modules. Unpacking
# it at /repo therefore reproduces the workspace layout the server expects.
COPY --from=build --chown=node:node /repo/apps/web/.next/standalone ./
# The standalone tree deliberately excludes static assets: they are served
# straight from disk and Next expects them placed alongside it. (There is no
# public/ directory in this app; add its COPY here if one is ever introduced.)
COPY --from=build --chown=node:node /repo/apps/web/.next/static ./apps/web/.next/static

# node:alpine ships an unprivileged `node` user; the base image would otherwise
# run this as root.
USER node

EXPOSE 3000

# curl is absent from alpine; node is not, and it is already the entrypoint.
HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=5 \
    CMD ["node", "-e", "require('http').get('http://127.0.0.1:3000/', r => process.exit(r.statusCode < 500 ? 0 : 1)).on('error', () => process.exit(1))"]

CMD ["node", "apps/web/server.js"]
