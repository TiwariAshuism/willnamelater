import path from "node:path";
import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // Standalone tracing must start at the workspace root, not at this package:
  // @influaudit/contracts is a workspace dependency whose files live outside
  // apps/web, and pnpm links dependencies through a store above it. Next infers
  // this root by looking for a lockfile, and apps/pallet-ross carries a second
  // one — so the inference is ambiguous and the answer is stated here instead.
  outputFileTracingRoot: path.join(__dirname, "../.."),

  // The contract package ships raw TypeScript (its `exports` point at .ts
  // sources), so Next must transpile it rather than expect prebuilt JS.
  transpilePackages: ["@influaudit/contracts"],

  // Trace the module graph and emit a self-contained server.js carrying only the
  // reachable subset of node_modules. Without it the runtime image must ship the
  // whole pnpm store — which, in a workspace whose store is symlinked, does not
  // survive being copied out of the build stage at all.
  //
  // Note there is deliberately no NEXT_PUBLIC_* anywhere in this app: every
  // setting is read at runtime on the server (lib/env.ts). That is what lets ONE
  // image run on any cloud with no rebuild, the same property the Go binaries
  // have. Do not introduce a build-time public env var without understanding that
  // it welds the image to the environment it was built for.
  output: "standalone",
};

export default nextConfig;
