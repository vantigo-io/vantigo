import { createVantigoAuth } from "@vantigo/auth";
import { Hono } from "hono";
import type { AppEnv } from "../types";

export const apiRouter = new Hono<AppEnv>();

apiRouter.get("/health", (c) => {
  return c.json({ status: "ok", service: "idp" });
});

apiRouter.on(["POST", "GET"], "/auth/*", async (c) => {
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
  let authUrl =
    c.env?.AUTH_URL ||
    (typeof process !== "undefined" ? process.env.AUTH_URL : undefined) ||
    `${siteUrl}${sitePathVal}`;

  if (authUrl && !authUrl.endsWith("/api/auth")) {
    authUrl = `${authUrl.replace(/\/$/, "")}/api/auth`;
  }

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
