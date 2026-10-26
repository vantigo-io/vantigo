/// <reference types="vitest/config" />
import { defineConfig } from "vite";
// testTimeout: the board mounts a memory router, Mantine's drawer and a
// people × weeks table, and the sheet's tests drive a handful of inputs; on a
// four-core CI runner that crosses Vitest's five-second default under load.
// The limit exists to catch a hang, not to race the runner.
export default defineConfig({
  test: {
    environment: "jsdom",
    setupFiles: ["src/test/setup.ts"],
    globals: false,
    testTimeout: 15_000,
    // Every date in Planning is a calendar day in the installation's business
    // zone, never the browser's. Pinning the suite to a zone that is neither
    // UTC nor Oslo keeps a date that slides by one from passing on somebody's
    // laptop.
    env: { TZ: "America/New_York" },
  },
});
