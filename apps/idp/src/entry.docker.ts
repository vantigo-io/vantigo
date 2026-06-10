import * as fs from "node:fs";
import * as path from "node:path";
import { serve } from "bun";
import { migrate } from "drizzle-orm/postgres-js/migrator";
import type { Context, Next } from "hono";
import { z } from "zod";
import { createIdpApp } from "./app";
import { getDb } from "./db";
import { getDockerEnv } from "./env";
import { logger } from "./logger";
import type { AppEnv } from "./types";

// Check if --migrate argument is passed
const isMigrationMode = process.argv.includes("--migrate");

if (isMigrationMode) {
  // Validate only database configuration for migration mode
  const migrationEnv = z
    .object({
      DATABASE_URL: z.string().url("DATABASE_URL must be a valid URL"),
      MIGRATIONS_PATH: z.string().optional(),
    })
    .parse(process.env);

  const db = getDb(migrationEnv.DATABASE_URL);

  logger.info("Running database migrations (--migrate flag detected)...");

  // Determine if running from a compiled binary or directly via Bun runtime
  const isCompiled =
    !process.execPath.endsWith("/bun") &&
    !process.execPath.endsWith("\\bun") &&
    !process.execPath.endsWith("/bun.exe");

  // Deterministically resolve the migrations folder path
  const migrationsFolder =
    migrationEnv.MIGRATIONS_PATH ||
    (isCompiled
      ? path.join(path.dirname(process.execPath), "db/migrations")
      : path.join(import.meta.dir, "db/migrations"));

  logger.info(`Using migrations folder: ${migrationsFolder}`);
  if (!fs.existsSync(migrationsFolder)) {
    logger.error(
      `CRITICAL: Migrations folder not found at: ${migrationsFolder}`,
    );
    process.exit(1);
  }

  try {
    await migrate(db, { migrationsFolder });
    logger.info("Database migrations applied successfully!");
    process.exit(0);
  } catch (error) {
    logger.error(
      { err: error },
      "CRITICAL: Failed to apply database migrations",
    );
    process.exit(1);
  }
}

// Server mode: Validate environment variables using Zod schema
const env = getDockerEnv();

// Pre-initialize Drizzle client once at startup
const db = getDb(env.DATABASE_URL);

// Middleware definitions (logger and database client injection)
const loggerMiddleware = async (c: Context<AppEnv>, next: Next) => {
  const start = Date.now();
  await next();
  const duration = Date.now() - start;
  logger.info(
    {
      method: c.req.method,
      url: c.req.url,
      status: c.res.status,
      durationMs: duration,
    },
    `${c.req.method} ${c.req.path} - ${c.res.status} (${duration}ms)`,
  );
};

const dbMiddleware = async (c: Context<AppEnv>, next: Next) => {
  c.set("db", db);
  await next();
};

// Create Hono app, injecting middlewares to run before routes are registered
const app = createIdpApp(env.SITE_PATH, [loggerMiddleware, dbMiddleware]);

logger.info(
  `Starting Vantigo IDP (Docker Build) on http://localhost:${env.PORT}`,
);

serve({
  fetch: app.fetch,
  port: env.PORT,
});
