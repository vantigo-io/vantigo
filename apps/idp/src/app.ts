import type { MiddlewareHandler } from "hono";
import { Hono } from "hono";
import { apiRouter } from "./api/routes";
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

  // Mount API router (health check & Better-Auth routes)
  app.route("/api", apiRouter);

  // Serve dynamic index.html for client-side SPA router
  app.get("*", async (c, next) => {
    const prefix = sitePath === "/" ? "" : sitePath;
    const path = c.req.path;

    // Do not intercept API requests or static file requests (let downstream handlers/middlewares resolve them)
    if (path.startsWith(`${prefix}/api/`) || path.includes(".")) {
      await next();
      return;
    }

    // Resolve dynamic site configuration from bindings or environment
    const siteUrlVal =
      c.env?.SITE_URL ||
      (typeof process !== "undefined" ? process.env.SITE_URL : undefined) ||
      c.req.url;
    const sitePathVal =
      c.env?.SITE_PATH ||
      (typeof process !== "undefined" ? process.env.SITE_PATH : undefined) ||
      sitePath ||
      "/idp";

    let origin = "http://localhost:3000";
    try {
      origin = new URL(siteUrlVal).origin;
    } catch (_) {}

    const configScript = `<script>
      window.__VANTIGO_CONFIG__ = {
        siteUrl: ${JSON.stringify(origin)},
        sitePath: ${JSON.stringify(sitePathVal)}
      };
    </script>`;

    // Inject the config script, CSS stylesheet link, and JS module script tags dynamically matching sitePath
    const htmlString = htmlContent as unknown as string;
    const dynamicHtml = htmlString
      .replace(
        "</head>",
        `${configScript}<link rel="stylesheet" href="${prefix}/index.css"></head>`,
      )
      .replace(
        "</body>",
        `<script type="module" src="${prefix}/index.js"></script></body>`,
      );

    return c.html(dynamicHtml);
  });

  return app;
}
