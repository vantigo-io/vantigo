import { describe, expect, it } from "vitest";
import {
  activeNavPath,
  firstAuthorizedIntegratedAppDestination,
  hasPermissions,
  navSearchFor,
  navSections,
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
    const sections = visibleNavSections(["customers:view", "communications:messages-view"], false, false);

    expect(sections.flatMap((section) => section.items.map((item) => item.label))).toEqual([
      "Customers",
      "Messages",
      "Settings",
    ]);
  });

  it("shows all navigation for an owner with unrestricted access", () => {
    const labels = visibleNavSections(["*"], true, true).flatMap((section) => section.items.map((item) => item.label));

    expect(labels).toEqual(navSections.flatMap((section) => section.items.map((item) => item.label)));
  });

  it("keeps lower administration separate from the first integrated destination", () => {
    const lower = navSections.find((section) => section.placement === "lower");
    expect(lower?.label).toBe("Settings & administration");
    expect(
      visibleNavSections(["customers:view"], false, false)
        .find((section) => section.placement === "lower")
        ?.items.map((item) => item.label),
    ).toEqual(["Settings"]);
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

    expect(ownerLowerLabels).toContain("Admin dashboard");
    expect(userLowerLabels).not.toContain("Admin dashboard");
    expect(
      navSections
        .find((section) => section.placement === "lower")
        ?.items.find((item) => item.label === "Admin dashboard"),
    ).toMatchObject({ to: "/admin/dashboard", ownerOnly: true });
  });

  it("provides search defaults only for list destinations", () => {
    expect(navSearchFor("customer-list")).toEqual({ page: 1, search: "" });
    expect(navSearchFor("messages-list")).toEqual({ page: 1, archived: undefined });
    expect(navSearchFor("products-list")).toEqual({ page: 1, search: "", status: "", categoryId: "" });
    expect(navSearchFor(undefined)).toBeUndefined();
  });
});

describe("active navigation paths", () => {
  const items = navSections.flatMap((section) => section.items);

  it("chooses the longest matching prefix for nested routes", () => {
    expect(activeNavPath("/products/categories/42", items)).toBe("/products/categories");
    expect(activeNavPath("/communications/mailboxes/new", items)).toBe("/communications/mailboxes");
  });

  it("does not treat a similarly prefixed route as a match", () => {
    expect(activeNavPath("/products-archive", items)).toBeUndefined();
    expect(activeNavPath("/customerships", items)).toBeUndefined();
  });
});
