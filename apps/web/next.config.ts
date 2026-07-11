import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // The contract package ships raw TypeScript (its `exports` point at .ts
  // sources), so Next must transpile it rather than expect prebuilt JS.
  transpilePackages: ["@influaudit/contracts"],
};

export default nextConfig;
