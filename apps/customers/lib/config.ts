import { z } from "zod";
import "dotenv/config";

const configSchema = z.object({
  VANTIGO_CUSTOMERS_BASE_URL: z.string().default("http://localhost:10010"),
  VANTIGO_CUSTOMERS_DATABASE_URL: z.string(),

  VANTIGO_CUSTOMERS_AUTH_SECRET: z.string(),

  VANTIGO_CUSTOMERS_EMAIL_AND_PASSWORD_ENABLED: z.stringbool().default(false),

  VANTIGO_CUSTOMERS_RATE_LIMIT_ENABLED: z.stringbool().default(true),
});

export type Config = z.infer<typeof configSchema>;

/**
 * Inert placeholders used ONLY while `next build` collects page data.
 * The container image is built without any environment configuration;
 * real values are provided (and strictly validated) when the server starts.
 */
const buildPhasePlaceholders: Config = {
  VANTIGO_CUSTOMERS_BASE_URL: "http://localhost:10010",
  VANTIGO_CUSTOMERS_DATABASE_URL: "postgresql://build:build@localhost:5432/build",
  VANTIGO_CUSTOMERS_AUTH_SECRET: "build-phase-placeholder",
  VANTIGO_CUSTOMERS_EMAIL_AND_PASSWORD_ENABLED: false,
  VANTIGO_CUSTOMERS_RATE_LIMIT_ENABLED: true,
};

let cached: Config | undefined;

function loadConfig(): Config {
  if (cached) return cached;

  if (process.env.NEXT_PHASE === "phase-production-build") {
    const lenient = configSchema.safeParse(process.env);
    cached = lenient.success ? lenient.data : buildPhasePlaceholders;
    return cached;
  }

  const parsed = configSchema.safeParse(process.env);
  if (!parsed.success) {
    throw new Error(
      `❌ Invalid environment configuration:\n${JSON.stringify(z.treeifyError(parsed.error), null, 2)}`,
    );
  }
  cached = parsed.data;
  return cached;
}

/**
 * Runtime configuration. Validation is lazy: it runs on first property
 * access rather than at import, so `next build` needs no environment
 * variables while a booting server still fails fast on invalid config.
 */
export const config: Config = new Proxy({} as Config, {
  get(_target, property) {
    // Ignore well-known symbols and interop probes (e.g. `then`) so that
    // merely importing or awaiting the module never triggers validation.
    if (typeof property !== "string" || !(property in configSchema.shape)) {
      return undefined;
    }
    return loadConfig()[property as keyof Config];
  },
  has(_target, property) {
    return typeof property === "string" && property in configSchema.shape;
  },
  ownKeys() {
    return Object.keys(configSchema.shape);
  },
  getOwnPropertyDescriptor(_target, property) {
    if (typeof property !== "string" || !(property in configSchema.shape)) {
      return undefined;
    }
    return { enumerable: true, configurable: true, writable: false };
  },
});
