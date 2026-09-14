import { describe, expect, it } from "vitest";
import {
  activeNavPath,
  hasPermissions,
  type ModuleKey,
  navSearchFor,
  navSections,
  visibleNavSections,
} from "./navigation";

const allModules: ModuleKey[] = ["communications", "customers", "energy", "products"];
const context = (overrides: Partial<Parameters<typeof visibleNavSections>[0]> = {}) => ({
  permissions: ["*"],
  isOwner: false,
  canManageAuthorization: false,
  enabledModules: allModules,
  ...overrides,
});

describe("navigation permissions", () => {
  it("grants access when any declared permission is held", () => {
    expect(hasPermissions(undefined)).toBe(true);
    expect(hasPermissions([], ["customers:view"])).toBe(false);
    expect(hasPermissions(["customers:view"], ["customers:view"])).toBe(true);
    expect(hasPermissions(["customers:view"], ["customers:view", "customers:contacts-view"])).toBe(true);
    expect(hasPermissions(["customers:contacts-view"], ["customers:view", "customers:contacts-view"])).toBe(true);
    expect(hasPermissions(["products:products-view"], ["customers:view", "customers:contacts-view"])).toBe(false);
    expect(hasPermissions(["*"], ["customers:view", "customers:contacts-view"])).toBe(true);
  });

  it("filters business, owner-only, and authorization navigation for a restricted user", () => {
    const sections = visibleNavSections(
      context({ permissions: ["customers:view", "communications:conversations-view"] }),
    );

    expect(sections.flatMap((section) => section.items.map((item) => item.label))).toEqual([
      "navigation.dashboard",
      "navigation.customers",
      "navigation.inbox",
      "navigation.settings",
    ]);
  });

  it("shows all navigation for a system-admin owner with unrestricted access", () => {
    const labels = visibleNavSections(
      context({ isOwner: true, canManageAuthorization: true, isSystemAdmin: true }),
    ).flatMap((section) => section.items.map((item) => item.label));

    expect(labels).toEqual(navSections.flatMap((section) => section.items.map((item) => item.label)));
  });

  it("hides destinations for disabled modules", () => {
    const labels = visibleNavSections(context({ enabledModules: ["customers"] })).flatMap((section) =>
      section.items.map((item) => item.label),
    );

    expect(labels).toContain("navigation.customers");
    expect(labels).toContain("navigation.contacts");
    expect(labels).not.toContain("navigation.inbox");
    expect(labels).not.toContain("navigation.products");
    expect(labels).not.toContain("navigation.meteringPoints");
  });

  it("hides module destinations while enabled modules are unknown", () => {
    const labels = visibleNavSections(context({ enabledModules: undefined })).flatMap((section) =>
      section.items.map((item) => item.label),
    );

    expect(labels).toEqual(["navigation.dashboard", "navigation.settings"]);
  });

  it("addresses every destination by a bare path with no tenant segment or route parameter", () => {
    const items = navSections.flatMap((section) => section.items);

    for (const item of items) {
      expect(item.to).toMatch(/^\/[a-z-]+(\/[a-z-]+)*$/);
    }
    expect(items.map((item) => item.to)).toEqual([
      "/dashboard",
      "/customers",
      "/contacts",
      "/inbox",
      "/communications/channels",
      "/communications/suppressions",
      "/products",
      "/products/categories",
      "/energy/metering-points",
      "/settings",
      "/workspace/overview",
      "/workspace/roles",
      "/admin",
    ]);
  });

  it("keeps workspace administration on its own segment, separate from personal settings", () => {
    const lower = navSections.find((section) => section.placement === "lower")?.items ?? [];
    const personal = lower.find((item) => item.label === "navigation.settings");
    const workspaceItems = lower.filter((item) => item.label !== "navigation.settings" && item.to !== "/admin");

    // The Owner gate lives on the /workspace route; pointing these at
    // /settings/* would put four admin pages behind the ungated personal
    // account tree instead.
    expect(personal?.to).toBe("/settings");
    expect(workspaceItems.map((item) => item.to)).toEqual(["/workspace/overview", "/workspace/roles"]);
    for (const item of workspaceItems) expect(item.to.startsWith("/settings")).toBe(false);
  });

  it("hides the system admin area from owners who are not system admins", () => {
    const labels = visibleNavSections(context({ isOwner: true, canManageAuthorization: true })).flatMap((section) =>
      section.items.map((item) => item.label),
    );

    expect(labels).not.toContain("navigation.systemAdmin");
  });

  it("does not expose users or invitations as direct owner destinations", () => {
    const ownerItems = visibleNavSections(context({ isOwner: true, canManageAuthorization: true })).flatMap(
      (section) => section.items,
    );

    expect(ownerItems.map((item) => item.label)).not.toContain("admin.users");
    expect(ownerItems.map((item) => item.label)).not.toContain("admin.invitations");
    expect(ownerItems.map((item) => item.to)).not.toContain("/workspace/users");
    expect(ownerItems.map((item) => item.to)).not.toContain("/workspace/invitations");
  });

  it("keeps lower administration separate from the first integrated destination", () => {
    const lower = navSections.find((section) => section.placement === "lower");
    expect(lower?.label).toBe("navigation.settingsAdministration");
    expect(
      visibleNavSections(context({ permissions: ["customers:view"] }))
        .find((section) => section.placement === "lower")
        ?.items.map((item) => item.label),
    ).toEqual(["navigation.settings"]);
  });

  it("shows the admin dashboard in lower navigation only to owners", () => {
    const ownerLowerLabels = visibleNavSections(context({ isOwner: true, canManageAuthorization: true }))
      .find((section) => section.placement === "lower")
      ?.items.map((item) => item.label);
    const userLowerLabels = visibleNavSections(context({ canManageAuthorization: true }))
      .find((section) => section.placement === "lower")
      ?.items.map((item) => item.label);

    expect(ownerLowerLabels).toContain("navigation.adminDashboard");
    expect(userLowerLabels).not.toContain("navigation.adminDashboard");
    expect(
      navSections
        .find((section) => section.placement === "lower")
        ?.items.find((item) => item.label === "navigation.adminDashboard"),
    ).toMatchObject({ to: "/workspace/overview", ownerOnly: true });
  });

  it("keeps Roles & access reachable for authorization users", () => {
    const authorizationItems = visibleNavSections(context({ canManageAuthorization: true })).flatMap(
      (section) => section.items,
    );

    expect(authorizationItems).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          label: "navigation.rolesAccess",
          to: "/workspace/roles",
          capability: "authorization",
        }),
      ]),
    );
  });

  it("provides search defaults only for list destinations", () => {
    expect(navSearchFor("customer-list")).toEqual({ page: 1, search: "" });
    expect(navSearchFor("inbox-list")).toEqual({
      conversationId: undefined,
      status: undefined,
      customerId: undefined,
      tagId: undefined,
      unreadOnly: undefined,
    });
    expect(navSearchFor("products-list")).toEqual({ page: 1, search: "", status: "", categoryId: "" });
    expect(navSearchFor(undefined)).toBeUndefined();
  });
});

describe("active navigation paths", () => {
  const items = navSections.flatMap((section) => section.items);

  it("chooses the longest matching prefix for nested routes", () => {
    expect(activeNavPath("/products/categories/42", items)).toBe("/products/categories");
    expect(activeNavPath("/communications/channels/new", items)).toBe("/communications/channels");
  });

  it("does not treat a similarly prefixed route as a match", () => {
    expect(activeNavPath("/products-archive", items)).toBeUndefined();
    expect(activeNavPath("/customerships", items)).toBeUndefined();
  });
});
