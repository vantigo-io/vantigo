import { IconCircle } from "@tabler/icons-react";
import { describe, expect, it } from "vitest";
import { activeNavPath, hasPermissions, type NavSection, navSearchFor, visibleNavSections } from "./navigation";

const icon = IconCircle;
const fixture: readonly NavSection[] = [
  {
    items: [
      { label: "open", to: "/open", icon },
      { label: "customers", to: "/customers", icon, module: "customers", requiredPermissions: ["customers:view"] },
      { label: "products", to: "/products", icon, module: "products", requiredPermissions: ["products:products-view"] },
      { label: "categories", to: "/products/categories", icon, module: "products" },
      { label: "channels", to: "/communications/channels", icon, module: "communications" },
    ],
  },
  {
    label: "admin",
    items: [
      { label: "owner", to: "/owner", icon, ownerOnly: true },
      { label: "roles", to: "/roles", icon, capability: "authorization" },
      { label: "system", to: "/system", icon, systemAdminOnly: true },
    ],
  },
];
const allModules = ["communications", "customers", "energy", "products", "projects", "time"] as const;
const context = (overrides: Partial<Parameters<typeof visibleNavSections>[1]> = {}) => ({
  permissions: ["*"],
  isOwner: false,
  canManageAuthorization: false,
  enabledModules: allModules,
  ...overrides,
});
const labels = (sections: readonly NavSection[]) =>
  sections.flatMap((section) => section.items.map((item) => item.label));

describe("navigation permissions", () => {
  it("grants access when any declared permission is held", () => {
    expect(hasPermissions(undefined)).toBe(true);
    expect(hasPermissions([], ["customers:view"])).toBe(false);
    expect(hasPermissions(["customers:view"], ["customers:view"])).toBe(true);
    expect(hasPermissions(["customers:view"], ["customers:view", "customers:contacts-view"])).toBe(true);
    expect(hasPermissions(["products:products-view"], ["customers:view", "customers:contacts-view"])).toBe(false);
    expect(hasPermissions(["*"], ["customers:view", "customers:contacts-view"])).toBe(true);
  });

  it("filters by permission and drops nothing else for a plain member", () => {
    expect(labels(visibleNavSections(fixture, context({ permissions: ["customers:view"] })))).toEqual([
      "open",
      "customers",
      "categories",
      "channels",
    ]);
  });

  it("hides destinations of disabled modules", () => {
    expect(labels(visibleNavSections(fixture, context({ enabledModules: ["customers"] })))).toEqual([
      "open",
      "customers",
    ]);
  });

  it("hides module destinations while enabled modules are unknown", () => {
    expect(labels(visibleNavSections(fixture, context({ enabledModules: undefined })))).toEqual(["open"]);
  });

  it("gates owner, authorization and system admin items separately", () => {
    expect(labels(visibleNavSections(fixture, context({ isOwner: true })))).toContain("owner");
    expect(labels(visibleNavSections(fixture, context({ isOwner: true })))).not.toContain("roles");
    expect(labels(visibleNavSections(fixture, context({ canManageAuthorization: true })))).toContain("roles");
    expect(labels(visibleNavSections(fixture, context({ isOwner: true, canManageAuthorization: true })))).not.toContain(
      "system",
    );
    expect(labels(visibleNavSections(fixture, context({ isSystemAdmin: true })))).toContain("system");
  });

  it("drops sections that end up empty", () => {
    const sections = visibleNavSections(fixture, context());
    expect(sections.map((section) => section.label)).toEqual([undefined]);
  });

  it("preserves extra fields on the section type it is given", () => {
    const adminSection = fixture[1] ?? { items: [] };
    const typed = [{ label: "admin", extra: 1, items: adminSection.items }];
    const [section] = visibleNavSections(typed, context({ isOwner: true }));
    expect(section?.extra).toBe(1);
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
    expect(navSearchFor("energy-list")).toEqual({ page: 1, search: "" });
    expect(navSearchFor("projects-list")).toEqual({ page: 1, search: "", status: "", mine: false });
    // My week has one param and no default: an absent week is "this week", so
    // Spotlight clears a week left over from wherever the caller came from.
    expect(navSearchFor("time-week")).toEqual({ week: undefined });
    expect(navSearchFor(undefined)).toBeUndefined();
  });
});

describe("active navigation paths", () => {
  const items = fixture.flatMap((section) => section.items);

  it("chooses the longest matching prefix for nested routes", () => {
    expect(activeNavPath("/products/categories/42", items)).toBe("/products/categories");
    expect(activeNavPath("/communications/channels/new", items)).toBe("/communications/channels");
  });

  it("does not treat a similarly prefixed route as a match", () => {
    expect(activeNavPath("/products-archive", items)).toBeUndefined();
    expect(activeNavPath("/customerships", items)).toBeUndefined();
  });
});
