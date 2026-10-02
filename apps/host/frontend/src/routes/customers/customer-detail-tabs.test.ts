import { describe, expect, it } from "vitest";
import { moduleKeys } from "../../navigation";
import {
  activeCustomerDetailTab,
  showCorrespondenceAction,
  visibleCustomerDetailTabs,
} from "./-customer-detail-layout";

const values = (enabledModules: Parameters<typeof visibleCustomerDetailTabs>[0], permissions: string[] | undefined) =>
  visibleCustomerDetailTabs(enabledModules, permissions).map((tab) => tab.value);

describe("customer detail tab visibility", () => {
  it("shows every tab when all modules are enabled and permissions granted", () => {
    expect(values(moduleKeys, ["*"])).toEqual(["overview", "energy", "projects", "invoices"]);
  });

  it("hides the energy tab when the energy module is disabled for the tenant", () => {
    expect(values(["communications", "customers"], ["*"])).toEqual(["overview"]);
  });

  it("hides the projects tab when the projects module is disabled for the tenant", () => {
    expect(values(["communications", "customers", "energy"], ["*"])).toEqual(["overview", "energy"]);
  });

  it("hides the projects tab without projects:access even though the module is enabled", () => {
    expect(values(moduleKeys, ["customers:view"])).toEqual(["overview"]);
    expect(values(moduleKeys, ["projects:access"])).toEqual(["overview", "projects"]);
  });

  it("hides the energy tab when its view permission is missing even though the module is enabled", () => {
    expect(values(["communications", "customers", "energy"], ["customers:view"])).toEqual(["overview"]);
    expect(values(["communications", "customers", "energy"], ["energy:metering-points-view"])).toEqual([
      "overview",
      "energy",
    ]);
    expect(values(moduleKeys, ["energy:metering-points-view"])).toEqual(["overview", "energy"]);
  });

  it("shows the invoices tab only with the invoices module and invoices:access", () => {
    expect(values(moduleKeys, ["invoices:access"])).toEqual(["overview", "invoices"]);
    expect(values(["customers", "projects"], ["*"])).toEqual(["overview", "projects"]);
    expect(values(["customers", "invoices"], ["invoices:access"])).toEqual(["overview", "invoices"]);
  });

  it("hides the invoices tab without invoices:access even though the module is enabled", () => {
    expect(values(moduleKeys, ["customers:view", "invoices:create", "invoices:issue"])).toEqual(["overview"]);
  });

  // Every module in the navigation catalog ships in this build (the
  // tenant-capabilities endpoint is gone; see the production call site in
  // $customerId.tsx), so enabledModules is never actually undefined here.
  // Only the permissions query is genuinely transient.
  it("shows only the overview while permissions are still loading", () => {
    expect(values(moduleKeys, undefined)).toEqual(["overview"]);
  });
});

// Without its route id in the ternary a tab would never highlight, and the
// row would silently say "Overview" on the invoices page.
describe("the active customer detail tab", () => {
  const routeIds = (...ids: string[]) => ["__root__", "/customers", "/customers/$customerId", ...ids];

  it("follows the child route the URL matched", () => {
    expect(activeCustomerDetailTab(routeIds("/customers/$customerId/"))).toBe("overview");
    expect(activeCustomerDetailTab(routeIds("/customers/$customerId/energy"))).toBe("energy");
    expect(activeCustomerDetailTab(routeIds("/customers/$customerId/projects"))).toBe("projects");
    expect(activeCustomerDetailTab(routeIds("/customers/$customerId/invoices"))).toBe("invoices");
  });
});

// Correspondence is a header action that opens the inbox, not a tab, but it
// is gated exactly as the tab it replaced was.
describe("the correspondence action", () => {
  it("shows when communications is enabled and the caller may view conversations", () => {
    expect(showCorrespondenceAction(moduleKeys, ["*"])).toBe(true);
    expect(showCorrespondenceAction(moduleKeys, ["communications:conversations-view"])).toBe(true);
  });

  it("hides when the communications module is disabled", () => {
    expect(showCorrespondenceAction(["customers", "energy"], ["*"])).toBe(false);
  });

  it("hides without the conversations permission, and while permissions are loading", () => {
    expect(showCorrespondenceAction(moduleKeys, ["customers:view"])).toBe(false);
    expect(showCorrespondenceAction(moduleKeys, undefined)).toBe(false);
  });
});
