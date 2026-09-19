import { describe, expect, it } from "vitest";
import { moduleKeys } from "../../navigation";
import { visibleProjectDetailTabs } from "./-project-detail-layout";

const values = (
  enabledModules: Parameters<typeof visibleProjectDetailTabs>[0],
  permissions: string[] | undefined,
  capabilities: Parameters<typeof visibleProjectDetailTabs>[2],
) => visibleProjectDetailTabs(enabledModules, permissions, capabilities).map((tab) => tab.value);

const canSeeEverything = { canManage: true, canContribute: true, canSeeFinancials: true, canManageMilestones: true };

describe("project detail tab visibility", () => {
  it("shows every tab to a caller who may see the project's financial fields", () => {
    expect(values(moduleKeys, ["*"], canSeeEverything)).toEqual([
      "overview",
      "tasks",
      "people",
      "billing",
      "economy",
      "time",
    ]);
  });

  // Delivery A of the economy view only has an invoice plan to show, so it is
  // gated exactly like Billing: the capability, not a permission. The next
  // delivery widens this once the tab has an hours-only half for everyone who
  // sees the project.
  it("puts the economy tab between billing and time, gated on canSeeFinancials like billing", () => {
    expect(values(moduleKeys, ["*"], canSeeEverything)).toEqual(expect.arrayContaining(["billing", "economy", "time"]));
    const shown = values(moduleKeys, ["*"], canSeeEverything);
    expect(shown.indexOf("billing")).toBeLessThan(shown.indexOf("economy"));
    expect(shown.indexOf("economy")).toBeLessThan(shown.indexOf("time"));

    expect(
      values(moduleKeys, ["*"], {
        canManage: true,
        canContribute: true,
        canSeeFinancials: false,
        canManageMilestones: true,
      }),
    ).not.toContain("economy");
  });

  // The Time tab belongs to another module, so it carries both gates the
  // project's own tabs do without: the installation has to have mounted time,
  // and the caller has to hold time:access. The panel then asks the time API
  // for this project, which answers 404 to anyone who may not see it.
  it("shows the time tab only when the module is on and the caller holds time:access", () => {
    expect(values(moduleKeys, ["projects:access", "time:access"], canSeeEverything)).toContain("time");
    expect(values(moduleKeys, ["projects:access"], canSeeEverything)).not.toContain("time");
    expect(values(["projects"], ["*"], canSeeEverything)).not.toContain("time");
    expect(values(undefined, ["*"], canSeeEverything)).not.toContain("time");
  });

  it("puts the time tab last, after the project's own views end", () => {
    expect(values(moduleKeys, ["*"], canSeeEverything).at(-1)).toBe("time");
  });

  // Tasks follow the project's own roles — there is no task permission and no
  // task capability (design §6) — so seeing the project is seeing its tasks.
  // A viewer gets the tab and a read-only board; the package decides that from
  // canContribute, not the host.
  it("shows the tasks tab to anyone who sees the project, financials or not", () => {
    expect(
      values(moduleKeys, [], {
        canManage: false,
        canContribute: false,
        canSeeFinancials: false,
        canManageMilestones: false,
      }),
    ).toEqual(["overview", "tasks", "people"]);
  });

  // The capability, not a permission, decides: the backend shapes the
  // financial fields out of the response per project (design D12), so a caller
  // who may see a project without its amounts gets no Billing tab — and, if
  // they paste the URL anyway, the package's own forbidden state.
  it("hides the billing tab without canSeeFinancials on this project", () => {
    expect(
      values(moduleKeys, ["*"], {
        canManage: true,
        canContribute: true,
        canSeeFinancials: false,
        canManageMilestones: true,
      }),
    ).toEqual(["overview", "tasks", "people", "time"]);
  });

  it("hides the billing tab while the project is still loading", () => {
    expect(values(moduleKeys, ["*"], undefined)).toEqual(["overview", "tasks", "people", "time"]);
  });

  // The app's own tabs carry no module or permission gate: the
  // whole /projects prefix already sits behind projects:access in the
  // permission guard. The gate exists for the tabs other modules will add,
  // the way Energy adds one to the customer page.
  it("leaves the app's own tabs to the route guard rather than re-checking projects:access", () => {
    expect(values(moduleKeys, [], canSeeEverything)).toEqual(["overview", "tasks", "people", "billing", "economy"]);
    expect(values(moduleKeys, undefined, canSeeEverything)).toEqual([
      "overview",
      "tasks",
      "people",
      "billing",
      "economy",
    ]);
  });
});
