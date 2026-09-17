import { describe, expect, it } from "vitest";
import {
  activeAppKey,
  allNavSections,
  appForKey,
  appNavSections,
  apps,
  appTitleLabel,
  areaForKey,
  areas,
  isAppEnabled,
  switcherTiles,
} from "./apps";

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
      "/products/tax-categories",
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

describe("the administration areas", () => {
  const visibility = { permissions: [], isOwner: false, canManageAuthorization: false, enabledModules: allModules };

  it("declares settings, workspace and admin, none of them a switcher tile", () => {
    expect(areas.map((area) => area.key)).toEqual(["settings", "workspace", "admin"]);
    const tiles = switcherTiles(["*"], allModules, "settings");
    expect(tiles.map((tile) => tile.app.key)).toEqual(["home", "customers", "communications", "products", "energy"]);
    expect(tiles.some((tile) => tile.current)).toBe(false);
  });

  it("keeps every area destination under the area's URL prefix", () => {
    for (const area of areas) {
      const prefix = `/${area.key}`;
      for (const item of area.navSections.flatMap((section) => section.items)) {
        expect(item.to === prefix || item.to.startsWith(`${prefix}/`)).toBe(true);
        expect(item.module).toBeUndefined();
      }
    }
  });

  it("resolves an area or an app by key, and throws for anything else", () => {
    expect(areaForKey("workspace")).toBe(areas[1]);
    expect(areaForKey("customers")).toBe(appForKey("customers"));
    expect(() => areaForKey("billing" as never)).toThrow(/billing/);
  });

  it("is always enabled: an area has no module to turn off", () => {
    expect(isAppEnabled(areaForKey("settings"), [])).toBe(true);
  });

  it("shows the whole workspace area to an Owner and only roles to an authorization manager", () => {
    const paths = (sections: ReturnType<typeof appNavSections>) =>
      sections.flatMap((section) => section.items.map((item) => item.to));
    expect(paths(appNavSections(areaForKey("workspace"), allModules, { ...visibility, isOwner: true }))).toEqual([
      "/workspace/overview",
      "/workspace/users",
      "/workspace/invitations",
    ]);
    expect(
      paths(appNavSections(areaForKey("workspace"), allModules, { ...visibility, canManageAuthorization: true })),
    ).toEqual(["/workspace/roles"]);
    expect(paths(appNavSections(areaForKey("workspace"), allModules, visibility))).toEqual([]);
  });

  it("shows the settings pages to everyone and gives system admin no sidebar", () => {
    expect(
      appNavSections(areaForKey("settings"), allModules, visibility).flatMap((section) =>
        section.items.map((item) => item.to),
      ),
    ).toEqual(["/settings/profile", "/settings/security"]);
    expect(appNavSections(areaForKey("admin"), allModules, visibility)).toEqual([]);
  });

  it("names the area in the header", () => {
    expect(appTitleLabel(areaForKey("settings"))).toBe("navigation.settings");
    expect(appTitleLabel(areaForKey("workspace"))).toBe("navigation.workspaceAdmin");
    expect(appTitleLabel(areaForKey("admin"))).toBe("navigation.systemAdmin");
  });
});

describe("activeAppKey", () => {
  it("takes the deepest match that carries an app", () => {
    expect(activeAppKey([{ staticData: {} }, { staticData: { app: "customers" } }, { staticData: {} }])).toBe(
      "customers",
    );
  });

  it("reads an area the same way", () => {
    expect(activeAppKey([{ staticData: {} }, { staticData: { app: "workspace" } }])).toBe("workspace");
  });

  it("is undefined when no match carries an app (public paths)", () => {
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

describe("appTitleLabel", () => {
  it("names the app in the header, except Home, which shows the product title", () => {
    expect(appTitleLabel(appForKey("customers"))).toBe("navigation.customers");
    expect(appTitleLabel(appForKey("home"))).toBeUndefined();
    expect(appTitleLabel(undefined)).toBeUndefined();
  });
});

describe("appNavSections", () => {
  const visibility = { permissions: ["*"], isOwner: false, canManageAuthorization: false, enabledModules: allModules };

  it("returns the app's visible sections when its module is enabled", () => {
    const sections = appNavSections(appForKey("customers"), allModules, visibility);
    expect(sections.flatMap((section) => section.items.map((item) => item.to))).toEqual([
      "/customers",
      "/customers/contacts",
    ]);
  });

  it("returns nothing for a disabled app, for Home, and off any app", () => {
    expect(
      appNavSections(appForKey("energy"), ["customers"], { ...visibility, enabledModules: ["customers"] }),
    ).toEqual([]);
    expect(appNavSections(appForKey("home"), allModules, visibility)).toEqual([]);
    expect(appNavSections(undefined, allModules, visibility)).toEqual([]);
  });

  it("still applies permission filtering inside an enabled app", () => {
    const sections = appNavSections(appForKey("customers"), allModules, {
      ...visibility,
      permissions: ["customers:view"],
    });
    expect(sections.flatMap((section) => section.items.map((item) => item.to))).toEqual(["/customers"]);
  });
});
