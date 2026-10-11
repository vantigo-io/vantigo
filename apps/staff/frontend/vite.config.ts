/// <reference types="vitest/config" />
import { defineConfig } from "vite";
// testTimeout: New person mounts a memory router, Mantine's modal and a
// stepper, and its tests drive a dozen inputs across two steps; on a
// four-core CI runner that crosses Vitest's five-second default under load
// (Expenses' form did). The limit exists to catch a hang, not to race the
// runner.
export default defineConfig({
  test: {
    environment: "jsdom",
    setupFiles: ["src/test/setup.ts"],
    globals: false,
    testTimeout: 15_000,
    // Every date in Staff is a calendar day in the installation's business
    // zone, never the browser's. Pinning the suite to a zone that is neither
    // UTC nor Oslo keeps a date that slides by one from passing on somebody's
    // laptop.
    env: { TZ: "America/New_York" },
  },
});
