/// <reference types="vitest/config" />
import { defineConfig } from "vite";
// testTimeout: the invoice editor mounts Mantine's modals, date inputs and a
// line table driven by a dozen inputs a test; on a four-core CI runner that has
// crossed Vitest's five-second default under load elsewhere in the repo. The
// limit exists to catch a hang, not to race the runner.
export default defineConfig({
  test: {
    environment: "jsdom",
    setupFiles: ["src/test/setup.ts"],
    globals: false,
    testTimeout: 15_000,
    // "Today" is the server's Oslo business day (GET /meta), never the
    // browser's. Pinning the suite to a zone that is neither UTC nor Oslo means
    // a test that quietly read the browser's date would fail here.
    env: { TZ: "America/New_York" },
  },
});
