import { describe, expect, it } from "vitest";
import {
  activeNavPath,
  firstAuthorizedIntegratedAppDestination,
  hasPermissions,
  navSearchFor,
  navSections,
  navSectionsForTenant,
  visibleNavSections,
} from "./navigation";

describe("navigation permissions", () => {
  it("requires every declared permission unless access is unrestricted", () => {
    expect(hasPermissions(undefined)).toBe(true);
    expect(hasPermissions([], ["customers:view"])).toBe(false);
    expect(hasPermissions(["customers:view"], ["customers:view"])).toBe(true);
    expect(hasPermissions(["customers:view"], ["customers:view", "customers:contacts-view"])).toBe(false);
    expect(hasPermissions(["*"], ["customers:view", "customers:contacts-view"])).toBe(true);
  });

  it("filters business, owner-only, and authorization navigation for a restricted user", () => {
    const sections = visibleNavSections(["customers:view", "communications:conversations-view"], false, false);

    expect(sections.flatMap((section) => section.items.map((item) => item.label))).toEqual([
      "navigation.customers",
      "navigation.inbox",
      "navigation.settings",
    ]);
  });

  it("shows all navigation for a system-admin owner with unrestricted access", () => {
    const labels = visibleNavSections(["*"], true, true, undefined, true).flatMap((section) =>
      section.items.map((item) => item.label),
    );

    expect(labels).toEqual(navSections.flatMap((section) => section.items.map((item) => item.label)));
  });

  it("hides the system admin area from owners who are not system admins", () => {
    const labels = visibleNavSections(["*"], true, true).flatMap((section) => section.items.map((item) => item.label));

    expect(labels).not.toContain("navigation.systemAdmin");
  });

  it("does not expose users or invitations as direct owner destinations", () => {
    const ownerItems = visibleNavSections(["*"], true, true).flatMap((section) => section.items);

    expect(ownerItems.map((item) => item.label)).not.toContain("admin.users");
    expect(ownerItems.map((item) => item.label)).not.toContain("admin.invitations");
    expect(ownerItems.map((item) => item.to)).not.toContain("/settings/users");
    expect(ownerItems.map((item) => item.to)).not.toContain("/settings/invitations");
  });

  it("keeps lower administration separate from the first integrated destination", () => {
    const lower = navSections.find((section) => section.placement === "lower");
    expect(lower?.label).toBe("navigation.settingsAdministration");
    expect(
      visibleNavSections(["customers:view"], false, false)
        .find((section) => section.placement === "lower")
        ?.items.map((item) => item.label),
    ).toEqual(["navigation.settings"]);
    expect(firstAuthorizedIntegratedAppDestination(["customers:view"], false, false)).toBe("/customers");
    expect(firstAuthorizedIntegratedAppDestination([], false, false)).toBeUndefined();
  });

  it("shows the admin dashboard in lower navigation only to owners", () => {
    const ownerLowerLabels = visibleNavSections(["*"], true, true)
      .find((section) => section.placement === "lower")
      ?.items.map((item) => item.label);
    const userLowerLabels = visibleNavSections(["*"], false, true)
      .find((section) => section.placement === "lower")
      ?.items.map((item) => item.label);

    expect(ownerLowerLabels).toContain("navigation.adminDashboard");
    expect(userLowerLabels).not.toContain("navigation.adminDashboard");
    expect(
      navSections
        .find((section) => section.placement === "lower")
        ?.items.find((item) => item.label === "navigation.adminDashboard"),
    ).toMatchObject({ to: "/settings/overview", ownerOnly: true });
  });

  it("keeps Roles & access reachable for authorization users", () => {
    const authorizationItems = visibleNavSections(["*"], false, true).flatMap((section) => section.items);

    expect(authorizationItems).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          label: "navigation.rolesAccess",
          to: "/settings/roles",
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

  it("prefixes tenant-scoped destinations without changing global destinations", () => {
    const items = navSectionsForTenant("acme").flatMap((section) => section.items);

    expect(items.find((item) => item.label === "navigation.customers")?.to).toBe("/acme/customers");
    expect(items.find((item) => item.label === "navigation.inbox")?.to).toBe("/acme/inbox");
    expect(items.find((item) => item.label === "navigation.settings")?.to).toBe("/settings");
    expect(items.find((item) => item.label === "navigation.adminDashboard")?.to).toBe("/acme/settings/overview");
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
