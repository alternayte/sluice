import path from "node:path";
import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

// The Go server that the dev server proxies to. The justfile loads .env, so
// VITE_API_TARGET from .env reaches this process.
const apiTarget = process.env.VITE_API_TARGET ?? "http://127.0.0.1:8080";

export default defineConfig({
  plugins: [
    tanstackRouter({ target: "react", autoCodeSplitting: true }),
    react(),
    tailwindcss(),
  ],
  resolve: {
    alias: { "@": path.resolve(__dirname, "./src") },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    manifest: true,
    assetsDir: "assets",
  },
  server: {
    proxy: {
      "/api": apiTarget,
      "/hooks": apiTarget,
      "/mcp": apiTarget,
    },
  },
  test: {
    environment: "jsdom",
    include: ["src/**/*.test.{ts,tsx}"],
    setupFiles: ["./src/test-setup.ts"],
  },
});
