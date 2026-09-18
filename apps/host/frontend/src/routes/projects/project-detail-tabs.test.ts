import { describe, expect, it } from "vitest";
import { moduleKeys } from "../../navigation";
import { visibleProjectDetailTabs } from "./-project-detail-layout";

const values = (
  enabledModules: Parameters<typeof visibleProjectDetailTabs>[0],
  permissions: string[] | undefined,
  capabilities: Parameters<typeof visibleProjectDetailTabs>[2],
) => visibleProjectDetailTabs(enabledModules, permissions, capabilities).map((tab) => tab.value);

const canSeeEverything = { canManage: true, canContribute: true, canSeeFinancials: true };

describe("project detail tab visibility", () => {
  it("shows every tab to a caller who may see the project's financial fields", () => {
    expect(values(moduleKeys, ["*"], canSeeEverything)).toEqual(["overview", "people", "billing"]);
  });

  // The capability, not a permission, decides: the backend shapes the
  // financial fields out of the response per project (design D12), so a caller
  // who may see a project without its amounts gets no Billing tab — and, if
  // they paste the URL anyway, the package's own forbidden state.
  it("hides the billing tab without canSeeFinancials on this project", () => {
    expect(values(moduleKeys, ["*"], { canManage: true, canContribute: true, canSeeFinancials: false })).toEqual([
      "overview",
      "people",
    ]);
  });

  it("hides the billing tab while the project is still loading", () => {
    expect(values(moduleKeys, ["*"], undefined)).toEqual(["overview", "people"]);
  });

  // The three tabs of the app itself carry no module or permission gate: the
  // whole /projects prefix already sits behind projects:access in the
  // permission guard. The gate exists for the tabs other modules will add,
  // the way Energy adds one to the customer page.
  it("leaves the app's own tabs to the route guard rather than re-checking projects:access", () => {
    expect(values(moduleKeys, [], canSeeEverything)).toEqual(["overview", "people", "billing"]);
    expect(values(moduleKeys, undefined, canSeeEverything)).toEqual(["overview", "people", "billing"]);
  });
});
