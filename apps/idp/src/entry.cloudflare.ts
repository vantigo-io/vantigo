import type { Hono } from "hono";
import { createIdpApp } from "./app";
import { getDb } from "./db";
import type { AppEnv } from "./types";

let appSingleton: Hono<AppEnv> | null = null;

export default {
  // biome-ignore lint/suspicious/noExplicitAny: env bindings and execution context are dynamic at runtime
  async fetch(request: Request, env: any, ctx: any) {
    if (!appSingleton) {
      const sitePath = env.SITE_PATH || "/idp";
      appSingleton = createIdpApp(sitePath);

      // Injects Cloudflare native bindings and services into Hono context
      appSingleton.use("*", async (c, next) => {
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
    }

    return appSingleton.fetch(request, env, ctx);
  },
};
