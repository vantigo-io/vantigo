import { Hono } from "hono";
import type { AppEnv } from "./types";

/**
 * Creates the IDP Hono application instance.
 * All core application routes are natively mounted under the specified sitePath prefix.
 */
export function createIdpApp(sitePath: string) {
  const app = new Hono<AppEnv>().basePath(sitePath);

  app.get("/api/health", (c) => {
    return c.json({ status: "ok", service: "idp" });
  });

  // Better-Auth endpoints and other sub-routers will be mounted here

  return app;
}
