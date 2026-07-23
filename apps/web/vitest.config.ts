import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./vitest.setup.ts"],
    include: ["**/*.test.ts", "**/*.test.tsx"],
    exclude: ["node_modules/**", ".next/**"],
    env: {
      API_BASE_URL: "http://backend.test/v1",
      APP_BASE_URL: "http://localhost:3000",
      SESSION_COOKIE_SECURE: "false",
    },
  },
  resolve: {
    alias: {
      // The `server-only` guard throws when imported outside a React Server
      // Component. Under the test runner there is no RSC boundary, so stub it.
      "server-only": fileURLToPath(
        new URL("./test/stubs/empty.ts", import.meta.url),
      ),
      "@": fileURLToPath(new URL("./", import.meta.url)),
    },
  },
});
