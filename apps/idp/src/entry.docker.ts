import * as fs from "node:fs";
import * as path from "node:path";
import { serve } from "bun";
import { migrate } from "drizzle-orm/postgres-js/migrator";
import { app } from "./app";
import { getDb } from "./db";

// Ensure DATABASE_URL is set in self-hosted environment
const databaseUrl = Bun.env.DATABASE_URL;
if (!databaseUrl) {
  console.error("CRITICAL: DATABASE_URL environment variable is required!");
  process.exit(1);
}

// Pre-initialize Drizzle client once at startup
const db = getDb(databaseUrl);

// Check if --migrate argument is passed
if (Bun.argv.includes("--migrate")) {
  console.log("Running database migrations (--migrate flag detected)...");

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

  console.log(`Using migrations folder: ${migrationsFolder}`);
  if (!migrationsFolder) {
    console.error("CRITICAL: Migrations folder not found! Searched in:");
    for (const p of possiblePaths) {
      console.error(`  - ${p}`);
    }
    process.exit(1);
  }

  try {
    await migrate(db, { migrationsFolder });
    console.log("Database migrations applied successfully!");
    process.exit(0);
  } catch (error) {
    console.error("CRITICAL: Failed to apply database migrations:", error);
    process.exit(1);
  }
}

// Default mode: serve the API and frontend
app.use("*", async (c, next) => {
  c.set("db", db);
  await next();
});

const port = Bun.env.PORT || 3000;
console.log(`Starting Vantigo IDP (Docker Build) on http://localhost:${port}`);

serve({
  fetch: app.fetch,
  port,
});
