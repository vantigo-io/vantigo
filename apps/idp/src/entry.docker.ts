import { serve } from "bun";
import { app } from "./app";
import { getDb } from "./db";

// Ensure DATABASE_URL is set in self-hosted environment
const databaseUrl = process.env.DATABASE_URL;
if (!databaseUrl) {
  console.error("CRITICAL: DATABASE_URL environment variable is required!");
  process.exit(1);
}

// Pre-initialize Drizzle client once at startup
const db = getDb(databaseUrl);

app.use("*", async (c, next) => {
  c.set("db", db);
  await next();
});

const port = process.env.PORT || 3000;
console.log(`Starting Vantigo IDP (Docker Build) on http://localhost:${port}`);

serve({
  fetch: app.fetch,
  port,
});
