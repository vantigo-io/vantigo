import { describe, expect, it } from "vitest";
import { moduleKeys } from "../../navigation";
import { visibleProjectDetailTabs } from "./-project-detail-layout";

const values = (
  enabledModules: Parameters<typeof visibleProjectDetailTabs>[0],
  permissions: string[] | undefined,
  capabilities: Parameters<typeof visibleProjectDetailTabs>[2],
) => visibleProjectDetailTabs(enabledModules, permissions, capabilities).map((tab) => tab.value);

const canSeeEverything = {
  canManage: true,
  canContribute: true,
  canSeeFinancials: true,
  canManageMilestones: true,
  canSeeCosts: true,
};

describe("project detail tab visibility", () => {
  it("shows every tab to a caller who may see the project's financial fields", () => {
    expect(values(moduleKeys, ["*"], canSeeEverything)).toEqual([
      "overview",
      "tasks",
      "people",
      "billing",
      "economy",
      "time",
      "expenses",
    ]);
  });

  it("puts the economy tab between billing and time", () => {
    expect(values(moduleKeys, ["*"], canSeeEverything)).toEqual(expect.arrayContaining(["billing", "economy", "time"]));
    const shown = values(moduleKeys, ["*"], canSeeEverything);
    expect(shown.indexOf("billing")).toBeLessThan(shown.indexOf("economy"));
    expect(shown.indexOf("economy")).toBeLessThan(shown.indexOf("time"));
  });

  // The economy view has an hours-only half for a caller without financial
  // rights, so — unlike Billing — it carries no capability gate: a plain
  // member sees it the same way they see Overview and Tasks.
  it("shows the economy tab to a plain member without financial rights", () => {
    expect(
      values(moduleKeys, ["*"], {
        canManage: false,
        canContribute: true,
        canSeeFinancials: false,
        canManageMilestones: false,
        canSeeCosts: false,
      }),
    ).toContain("economy");
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

  // Expenses is the second tab from another module, and it carries **no**
  // project capability: a plain member of the project sees their own expenses
  // on it. What the totals above the list say — and whether there are any —
  // is the expenses API's answer, not a gate here.
  it("shows the expenses tab only when the module is on and the caller holds expenses:access", () => {
    expect(values(moduleKeys, ["projects:access", "expenses:access"], canSeeEverything)).toContain("expenses");
    expect(values(moduleKeys, ["projects:access"], canSeeEverything)).not.toContain("expenses");
    expect(values(["projects"], ["*"], canSeeEverything)).not.toContain("expenses");
    expect(values(undefined, ["*"], canSeeEverything)).not.toContain("expenses");
  });

  it("shows the expenses tab to a plain member with no financial rights on the project", () => {
    expect(
      values(moduleKeys, ["projects:access", "expenses:access"], {
        canManage: false,
        canContribute: true,
        canSeeFinancials: false,
        canManageMilestones: false,
        canSeeCosts: false,
      }),
    ).toContain("expenses");
  });

  it("puts the two tabs other modules add last, time before expenses", () => {
    const shown = values(moduleKeys, ["*"], canSeeEverything);
    expect(shown.at(-1)).toBe("expenses");
    expect(shown.indexOf("time")).toBeLessThan(shown.indexOf("expenses"));
  });

  // Tasks follow the project's own roles — there is no task permission and no
  // task capability (design §6) — so seeing the project is seeing its tasks.
  // A viewer gets the tab and a read-only board; the package decides that from
  // canContribute, not the host. Economy carries no gate of its own either, so
  // it is there too, with only its amounts shaped away by the response.
  it("shows the tasks and economy tabs to anyone who sees the project, financials or not", () => {
    expect(
      values(moduleKeys, [], {
        canManage: false,
        canContribute: false,
        canSeeFinancials: false,
        canManageMilestones: false,
        canSeeCosts: false,
      }),
    ).toEqual(["overview", "tasks", "people", "economy"]);
  });

  // The capability, not a permission, decides: the backend shapes the
  // financial fields out of the response per project (design D12), so a caller
  // who may see a project without its amounts gets no Billing tab — and, if
  // they paste the URL anyway, the package's own forbidden state. Economy
  // carries no such gate, so it stays.
  it("hides the billing tab without canSeeFinancials on this project, but keeps economy", () => {
    expect(
      values(moduleKeys, ["*"], {
        canManage: true,
        canContribute: true,
        canSeeFinancials: false,
        canManageMilestones: true,
        canSeeCosts: false,
      }),
    ).toEqual(["overview", "tasks", "people", "economy", "time", "expenses"]);
  });

  it("hides the billing tab while the project is still loading, but keeps economy", () => {
    expect(values(moduleKeys, ["*"], undefined)).toEqual([
      "overview",
      "tasks",
      "people",
      "economy",
      "time",
      "expenses",
    ]);
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
