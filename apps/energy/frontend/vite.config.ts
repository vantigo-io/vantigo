/// <reference types="vitest/config" />
import { defineConfig } from "vite";

// testTimeout: Vitest's five-second default has flaked on slow, loaded CI
// runners. The limit exists to catch a hang, not to race the runner.
export default defineConfig({
  test: { environment: "jsdom", setupFiles: ["src/test/setup.ts"], globals: false, testTimeout: 15_000 },
});
