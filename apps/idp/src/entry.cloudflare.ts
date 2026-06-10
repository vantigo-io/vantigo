import type { Context, Hono, Next } from "hono";
import { createIdpApp } from "./app";
import { getDb } from "./db";
import type { AppEnv } from "./types";

let appSingleton: Hono<AppEnv> | null = null;

export default {
  // biome-ignore lint/suspicious/noExplicitAny: env bindings and execution context are dynamic at runtime
  async fetch(request: Request, env: any, ctx: any) {
    if (!appSingleton) {
      const sitePath = env.SITE_PATH || "/idp";

      // Middleware to inject database client into Hono context
      const dbMiddleware = async (c: Context<AppEnv>, next: Next) => {
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
      };

      appSingleton = createIdpApp(sitePath, [dbMiddleware]);

      // Route static assets matching the SITE_PATH prefix through the Workers Assets binding
      const serveCloudflareAsset =
        (filename: string) => async (c: Context<AppEnv>) => {
          if (c.env.ASSETS) {
            const url = new URL(c.req.url);
            url.pathname = `/${filename}`;
            return c.env.ASSETS.fetch(new Request(url.toString(), c.req.raw));
          }
          return c.text("ASSETS binding missing", 500);
        };

      appSingleton.get("/index.js", serveCloudflareAsset("index.js"));
      appSingleton.get("/index.css", serveCloudflareAsset("index.css"));
      appSingleton.get("/logo.png", serveCloudflareAsset("logo.png"));
      appSingleton.get("/favicon.png", serveCloudflareAsset("favicon.png"));
    }

    return appSingleton.fetch(request, env, ctx);
  },
};
