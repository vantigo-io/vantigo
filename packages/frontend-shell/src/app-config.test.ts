// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";
import { appConfig } from "./app-config";

afterEach(() => {
  delete window.__VANTIGO_APP__;
});

describe("appConfig modules", () => {
  it("is undefined when the backend injected nothing (Vite dev server)", () => {
    expect(appConfig().modules).toBeUndefined();
  });

  it("is undefined when the injected value is null", () => {
    window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules: null };
    expect(appConfig().modules).toBeUndefined();
  });

  it("passes the injected list through, including an empty one", () => {
    window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules: ["customers", "energy"] };
    expect(appConfig().modules).toEqual(["customers", "energy"]);

    window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules: [] };
    expect(appConfig().modules).toEqual([]);
  });
});
