import { describe, expect, test } from "bun:test";
import type { Context, Next } from "hono";
import { createIdpApp } from "./app";
import type { Database } from "./db";
import type { AppEnv } from "./types";

describe("Dynamic Prefix Routing", () => {
  test("routes correctly with default /idp prefix", async () => {
    const app = createIdpApp("/idp");

    // Request with correct prefix should match and succeed
    const res = await app.request("/idp/api/health");
    expect(res.status).toBe(200);
    const body = await res.json();
    expect(body).toEqual({ status: "ok", service: "idp" });

    // Request without prefix should return 404 Not Found
    const resMissing = await app.request("/api/health");
    expect(resMissing.status).toBe(404);
  });

  test("routes correctly with custom path prefix", async () => {
    const app = createIdpApp("/custom-auth");

    // Request with custom prefix should match and succeed
    const res = await app.request("/custom-auth/api/health");
    expect(res.status).toBe(200);
    const body = await res.json();
    expect(body).toEqual({ status: "ok", service: "idp" });

    // Request with old prefix should return 404 Not Found
    const resOld = await app.request("/idp/api/health");
    expect(resOld.status).toBe(404);
  });

  test("routes correctly with root prefix", async () => {
    const app = createIdpApp("/");

    // Request at root should match and succeed
    const res = await app.request("/api/health");
    expect(res.status).toBe(200);
    const body = await res.json();
    expect(body).toEqual({ status: "ok", service: "idp" });
  });
});

describe("Frontend and Authentication Routing", () => {
  test("serves dynamic index.html fallback for client-side routes with prefix injection", async () => {
    const app = createIdpApp("/idp");

    // Requesting client-side route should return 200 HTML
    const res = await app.request("/idp/login");
    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toContain("text/html");

    const html = await res.text();
    expect(html).toContain(
      '<script type="module" src="/idp/index.js"></script>',
    );
    expect(html).toContain('<link rel="stylesheet" href="/idp/index.css">');
  });

  test("serves dynamic index.html fallback with correct root paths", async () => {
    const app = createIdpApp("/");

    const res = await app.request("/login");
    expect(res.status).toBe(200);
    expect(res.headers.get("content-type")).toContain("text/html");

    const html = await res.text();
    expect(html).toContain('<script type="module" src="/index.js"></script>');
    expect(html).toContain('<link rel="stylesheet" href="/index.css">');
  });

  test("returns 404 for nonexistent API routes and does not fall back to html", async () => {
    const app = createIdpApp("/idp");

    const res = await app.request("/idp/api/nonexistent");
    expect(res.status).toBe(404);
  });

  test("routes Better-Auth ok endpoint without crashing", async () => {
    // Inject a dummy db client via middleware
    const dummyDb = {} as unknown as Database;
    const dbMiddleware = async (c: Context<AppEnv>, next: Next) => {
      c.set("db", dummyDb);
      await next();
    };
    const app = createIdpApp("/idp", [dbMiddleware]);

    // Set mock env variables so configuration checks pass
    process.env.AUTH_SECRET =
      "dummy_secret_for_tests_must_be_at_least_32_characters_long";
    process.env.SITE_URL = "http://localhost:3000";

    const res = await app.request("http://localhost:3000/idp/api/auth/ok");
    expect(res.status).toBe(200);
    const body = await res.json();
    expect(body).toEqual({ ok: true });
  });
});
