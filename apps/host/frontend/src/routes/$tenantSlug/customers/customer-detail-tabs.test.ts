import { describe, expect, it } from "vitest";
import { visibleCustomerDetailTabs } from "./$customerId";

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

  it("shows only the overview while capabilities and permissions are still loading", () => {
    expect(values(undefined, undefined)).toEqual(["overview"]);
  });
});
