/// <reference types="vitest/config" />
import { defineConfig } from "vite";
// testTimeout: the settings page's form tests run close to three seconds on a
// four-core CI runner and have crossed Vitest's five-second default under
// load. The limit exists to catch a hang, not to race the runner.
export default defineConfig({
  test: { environment: "jsdom", setupFiles: ["src/test/setup.ts"], globals: false, testTimeout: 15_000 },
});
