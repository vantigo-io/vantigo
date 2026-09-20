/// <reference types="vitest/config" />
import { defineConfig } from "vite";
// testTimeout: the expense form mounts a memory router, Mantine's modal and a
// dropzone, and its tests drive a dozen inputs each; on a four-core CI runner
// that has crossed Vitest's five-second default under load. The limit exists
// to catch a hang, not to race the runner.
export default defineConfig({
  test: {
    environment: "jsdom",
    setupFiles: ["src/test/setup.ts"],
    globals: false,
    testTimeout: 15_000,
    // The installation's business time zone is never the browser's, and the
    // travel claim page's whole job is to keep the two apart. Pinning the
    // suite to a zone that is neither UTC nor Oslo means the offset
    // assertions cannot quietly become tautologies on somebody's laptop.
    env: { TZ: "America/New_York" },
  },
});
