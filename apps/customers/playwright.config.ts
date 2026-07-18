import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false,
  workers: 1,
  reporter: [["list"]],
  use: {
    baseURL: "http://localhost:10012",
    trace: "on-first-retry",
  },
  webServer: {
    command: "pnpm run build && pnpm exec tsx e2e/start-server.ts",
    url: "http://localhost:10012/sign-in",
    reuseExistingServer: false,
    timeout: 180_000,
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
});
