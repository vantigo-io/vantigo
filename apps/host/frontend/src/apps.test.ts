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

const allModules = ["communications", "customers", "energy", "products", "projects", "time"] as const;

describe("the app registry", () => {
  it("lists Home first, without a module, and every module app once", () => {
    expect(apps[0]).toMatchObject({ key: "home", home: "/dashboard", navSections: [] });
    expect(apps[0]?.module).toBeUndefined();
    expect(apps.map((app) => app.key)).toEqual([
      "home",
      "customers",
      "projects",
      "time",
      "communications",
      "products",
      "energy",
    ]);
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
      "/projects",
      "/projects/my-tasks",
      "/projects/economy",
      "/time",
      "/time/approvals",
      "/time/people",
      "/time/settings",
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

  // The Projects app is three sidebar entries behind the same one permission,
  // so its tile is exactly as visible as they are: shown to a caller holding
  // projects:access, absent without it, and muted rather than hidden when the
  // installation did not mount the module. My tasks and the economy portfolio
  // come after the list and carry no search strategy of their own — the
  // portfolio's own validator supplies its defaults, and my tasks takes no URL
  // search params beyond the create intent Spotlight hands it.
  it("gives Projects a list, My tasks and the economy portfolio behind projects:access, and a tile that follows them", () => {
    const projects = appForKey("projects");
    expect(projects).toMatchObject({ module: "projects", label: "navigation.projects", home: "/projects" });
    expect(projects.requiredPermissions).toEqual(["projects:access"]);
    const items = projects.navSections.flatMap((section) => section.items);
    expect(items.map((item) => [item.label, item.to, item.searchStrategy])).toEqual([
      ["navigation.projects", "/projects", "projects-list"],
      ["navigation.myTasks", "/projects/my-tasks", undefined],
      ["navigation.projectsEconomy", "/projects/economy", undefined],
    ]);
    for (const item of items) expect(item.requiredPermissions).toEqual(["projects:access"]);
  });

  it("shows the Projects tile for projects:access, hides it without, and mutes it when the module is off", () => {
    const keys = (permissions: string[], enabled: readonly (typeof allModules)[number][]) =>
      switcherTiles(permissions, enabled, undefined).map((tile) => [tile.app.key, tile.enabled]);
    expect(keys(["projects:access"], allModules)).toContainEqual(["projects", true]);
    expect(keys(["customers:view"], allModules).map(([key]) => key)).not.toContain("projects");
    expect(keys(["projects:access"], ["customers"])).toContainEqual(["projects", false]);
  });

  // Time is four destinations behind four different permissions, so the tile
  // is as visible as the least of them: anyone who may log an hour gets it,
  // and the sidebar then shows only the pages that caller may open. Approvals
  // is the interesting one — the sidebar entry is for the dedicated approvers,
  // while a project manager reaches the same queue from the dashboard (see the
  // guard's rule below).
  it("gives Time four destinations, each behind its own permission", () => {
    const time = appForKey("time");
    expect(time).toMatchObject({ module: "time", label: "navigation.time", home: "/time" });
    expect(time.requiredPermissions).toEqual(["time:access", "time:approve", "time:view-all", "time:manage"]);
    const items = time.navSections.flatMap((section) => section.items);
    expect(items.map((item) => [item.label, item.to, item.requiredPermissions, item.searchStrategy])).toEqual([
      ["navigation.myWeek", "/time", ["time:access"], "time-week"],
      ["navigation.approvals", "/time/approvals", ["time:approve"], undefined],
      ["navigation.people", "/time/people", ["time:view-all"], undefined],
      ["navigation.timeSettings", "/time/settings", ["time:manage"], undefined],
    ]);
  });

  it("shows the Time tile for time:access, hides it without, and mutes it when the module is off", () => {
    const keys = (permissions: string[], enabled: readonly (typeof allModules)[number][]) =>
      switcherTiles(permissions, enabled, undefined).map((tile) => [tile.app.key, tile.enabled]);
    expect(keys(["time:access"], allModules)).toContainEqual(["time", true]);
    expect(keys(["customers:view"], allModules).map(([key]) => key)).not.toContain("time");
    expect(keys(["time:access"], ["customers"])).toContainEqual(["time", false]);
  });

  it("shows a time:access holder My week alone, and each further page with its permission", () => {
    const paths = (permissions: string[]) =>
      appNavSections(appForKey("time"), allModules, {
        permissions,
        isOwner: false,
        canManageAuthorization: false,
        enabledModules: allModules,
      }).flatMap((section) => section.items.map((item) => item.to));
    expect(paths(["time:access"])).toEqual(["/time"]);
    expect(paths(["time:access", "time:approve"])).toEqual(["/time", "/time/approvals"]);
    expect(paths(["time:access", "time:view-all"])).toEqual(["/time", "/time/people"]);
    expect(paths(["time:access", "time:manage"])).toEqual(["/time", "/time/settings"]);
    expect(paths(["*"])).toEqual(["/time", "/time/approvals", "/time/people", "/time/settings"]);
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
    expect(tiles.map((tile) => tile.app.key)).toEqual([
      "home",
      "customers",
      "projects",
      "time",
      "communications",
      "products",
      "energy",
    ]);
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
      ["projects", false],
      ["time", false],
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
