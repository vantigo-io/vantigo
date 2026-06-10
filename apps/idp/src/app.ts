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
