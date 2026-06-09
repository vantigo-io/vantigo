import { Hono } from "hono";
import type { AppEnv } from "./types";

// This is the core application logic. It has NO knowledge of Cloudflare or Docker.
export const app = new Hono<AppEnv>();

app.get("/api/health", (c) => {
  return c.json({ status: "ok", service: "idp" });
});

// We will add Better-Auth routes and Frontend static serving here soon.
