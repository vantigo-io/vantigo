import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const REQUIRED_ENV = {
  VANTIGO_CUSTOMERS_DATABASE_URL: "postgresql://test:test@localhost:5432/test",
  VANTIGO_CUSTOMERS_AUTH_SECRET: "test-secret",
};

const MANAGED_KEYS = [
  "VANTIGO_CUSTOMERS_DATABASE_URL",
  "VANTIGO_CUSTOMERS_AUTH_SECRET",
  "VANTIGO_CUSTOMERS_BASE_URL",
  "VANTIGO_CUSTOMERS_EMAIL_AND_PASSWORD_ENABLED",
  "VANTIGO_CUSTOMERS_RATE_LIMIT_ENABLED",
  "NEXT_PHASE",
] as const;

async function loadConfig(env: Record<string, string>) {
  for (const key of MANAGED_KEYS) {
    vi.stubEnv(key, undefined as unknown as string);
    delete process.env[key];
  }
  for (const [key, value] of Object.entries(env)) {
    vi.stubEnv(key, value);
  }
  vi.resetModules();
  const module = await import("./config");
  return module.config;
}

describe("config", () => {
  beforeEach(() => {
    vi.resetModules();
  });

  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it("parses valid configuration and applies defaults", async () => {
    const config = await loadConfig(REQUIRED_ENV);
    expect(config.VANTIGO_CUSTOMERS_DATABASE_URL).toBe(REQUIRED_ENV.VANTIGO_CUSTOMERS_DATABASE_URL);
    expect(config.VANTIGO_CUSTOMERS_AUTH_SECRET).toBe(REQUIRED_ENV.VANTIGO_CUSTOMERS_AUTH_SECRET);
    expect(config.VANTIGO_CUSTOMERS_BASE_URL).toBe("http://localhost:10010");
    expect(config.VANTIGO_CUSTOMERS_EMAIL_AND_PASSWORD_ENABLED).toBe(false);
    expect(config.VANTIGO_CUSTOMERS_RATE_LIMIT_ENABLED).toBe(true);
  });

  it("does not validate at import time", async () => {
    // Importing with a broken environment must not throw...
    await expect(loadConfig({})).resolves.toBeDefined();
  });

  it("throws on first access when DATABASE_URL is missing", async () => {
    const config = await loadConfig({ VANTIGO_CUSTOMERS_AUTH_SECRET: "test-secret" });
    expect(() => config.VANTIGO_CUSTOMERS_DATABASE_URL).toThrow(
      /Invalid environment configuration/,
    );
  });

  it("throws on first access when AUTH_SECRET is missing", async () => {
    const config = await loadConfig({
      VANTIGO_CUSTOMERS_DATABASE_URL: REQUIRED_ENV.VANTIGO_CUSTOMERS_DATABASE_URL,
    });
    expect(() => config.VANTIGO_CUSTOMERS_AUTH_SECRET).toThrow(/Invalid environment configuration/);
  });

  it("uses placeholders instead of throwing during next build", async () => {
    const config = await loadConfig({ NEXT_PHASE: "phase-production-build" });
    expect(config.VANTIGO_CUSTOMERS_AUTH_SECRET).toBe("build-phase-placeholder");
    expect(config.VANTIGO_CUSTOMERS_BASE_URL).toBe("http://localhost:10010");
  });

  it("prefers real environment over placeholders during next build", async () => {
    const config = await loadConfig({
      ...REQUIRED_ENV,
      NEXT_PHASE: "phase-production-build",
    });
    expect(config.VANTIGO_CUSTOMERS_AUTH_SECRET).toBe("test-secret");
  });

  it("coerces EMAIL_AND_PASSWORD_ENABLED=true", async () => {
    const config = await loadConfig({
      ...REQUIRED_ENV,
      VANTIGO_CUSTOMERS_EMAIL_AND_PASSWORD_ENABLED: "true",
    });
    expect(config.VANTIGO_CUSTOMERS_EMAIL_AND_PASSWORD_ENABLED).toBe(true);
  });

  it("coerces EMAIL_AND_PASSWORD_ENABLED=false", async () => {
    const config = await loadConfig({
      ...REQUIRED_ENV,
      VANTIGO_CUSTOMERS_EMAIL_AND_PASSWORD_ENABLED: "false",
    });
    expect(config.VANTIGO_CUSTOMERS_EMAIL_AND_PASSWORD_ENABLED).toBe(false);
  });
});
