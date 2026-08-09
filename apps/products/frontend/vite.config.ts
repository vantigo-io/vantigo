/// <reference types="vitest/config" />
import tanstackRouter from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// Base path for serving the app on a shared domain (default "/products").
// Set VITE_BASE_PATH="" (or "/") to serve from the domain root. Must match the
// API's App__BasePath setting.
const rawBasePath = process.env.VITE_BASE_PATH ?? "/products";
const basePath = `/${rawBasePath.replace(/^\/+|\/+$/g, "")}`.replace(/^\/$/, "");
const apiTarget = process.env.services__products_api__http__0 || "http://localhost:10020";

export default defineConfig(({ mode }) => ({
  // Unit tests assert on root-relative URLs; only dev/build use the base path.
  base: mode === "test" ? "/" : `${basePath}/`,
  plugins: [
    tanstackRouter({
      target: "react",
      autoCodeSplitting: true,
      routesDirectory: "src/routes",
      generatedRouteTree: "src/routeTree.gen.ts",
      quoteStyle: "single",
      routeFileIgnorePrefix: "-",
    }),
    react(),
  ],
  server: {
    port: 10021,
    proxy: {
      [`${basePath}/api`]: {
        target: apiTarget,
        changeOrigin: true,
        secure: false,
      },
      [`${basePath}/auth`]: {
        target: apiTarget,
        changeOrigin: true,
        secure: false,
      },
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  test: {
    environment: "jsdom",
    setupFiles: ["src/test/setup.ts"],
    globals: false,
  },
}));
