import { describe, expect, test } from "bun:test";
import * as fs from "node:fs";
import * as path from "node:path";
import { drizzle } from "drizzle-orm/postgres-js";
import { migrate } from "drizzle-orm/postgres-js/migrator";
import postgres from "postgres";
import { GenericContainer, Wait } from "testcontainers";

// Opt-in check
const shouldSkip = Bun.env.TEST_MIGRATIONS !== "true";

describe("Database Migrations", () => {
  test.skipIf(shouldSkip)(
    "successfully apply all migrations programmatically",
    async () => {
      console.log("Starting PostgreSQL testcontainer...");
      const container = await new GenericContainer("postgres:16-alpine")
        .withExposedPorts(5432)
        .withEnvironment({
          POSTGRES_USER: "postgres",
          POSTGRES_PASSWORD: "password",
          POSTGRES_DB: "vantigo_test",
        })
        .withWaitStrategy(
          Wait.forLogMessage(
            /.*database system is ready to accept connections.*/,
            2,
          ),
        )
        .start();

      console.log("PostgreSQL testcontainer started.");

      let sql: postgres.Sql | null = null;

      try {
        const host = container.getHost();
        const port = container.getMappedPort(5432);
        const connectionString = `postgres://postgres:password@${host}:${port}/vantigo_test`;

        // Resilient connection retry loop to handle startup transitions
        let retries = 10;
        let db = null;
        while (retries > 0) {
          try {
            sql = postgres(connectionString, { max: 1 });
            db = drizzle(sql);
            await sql`SELECT 1`;
            break; // Successfully connected and queried
          } catch (err) {
            console.log(
              `Database connection/query failed, retrying... (retries left: ${retries})`,
            );
            if (sql) {
              await sql.end();
              sql = null;
            }
            retries--;
            if (retries === 0) {
              throw err;
            }
            await new Promise((resolve) => setTimeout(resolve, 1000));
          }
        }

        if (!db || !sql) {
          throw new Error("Failed to initialize database client");
        }

        // Resolve the path to the migrations folder
        const migrationsFolder = path.resolve(__dirname, "./migrations");
        console.log(`Running migrations from: ${migrationsFolder}`);

        await migrate(db, { migrationsFolder });
        console.log("Migrations applied successfully.");

        // Verify a basic query against public tables to ensure schema is active
        const result =
          await sql`SELECT tablename FROM pg_tables WHERE schemaname = 'public'`;
        const tableNames = result.map((row) => row.tablename);
        console.log("Tables created:", tableNames);

        expect(tableNames).toContain("users");
        expect(tableNames).toContain("sessions");
        expect(tableNames).toContain("accounts");
        expect(tableNames).toContain("verifications");

        await sql.end();
        sql = null;
      } finally {
        if (sql) {
          await sql.end();
        }
        console.log("Stopping PostgreSQL testcontainer...");
        await container.stop();
        console.log("PostgreSQL testcontainer stopped.");
      }
    },
    60000,
  ); // 60 seconds timeout

  test.skipIf(shouldSkip)(
    "compiled binary successfully applies migrations when invoked with --migrate",
    async () => {
      const binaryPath = path.resolve(
        __dirname,
        "../../../../dist/vantigo-idp",
      );
      if (!fs.existsSync(binaryPath)) {
        console.warn(
          `Compiled binary not found at ${binaryPath}. Skipping compiled binary test.`,
        );
        return;
      }

      console.log("Starting PostgreSQL testcontainer...");
      const container = await new GenericContainer("postgres:16-alpine")
        .withExposedPorts(5432)
        .withEnvironment({
          POSTGRES_USER: "postgres",
          POSTGRES_PASSWORD: "password",
          POSTGRES_DB: "vantigo_test",
        })
        .withWaitStrategy(
          Wait.forLogMessage(
            /.*database system is ready to accept connections.*/,
            2,
          ),
        )
        .start();

      console.log("PostgreSQL testcontainer started.");

      try {
        const host = container.getHost();
        const port = container.getMappedPort(5432);
        const connectionString = `postgres://postgres:password@${host}:${port}/vantigo_test`;

        console.log(
          `Running compiled binary at ${binaryPath} with --migrate...`,
        );
        const proc = Bun.spawn([binaryPath, "--migrate"], {
          env: {
            ...process.env,
            DATABASE_URL: connectionString,
            AUTH_SECRET: "dummy_secret_for_validation_pass",
            SITE_URL: "http://localhost:3000",
            MIGRATIONS_PATH: path.resolve(__dirname, "./migrations"),
          },
        });

        const exitCode = await proc.exited;
        const stdout = await new Response(proc.stdout).text();
        const stderr = await new Response(proc.stderr).text();

        console.log("Subprocess exited with code:", exitCode);
        console.log("Subprocess stdout:", stdout);
        console.log("Subprocess stderr:", stderr);

        expect(exitCode).toBe(0);
        expect(stdout).toContain("Database migrations applied successfully!");

        // Verify that tables were actually created in the DB
        const sql = postgres(connectionString, { max: 1 });
        const result =
          await sql`SELECT tablename FROM pg_tables WHERE schemaname = 'public'`;
        const tableNames = result.map((row) => row.tablename);
        console.log("Tables created via binary:", tableNames);

        expect(tableNames).toContain("users");
        expect(tableNames).toContain("sessions");
        expect(tableNames).toContain("accounts");
        expect(tableNames).toContain("verifications");

        await sql.end();
      } finally {
        console.log("Stopping PostgreSQL testcontainer...");
        await container.stop();
        console.log("PostgreSQL testcontainer stopped.");
      }
    },
    60000,
  ); // 60 seconds timeout
});
