import * as fs from "node:fs";
import * as path from "node:path";
import { serve } from "bun";
import { migrate } from "drizzle-orm/postgres-js/migrator";
import { app } from "./app";
import { getDb } from "./db";
import { getDockerEnv } from "./env";
import { logger } from "./logger";

// Validate environment variables using Zod schema
const env = getDockerEnv();

// Pre-initialize Drizzle client once at startup
const db = getDb(env.DATABASE_URL);

// Check if --migrate argument is passed
if (Bun.argv.includes("--migrate")) {
  logger.info("Running database migrations (--migrate flag detected)...");

  // Resolve migrations folder path (support monorepo root, app root, and Docker runtimes)
  const possiblePaths = [
    path.resolve(process.cwd(), "apps/idp/src/db/migrations"),
    path.resolve(process.cwd(), "src/db/migrations"),
    path.resolve(process.cwd(), "db/migrations"),
  ];

  let migrationsFolder = "";
  for (const p of possiblePaths) {
    if (fs.existsSync(p)) {
      migrationsFolder = p;
      break;
    }
  }

  logger.info(`Using migrations folder: ${migrationsFolder}`);
  if (!migrationsFolder) {
    logger.error("CRITICAL: Migrations folder not found! Searched in:");
    for (const p of possiblePaths) {
      logger.error(`  - ${p}`);
    }
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

// Request logging middleware
app.use("*", async (c, next) => {
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
});

// Default mode: serve the API and frontend
app.use("*", async (c, next) => {
  c.set("db", db);
  await next();
});

logger.info(
  `Starting Vantigo IDP (Docker Build) on http://localhost:${env.PORT}`,
);

serve({
  fetch: app.fetch,
  port: env.PORT,
});
