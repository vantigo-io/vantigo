import { spawn, spawnSync } from "node:child_process";
import { writeFileSync } from "node:fs";
import path from "node:path";
import { PostgreSqlContainer } from "@testcontainers/postgresql";

/**
 * Launched by Playwright's webServer (which starts BEFORE globalSetup,
 * so the database container is owned here rather than in a setup hook).
 *
 * Starts a fresh throwaway Postgres container, applies migrations, writes
 * the connection URL to e2e/.state.json (consumed by helpers.ts) and runs
 * `next start`. The container is stopped when Playwright shuts the server down.
 */
const appDir = path.join(__dirname, "..");
const stateFile = path.join(__dirname, ".state.json");

// Colima/Lima compatibility: the docker socket lives at /var/run/docker.sock
// INSIDE the VM, but the client socket path on the host differs. Without this,
// testcontainers' reaper fails to mount the socket. Harmless on Docker Desktop.
process.env.TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE ??= "/var/run/docker.sock";

async function main() {
  console.log("[e2e] Starting Postgres testcontainer...");
  const container = await new PostgreSqlContainer("postgres:18")
    .withDatabase("vantigo_e2e")
    .start();
  const databaseUrl = container.getConnectionUri();

  console.log("[e2e] Running migrations...");
  const migrate = spawnSync("./node_modules/.bin/drizzle-kit", ["migrate"], {
    cwd: appDir,
    env: { ...process.env, VANTIGO_CUSTOMERS_DATABASE_URL: databaseUrl },
    stdio: "inherit",
  });
  if (migrate.status !== 0) {
    await container.stop();
    throw new Error(`drizzle-kit migrate failed with exit code ${migrate.status}`);
  }

  writeFileSync(stateFile, JSON.stringify({ databaseUrl }, null, 2));

  console.log("[e2e] Starting Next.js server...");
  const server = spawn("./node_modules/.bin/next", ["start", "-p", "10012"], {
    cwd: appDir,
    env: {
      ...process.env,
      VANTIGO_CUSTOMERS_DATABASE_URL: databaseUrl,
      VANTIGO_CUSTOMERS_AUTH_SECRET: "e2e-test-secret-at-least-32-chars!!",
      VANTIGO_CUSTOMERS_BASE_URL: "http://localhost:10012",
      VANTIGO_CUSTOMERS_EMAIL_AND_PASSWORD_ENABLED: "true",
      VANTIGO_CUSTOMERS_RATE_LIMIT_ENABLED: "false",
    },
    stdio: "inherit",
  });

  let shuttingDown = false;
  const shutdown = async (code: number) => {
    if (shuttingDown) return;
    shuttingDown = true;
    console.log("[e2e] Stopping Postgres testcontainer...");
    server.kill("SIGTERM");
    await container.stop();
    process.exit(code);
  };

  server.on("exit", (code) => void shutdown(code ?? 1));
  for (const signal of ["SIGINT", "SIGTERM"] as const) {
    process.on(signal, () => void shutdown(0));
  }
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
