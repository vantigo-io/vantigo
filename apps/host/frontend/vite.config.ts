/// <reference types="vitest/config" />
import tanstackRouter from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import { defineConfig, searchForWorkspaceRoot } from "vite";

const apiTarget = "http://localhost:8080";

export default defineConfig({
  base: "/",
  plugins: [
    tanstackRouter({
      target: "react",
      autoCodeSplitting: true,
      routesDirectory: "src/routes",
      generatedRouteTree: "src/routeTree.gen.ts",
      quoteStyle: "single",
      routeFileIgnorePrefix: "-",
      routeFileIgnorePattern: "\\.test\\.",
    }),
    react(),
  ],
  resolve: {
    dedupe: ["react", "react-dom", "@mantine/core", "@mantine/dates", "@mantine/hooks"],
  },
  server: {
    port: 10011,
    // ws: the live socket (/api/v1/live) is proxied too. Browsers send no
    // Sec-Fetch-Site on a WebSocket handshake, so the server judges it by its
    // Origin, which must be APP_URL's: the dev proxy presents the API's origin,
    // as it presents its Host through changeOrigin.
    proxy: {
      "/api": {
        target: apiTarget,
        changeOrigin: true,
        secure: false,
        ws: true,
        configure: (proxy) => {
          proxy.on("proxyReqWs", (proxyReq) => proxyReq.setHeader("origin", apiTarget));
        },
      },
    },
    fs: {
      allow: [searchForWorkspaceRoot(process.cwd())],
    },
  },
  build: { outDir: "dist", emptyOutDir: true },
  // testTimeout: Vitest's five-second default has flaked on slow, loaded CI
  // runners. The limit exists to catch a hang, not to race the runner.
  test: { environment: "jsdom", setupFiles: ["src/test/setup.ts"], globals: false, testTimeout: 15_000 },
});
