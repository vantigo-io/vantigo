import type { PostgresJsDatabase } from "drizzle-orm/postgres-js";
import { drizzle } from "drizzle-orm/postgres-js";
import postgres from "postgres";
import * as schema from "./schema";

export type Database = PostgresJsDatabase<typeof schema>;

let client: postgres.Sql | null = null;
let db: Database | null = null;

/**
 * Returns a cached Drizzle database instance connected to the specified connection string.
 * This is designed to reuse the database connection pool across requests in both serverless
 * (Cloudflare Workers) and containerized (Docker) environments.
 */
export function getDb(connectionString: string): Database {
  if (!db || !client) {
    // In serverless, keep connection limit low per isolate instance (typically 1).
    // In Docker, we can increase the pool size if needed, but 10 is standard.
    const maxConnections = process.env.DB_MAX_CONNECTIONS
      ? Number.parseInt(process.env.DB_MAX_CONNECTIONS, 10)
      : 10;

    client = postgres(connectionString, {
      max: maxConnections,
      // Enable SSL fallback or options if needed, but Neon connection strings usually include it.
    });
    db = drizzle(client, { schema });
  }
  return db;
}
