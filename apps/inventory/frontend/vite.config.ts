/// <reference types="vitest/config" />
import { defineConfig } from "vite";
// testTimeout: the line editor's tests drive a memory router, Mantine's
// comboboxes and several inputs; on a four-core CI runner that crosses
// Vitest's five-second default under load. The limit exists to catch a hang,
// not to race the runner.
export default defineConfig({
  test: {
    environment: "jsdom",
    setupFiles: ["src/test/setup.ts"],
    globals: false,
    testTimeout: 15_000,
  },
});
