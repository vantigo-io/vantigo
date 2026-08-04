/// <reference types="vitest/config" />
import tanstackRouter from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";
export default defineConfig({
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
  server: {
    port: 10012,
    proxy: {
      "/api": {
        target: process.env.services__communications_api__http__0 || "http://localhost:5260",
        changeOrigin: true,
        secure: false,
      },
      "/auth": {
        target: process.env.services__communications_api__http__0 || "http://localhost:5260",
        changeOrigin: true,
        secure: false,
      },
    },
  },
  build: { outDir: "dist", emptyOutDir: true },
  test: { environment: "jsdom", setupFiles: ["src/test/setup.ts"] },
});
