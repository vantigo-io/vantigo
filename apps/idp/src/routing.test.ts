import { describe, expect, test } from "bun:test";
import { createIdpApp } from "./app";

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
