import { afterEach, describe, expect, it } from "vitest";
import { moduleKeys } from "../navigation";
import { enabledModuleKeys } from "./enabled-modules";

const inject = (modules: string[] | null | undefined) => {
  window.__VANTIGO_APP__ = { basePath: "/", title: "Vantigo", support: {}, modules };
};

afterEach(() => {
  delete window.__VANTIGO_APP__;
});

describe("enabledModuleKeys", () => {
  it("treats a missing injection as every known module (dev server)", () => {
    expect(enabledModuleKeys()).toEqual(moduleKeys);
    inject(null);
    expect(enabledModuleKeys()).toEqual(moduleKeys);
  });

  it("keeps only the injected modules, in catalog order", () => {
    inject(["energy", "customers"]);
    expect(enabledModuleKeys()).toEqual(["customers", "energy"]);
  });

  it("ignores names the frontend does not know", () => {
    inject(["identity", "billing", "products"]);
    expect(enabledModuleKeys()).toEqual(["products"]);
  });

  it("treats an injected empty list as nothing enabled", () => {
    inject([]);
    expect(enabledModuleKeys()).toEqual([]);
  });
});
