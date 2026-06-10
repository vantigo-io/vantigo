import { z } from "zod";

export const envSchema = z.object({
  DATABASE_URL: z.string().url("DATABASE_URL must be a valid URL"),
  PORT: z.coerce.number().default(3000),
  NODE_ENV: z
    .enum(["development", "production", "test"])
    .default("development"),
  LOG_LEVEL: z
    .enum(["fatal", "error", "warn", "info", "debug", "trace"])
    .default("info"),
  AUTH_SECRET: z.string().min(1, "AUTH_SECRET is required"),
  AUTH_URL: z.string().url("AUTH_URL must be a valid URL").optional(),
  MIGRATIONS_PATH: z.string().optional(),
  SITE_URL: z.string().url("SITE_URL must be a valid URL"),
  SITE_PATH: z
    .string()
    .startsWith("/", "SITE_PATH must start with '/'")
    .default("/idp"),
  CORS_ALLOWED_ORIGINS: z.string().optional(),
  COOKIE_DOMAIN: z.string().optional(),
});

export type Env = z.infer<typeof envSchema>;

/**
 * Validates process.env for the Docker self-hosted runtime.
 * Prints errors cleanly and exits on failure.
 */
export function getDockerEnv(): Env {
  const result = envSchema.safeParse(process.env);
  if (!result.success) {
    console.error("❌ CRITICAL: Invalid environment variables configuration:");
    const errors = result.error.flatten().fieldErrors;
    for (const [key, messages] of Object.entries(errors)) {
      console.error(`  - ${key}: ${messages?.join(", ")}`);
    }
    process.exit(1);
  }
  return result.data;
}
