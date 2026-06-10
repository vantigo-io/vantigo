import { createVantigoAuth } from "@vantigo/auth";
import type { MiddlewareHandler } from "hono";
import { Hono } from "hono";
import type { AppEnv } from "./types";
import htmlContent from "./web/index.html" with { type: "text" };

/**
 * Creates the IDP Hono application instance.
 * All core application routes are natively mounted under the specified sitePath prefix.
 */
export function createIdpApp(
  sitePath: string,
  middlewares: MiddlewareHandler<AppEnv>[] = [],
) {
  const app = new Hono<AppEnv>().basePath(sitePath);

  // Apply injected middlewares first (e.g. database and logging)
  for (const mw of middlewares) {
    app.use("*", mw);
  }

  app.get("/api/health", (c) => {
    return c.json({ status: "ok", service: "idp" });
  });

  // Better-Auth endpoints resolved dynamically at request time (Ports & Adapters DI)
  app.on(["POST", "GET"], "/api/auth/*", async (c) => {
    const db = c.get("db");
    const secret =
      c.env?.AUTH_SECRET ||
      (typeof process !== "undefined" ? process.env.AUTH_SECRET : undefined);
    const siteUrl =
      c.env?.SITE_URL ||
      (typeof process !== "undefined" ? process.env.SITE_URL : undefined);
    const sitePathVal =
      c.env?.SITE_PATH ||
      (typeof process !== "undefined" ? process.env.SITE_PATH : undefined) ||
      "/idp";
    const authUrl =
      c.env?.AUTH_URL ||
      (typeof process !== "undefined" ? process.env.AUTH_URL : undefined) ||
      `${siteUrl}${sitePathVal}`;

    if (!secret) {
      return c.text("Configuration error: AUTH_SECRET missing", 500);
    }

    const auth = createVantigoAuth({
      db,
      secret,
      baseURL: authUrl,
    });

    return auth.handler(c.req.raw);
  });

  // Serve dynamic index.html for client-side SPA router
  app.get("*", (c) => {
    const prefix = sitePath === "/" ? "" : sitePath;
    const path = c.req.path;

    // Do not intercept API requests or static file requests
    if (path.startsWith(`${prefix}/api/`) || path.includes(".")) {
      return c.notFound();
    }

    // Inject the CSS stylesheet link and JS module script tags dynamically matching sitePath
    const htmlString = htmlContent as unknown as string;
    const dynamicHtml = htmlString
      .replace(
        "</head>",
        `<link rel="stylesheet" href="${prefix}/index.css"></head>`,
      )
      .replace(
        "</body>",
        `<script type="module" src="${prefix}/index.js"></script></body>`,
      );

    return c.html(dynamicHtml);
  });

  return app;
}
