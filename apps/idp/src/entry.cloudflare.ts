import { app } from "./app";
import { getDb } from "./db";

// Injects Cloudflare native bindings and services into Hono context
app.use("*", async (c, next) => {
  const connectionString =
    c.env.HYPERDRIVE?.connectionString || c.env.DATABASE_URL;
  if (!connectionString) {
    return c.text(
      "Configuration error: DATABASE_URL or HYPERDRIVE missing",
      500,
    );
  }

  const db = getDb(connectionString);
  c.set("db", db);

  await next();
});

export default app;
