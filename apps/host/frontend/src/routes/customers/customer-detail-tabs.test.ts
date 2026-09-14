import { describe, expect, it } from "vitest";
import { moduleKeys } from "../../navigation";
import { visibleCustomerDetailTabs } from "./-customer-detail-layout";

const values = (enabledModules: Parameters<typeof visibleCustomerDetailTabs>[0], permissions: string[] | undefined) =>
  visibleCustomerDetailTabs(enabledModules, permissions).map((tab) => tab.value);

describe("customer detail tab visibility", () => {
  it("shows every tab when all modules are enabled and permissions granted", () => {
    expect(values(["communications", "customers", "energy", "products"], ["*"])).toEqual([
      "overview",
      "energy",
      "correspondence",
    ]);
  });

  it("hides the energy tab when the energy module is disabled for the tenant", () => {
    expect(values(["communications", "customers"], ["*"])).toEqual(["overview", "correspondence"]);
  });

  it("hides the correspondence tab when the communications module is disabled", () => {
    expect(values(["customers", "energy"], ["*"])).toEqual(["overview", "energy"]);
  });

  it("hides tabs whose view permission is missing even when the module is enabled", () => {
    expect(values(["communications", "customers", "energy"], ["customers:view"])).toEqual(["overview"]);
    expect(values(["communications", "customers", "energy"], ["energy:metering-points-view"])).toEqual([
      "overview",
      "energy",
    ]);
  });

  // Every module in the navigation catalog ships in this build (the
  // tenant-capabilities endpoint is gone; see the production call site in
  // $customerId.tsx), so enabledModules is never actually undefined here.
  // Only the permissions query is genuinely transient.
  it("shows only the overview while permissions are still loading", () => {
    expect(values(moduleKeys, undefined)).toEqual(["overview"]);
  });
});
