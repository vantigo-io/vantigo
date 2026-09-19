import { defineConfig } from "vitest/config";

// testTimeout: Vitest's five-second default has flaked on slow, loaded CI
// runners. The limit exists to catch a hang, not to race the runner.
export default defineConfig({
  test: { testTimeout: 15_000 },
});
