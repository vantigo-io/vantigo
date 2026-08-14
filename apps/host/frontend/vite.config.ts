/// <reference types="vitest/config" />
import tanstackRouter from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// Aspire injects the API address as services__{resource}__{endpoint}__{index};
// fall back to the launch-profile port for standalone `bun run dev`.
const apiTarget = process.env["services__vantigo-api__http__0"] || "http://localhost:10010";

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
  server: { port: 10011, proxy: { "/api": { target: apiTarget, changeOrigin: true, secure: false } } },
  build: { outDir: "dist", emptyOutDir: true },
  test: { environment: "jsdom", setupFiles: ["src/test/setup.ts"], globals: false },
});
