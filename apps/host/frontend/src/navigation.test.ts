import { IconCircle } from "@tabler/icons-react";
import { describe, expect, it } from "vitest";
import {
  activeNavItem,
  activeNavPath,
  hasPermissions,
  type NavItem,
  type NavSection,
  navItemKey,
  navSearchFor,
  visibleNavSections,
} from "./navigation";

const icon = IconCircle;
const fixture: readonly NavSection[] = [
  {
    items: [
      { label: "open", to: "/open", icon },
      { label: "customers", to: "/customers", icon, module: "customers", requiredPermissions: ["customers:view"] },
      { label: "products", to: "/products", icon, module: "products", requiredPermissions: ["products:products-view"] },
      { label: "categories", to: "/products/categories", icon, module: "products" },
      { label: "channels", to: "/communications/channels", icon, module: "communications" },
      { label: "ai", to: "/ai", icon, ownerOnly: true, feature: "ai" },
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

  it("shows a destination naming several modules while any one of them is on", () => {
    const sections: readonly NavSection[] = [
      {
        items: [
          { label: "connections", to: "/connections", icon, modules: ["entra", "calendar"] },
          { label: "both", to: "/both", icon, module: "customers", modules: ["entra"] },
        ],
      },
    ];
    expect(labels(visibleNavSections(sections, context({ enabledModules: ["entra"] })))).toEqual(["connections"]);
    expect(labels(visibleNavSections(sections, context({ enabledModules: ["calendar"] })))).toEqual(["connections"]);
    expect(labels(visibleNavSections(sections, context({ enabledModules: ["customers"] })))).toEqual([]);
    expect(labels(visibleNavSections(sections, context({ enabledModules: undefined })))).toEqual([]);
    // Beside `module`, both must hold.
    expect(labels(visibleNavSections(sections, context({ enabledModules: ["customers", "entra"] })))).toEqual([
      "connections",
      "both",
    ]);
  });

  it("hides module destinations while enabled modules are unknown", () => {
    expect(labels(visibleNavSections(fixture, context({ enabledModules: undefined })))).toEqual(["open"]);
  });

  it("hides a feature's destination until the instance is known to have it", () => {
    const owner = { isOwner: true };
    expect(labels(visibleNavSections(fixture, context(owner)))).not.toContain("ai");
    expect(labels(visibleNavSections(fixture, context({ ...owner, features: [] })))).not.toContain("ai");
    expect(labels(visibleNavSections(fixture, context({ ...owner, features: ["ai"] })))).toContain("ai");
    // The feature is one more condition, never a replacement for the Owner's.
    expect(labels(visibleNavSections(fixture, context({ features: ["ai"] })))).not.toContain("ai");
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
      assignedUserId: undefined,
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

// Two entries may share a path when one is the other's page filtered:
// Inventory's Receipts is its movements list with kind=receipt.
describe("entries that carry a search", () => {
  const icon = (() => null) as unknown as NavItem["icon"];
  const movements: NavItem = { label: "Movements", to: "/inventory/movements", icon };
  const receipts: NavItem = { label: "Receipts", to: "/inventory/movements", search: { kind: "receipt" }, icon };
  const overview: NavItem = { label: "Overview", to: "/inventory", icon };
  const items = [overview, movements, receipts];

  it("are keyed apart from their sibling on the same path", () => {
    expect(navItemKey(movements)).toBe("/inventory/movements");
    expect(navItemKey(receipts)).toBe("/inventory/movements?kind=receipt");
  });

  it("are active only while the URL carries their search, and then beat the sibling", () => {
    expect(activeNavItem("/inventory/movements", items, { kind: "receipt" })).toBe(receipts);
    expect(activeNavItem("/inventory/movements", items, { kind: "move" })).toBe(movements);
    expect(activeNavItem("/inventory/movements", items)).toBe(movements);
    expect(activeNavItem("/inventory/movements/41", items, {})).toBe(movements);
    expect(activeNavItem("/inventory/sites/3", items, { kind: "receipt" })).toBe(overview);
    expect(activeNavPath("/inventory/movements", items)).toBe("/inventory/movements");
  });
});
