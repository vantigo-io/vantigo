import { describe, expect, it } from "vitest";
import { activeAppKey, allNavSections, appForKey, apps, isAppEnabled, switcherTiles } from "./apps";

const allModules = ["communications", "customers", "energy", "products"] as const;

describe("the app registry", () => {
  it("lists Home first, without a module, and every module app once", () => {
    expect(apps[0]).toMatchObject({ key: "home", home: "/dashboard", navSections: [] });
    expect(apps[0]?.module).toBeUndefined();
    expect(apps.map((app) => app.key)).toEqual(["home", "customers", "communications", "products", "energy"]);
    for (const app of apps.slice(1)) expect(app.module).toBe(app.key);
  });

  it("keeps every sidebar destination under its app's URL prefix", () => {
    for (const app of apps) {
      const prefix = `/${app.key}`;
      for (const item of app.navSections.flatMap((section) => section.items)) {
        expect(item.to === prefix || item.to.startsWith(`${prefix}/`)).toBe(true);
        expect(item.module).toBe(app.module);
      }
    }
  });

  it("addresses every destination by a bare path", () => {
    const paths = allNavSections.flatMap((section) => section.items.map((item) => item.to));
    for (const path of paths) expect(path).toMatch(/^\/[a-z-]+(\/[a-z-]+)*$/);
    expect(paths).toEqual([
      "/customers",
      "/customers/contacts",
      "/communications/inbox",
      "/communications/channels",
      "/communications/suppressions",
      "/products",
      "/products/categories",
      "/energy/metering-points",
    ]);
  });

  it("requires, for a tile, any permission that unlocks one of its sidebar entries", () => {
    expect(appForKey("customers").requiredPermissions).toEqual([
      "customers:view",
      "customers:contacts-view",
      "customers:associations-view",
    ]);
    expect(appForKey("home").requiredPermissions).toBeUndefined();
  });

  it("throws for an unknown key instead of returning undefined", () => {
    expect(() => appForKey("billing" as never)).toThrow(/billing/);
  });

  it("treats Home as always enabled and module apps as enabled when their module is", () => {
    expect(isAppEnabled(appForKey("home"), [])).toBe(true);
    expect(isAppEnabled(appForKey("energy"), ["customers"])).toBe(false);
    expect(isAppEnabled(appForKey("energy"), ["customers", "energy"])).toBe(true);
  });
});

describe("activeAppKey", () => {
  it("takes the deepest match that carries an app", () => {
    expect(activeAppKey([{ staticData: {} }, { staticData: { app: "customers" } }, { staticData: {} }])).toBe(
      "customers",
    );
  });

  it("is undefined when no match carries an app (administration and public paths)", () => {
    expect(activeAppKey([{ staticData: {} }, { staticData: {} }])).toBeUndefined();
    expect(activeAppKey([])).toBeUndefined();
  });
});

describe("switcherTiles", () => {
  it("hides apps the user has no permission for, keeps Home, marks current and disabled", () => {
    const tiles = switcherTiles(["customers:view"], ["customers", "products"], "customers");
    expect(tiles.map((tile) => tile.app.key)).toEqual(["home", "customers"]);
    expect(tiles.map((tile) => tile.current)).toEqual([false, true]);
    expect(tiles.map((tile) => tile.enabled)).toEqual([true, true]);
  });

  it("shows a disabled module the user could otherwise use, muted rather than hidden", () => {
    const tiles = switcherTiles(["*"], ["customers"], undefined);
    expect(tiles.map((tile) => [tile.app.key, tile.enabled])).toEqual([
      ["home", true],
      ["customers", true],
      ["communications", false],
      ["products", false],
      ["energy", false],
    ]);
    expect(tiles.some((tile) => tile.current)).toBe(false);
  });

  it("shows only Home while permissions are still loading", () => {
    expect(switcherTiles(undefined, allModules, "home").map((tile) => tile.app.key)).toEqual(["home"]);
  });
});
