/// <reference types="vitest/config" />
import { defineConfig } from "vite";
// testTimeout: FullCalendar measures and lays out its grid on mount, and the
// suite mounts it under Mantine; the limit exists to catch a hang, not to race
// a loaded CI runner.
export default defineConfig({
  test: {
    environment: "jsdom",
    setupFiles: ["src/test/setup.ts"],
    globals: false,
    testTimeout: 15_000,
    // A calendar is all about which day an instant falls on. Pinning the suite
    // to a zone that is neither UTC nor Oslo means a test that confuses the
    // viewer's zone with the machine's fails everywhere, not only on a laptop
    // abroad.
    env: { TZ: "America/New_York" },
  },
});
