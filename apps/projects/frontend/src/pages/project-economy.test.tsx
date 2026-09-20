import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { Economy, EconomyActuals, EconomyLine } from "../api/economy";
import type { BillingMilestone, BillingMilestonePlan, BillingMilestoneTotals } from "../api/milestones";
import type { Project } from "../api/projects";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { ProjectEconomy } from "./project-economy";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/** The same formatter the page uses, so the assertion does not hard-code a locale's separators. */
const money = (amount: number, currency = "NOK") =>
  new Intl.NumberFormat("en-US", { style: "currency", currency }).format(amount).replace(/\u00a0/g, " ");

/** The same amount with its non-breaking space kept, the way an accessible name carries it. */
const rawMoney = (amount: number, currency = "NOK") =>
  new Intl.NumberFormat("en-US", { style: "currency", currency }).format(amount);

const work = (
  approved: number,
  submitted: number,
  draft: number,
  overrides: Partial<EconomyActuals> = {},
): EconomyActuals => ({
  approved: { hours: approved },
  submitted: { hours: submitted },
  draft: { hours: draft },
  totalHours: approved + submitted + draft,
  billableHours: approved + submitted + draft,
  nonBillableHours: 0,
  unpricedHours: 0,
  ...overrides,
});

/** What the economy endpoint answers a manager with financial rights but no cost rights. */
const economy = (overrides: Partial<Economy> = {}): Economy => ({
  timeTracking: true,
  // This installation has no expenses module; the expenses half of the page is Task 4's.
  expenseTracking: false,
  currency: "NOK",
  budget: { hours: 400, amount: 480000, fixedPrice: 1000000 },
  lines: [],
  overBudget: false,
  actuals: work(210, 62, 40, {
    approved: { hours: 210, amount: 240000 },
    submitted: { hours: 62, amount: 120000 },
    draft: { hours: 40, amount: 0 },
    totalAmount: 360000,
  }),
  budgetUsed: { basis: "amount", percent: 75, approvedPercent: 50 },
  milestones: totals(),
  ...overrides,
});

const line = (overrides: Partial<EconomyLine> = {}): EconomyLine => ({
  billingLineId: 5,
  code: "DEV",
  active: true,
  overBudget: false,
  budgetHours: 200,
  actuals: work(100, 0, 0),
  usedPercent: 50,
  remainingHours: 100,
  ...overrides,
});

const project = (overrides: Partial<Project> = {}): Project =>
  ({
    id: 7,
    code: "KVEWEBS",
    name: "Website",
    status: "active",
    billingType: "fixed-price",
    internal: false,
    capabilities: {
      canManage: true,
      canContribute: true,
      canSeeFinancials: true,
      canManageMilestones: true,
      canSeeCosts: false,
    },
    billingLinesAvailable: true,
    financials: { currency: "NOK", fixedPriceAmount: 1000000 },
    managers: [],
    revision: 3,
    createdAt: "2026-01-01T10:00:00Z",
    updatedAt: "2026-01-02T10:00:00Z",
    ...overrides,
  }) as Project;

const capabilities = (overrides: Partial<BillingMilestone["capabilities"]> = {}) => ({
  canEdit: true,
  canDelete: true,
  canMarkReady: true,
  canMarkPlanned: false,
  canMarkInvoiced: false,
  canUndoInvoiced: false,
  canCancel: true,
  canReopen: false,
  ...overrides,
});

const milestone = (overrides: Partial<BillingMilestone> = {}): BillingMilestone =>
  ({
    id: 1,
    projectId: 7,
    name: "Kick-off",
    status: "planned",
    position: 1,
    overdue: false,
    amount: 300000,
    effectiveAmount: 300000,
    currency: "NOK",
    revision: 1,
    createdAt: "2026-01-01T10:00:00Z",
    updatedAt: "2026-01-01T10:00:00Z",
    capabilities: capabilities(),
    ...overrides,
  }) as BillingMilestone;

const totals = (overrides: Partial<BillingMilestoneTotals> = {}): BillingMilestoneTotals => ({
  planned: 300000,
  ready: 0,
  invoiced: 0,
  currency: "NOK",
  fixedPrice: 1000000,
  ...overrides,
});

const plan = (
  milestones: BillingMilestone[],
  overrides: Partial<BillingMilestoneTotals> = {},
): BillingMilestonePlan => ({
  milestones,
  totals: totals(overrides),
});

interface EconomyStub {
  /** The economy endpoint's answer, or the status it refuses with. */
  economy?: Economy;
  economyStatus?: number;
}

const stubEconomy = (
  row: Project,
  body: BillingMilestonePlan | null = plan([milestone()]),
  status = 200,
  { economy: economyBody = economy(), economyStatus = 200 }: EconomyStub = {},
) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/projects/7") return Promise.resolve(jsonResponse(200, row));
    if (url.pathname === "/api/v1/projects/7/economy") {
      return Promise.resolve(
        economyStatus === 200
          ? jsonResponse(200, economyBody)
          : jsonResponse(economyStatus, { title: "The budget is unavailable" }),
      );
    }
    if (url.pathname === "/api/v1/customers") {
      return Promise.resolve(jsonResponse(200, { data: [], pagination: { page: 1, pageSize: 20 } }));
    }
    if (url.pathname === "/api/v1/projects/7/milestones" && !init?.method) {
      return Promise.resolve(status === 200 ? jsonResponse(200, body) : jsonResponse(status, { title: "Nope" }));
    }
    if (url.pathname.startsWith("/api/v1/projects/milestones/")) {
      return Promise.resolve(jsonResponse(200, milestone()));
    }
    return Promise.resolve(new Response(null, { status: 404 }));
  });

const rowFor = async (name: string) => (await screen.findByText(name)).closest("tr") as HTMLElement;

describe("ProjectEconomy", () => {
  it("tells someone who may not see the amounts why there is no plan, and asks for none", async () => {
    const fetchMock = stubEconomy(
      project({
        capabilities: {
          canManage: false,
          canContribute: false,
          canSeeFinancials: false,
          canManageMilestones: false,
          canSeeCosts: false,
        },
        financials: undefined,
      }),
    );
    renderWithProviders(<ProjectEconomy projectId={7} />);

    expect(await screen.findByText("You cannot see this project's amounts.")).toBeInTheDocument();
    expect(screen.getByTestId("empty-state")).toHaveTextContent("invoice plan");
    expect(fetchMock.actualCalls.some(([url]) => String(url).includes("/milestones"))).toBe(false);
  });

  it("says that milestones need a currency, and offers no add until there is one", async () => {
    stubEconomy(project({ financials: { fixedPriceAmount: 1000000 } }), plan([], { currency: undefined, planned: 0 }));
    renderWithProviders(<ProjectEconomy projectId={7} />);

    expect(
      await screen.findByText("Billing milestones are billed in the project's currency. Give the project one first."),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add milestone" })).not.toBeInTheDocument();
  });

  // Every row's menu holds a different set of writes, so a reader listing the
  // page's buttons has to be able to tell them apart without the table around
  // them.
  it("names each row's menu after the milestone it belongs to", async () => {
    stubEconomy(
      project(),
      plan([milestone({ id: 1, name: "Kick-off" }), milestone({ id: 2, name: "Launch", position: 2 })]),
    );
    renderWithProviders(<ProjectEconomy projectId={7} />);

    expect(await screen.findByRole("button", { name: "Actions for Kick-off" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Actions for Launch" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Milestone actions" })).not.toBeInTheDocument();
  });

  // A viewer with financial rights may not edit anything, so the row is the
  // only place they ever see what a milestone is for: it has to be in the
  // page, not behind a pointer.
  it("writes a milestone's description under its name", async () => {
    stubEconomy(project(), plan([milestone({ description: "Signed contract and the kick-off workshop" })]));
    renderWithProviders(<ProjectEconomy projectId={7} />);

    const row = await rowFor("Kick-off");
    expect(within(row).getByText("Signed contract and the kick-off workshop")).toBeInTheDocument();
  });

  it("opens the project form from the note about the missing currency", async () => {
    stubEconomy(project({ financials: { fixedPriceAmount: 1000000 } }), plan([], { currency: undefined, planned: 0 }));
    renderWithProviders(<ProjectEconomy projectId={7} />);

    await userEvent.click(await screen.findByRole("button", { name: "Edit the project" }));

    expect(await screen.findByRole("dialog", { name: "Edit project" })).toBeInTheDocument();
  });

  it("sums the plan into the three headline figures", async () => {
    stubEconomy(project(), plan([milestone()], { planned: 300000, ready: 200000, invoiced: 100000 }));
    renderWithProviders(<ProjectEconomy projectId={7} />);

    const headline = await screen.findByTestId("milestone-totals");
    expect(headline).toHaveTextContent(money(300000));
    expect(headline).toHaveTextContent(money(200000));
    expect(headline).toHaveTextContent(money(100000));
    expect(within(headline).getByText("Ready to invoice")).toBeInTheDocument();
  });

  it("names the invoice plan table after its own heading", async () => {
    stubEconomy(project(), plan([milestone()]));
    renderWithProviders(<ProjectEconomy projectId={7} />);

    expect(await screen.findByRole("table", { name: "Invoice plan" })).toBeInTheDocument();
  });

  it("renders a milestone's date, amount and status, and marks an overdue one", async () => {
    stubEconomy(
      project(),
      plan([
        milestone({ plannedDate: "2026-03-31", overdue: true }),
        milestone({ id: 2, name: "Launch", status: "ready", position: 2, plannedDate: "2026-06-30" }),
      ]),
    );
    renderWithProviders(<ProjectEconomy projectId={7} />);

    const kickoff = await rowFor("Kick-off");
    expect(kickoff).toHaveTextContent("Mar 31, 2026");
    expect(kickoff).toHaveTextContent(money(300000));
    expect(within(kickoff).getByText("Planned")).toBeInTheDocument();
    expect(within(kickoff).getByText("Overdue")).toBeInTheDocument();

    const launch = screen.getByText("Launch").closest("tr") as HTMLElement;
    expect(within(launch).getByText("Ready to invoice")).toBeInTheDocument();
    expect(within(launch).queryByText("Overdue")).not.toBeInTheDocument();
  });

  it("spells a percent milestone as its share and the amount it comes to", async () => {
    stubEconomy(project(), plan([milestone({ amount: undefined, percent: 30, effectiveAmount: 300000 })]));
    renderWithProviders(<ProjectEconomy projectId={7} />);

    const row = await rowFor("Kick-off");
    expect(row).toHaveTextContent(`30 % · ${money(300000)}`);
  });

  it("shows a dash rather than nothing for a cancelled milestone that can no longer be priced", async () => {
    stubEconomy(
      project({ financials: { currency: "NOK" }, billingType: "time-and-materials" }),
      plan([milestone({ status: "cancelled", amount: undefined, percent: 25, effectiveAmount: undefined })], {
        fixedPrice: undefined,
        planned: 0,
      }),
    );
    renderWithProviders(<ProjectEconomy projectId={7} />);

    const row = await rowFor("Kick-off");
    expect(row).toHaveTextContent("—");
    expect(row).not.toHaveTextContent("0.00");
  });

  it("lists cancelled milestones last and strikes them through", async () => {
    stubEconomy(
      project(),
      plan([
        milestone({ id: 2, name: "Launch", position: 2 }),
        milestone({
          id: 1,
          name: "Kick-off",
          position: 1,
          status: "cancelled",
          capabilities: capabilities({
            canEdit: false,
            canDelete: false,
            canMarkReady: false,
            canCancel: false,
            canReopen: true,
          }),
        }),
      ]),
    );
    renderWithProviders(<ProjectEconomy projectId={7} />);

    await screen.findByText("Launch");
    const names = screen.getAllByTestId("milestone-name").map((node) => node.textContent);
    expect(names).toEqual(["Launch", "Kick-off"]);
    expect(screen.getByText("Kick-off")).toHaveStyle({ textDecoration: "line-through" });
  });

  it("says how much of the fixed price is still unplanned", async () => {
    stubEconomy(project(), plan([milestone()], { unplanned: 700000 }));
    renderWithProviders(<ProjectEconomy projectId={7} />);

    expect(await screen.findByText(`${money(700000)} of the fixed price is not planned yet.`)).toBeInTheDocument();
  });

  it("notes an over-planned project gently rather than as an error", async () => {
    stubEconomy(project(), plan([milestone()], { overPlanned: 50000 }));
    renderWithProviders(<ProjectEconomy projectId={7} />);

    // A plan over the price is a thing to look at, not a failure: it is its
    // own gentle note rather than the red alert a load failure gets.
    const note = await screen.findByTestId("over-planned-note");
    expect(note).toHaveTextContent(`The plan is ${money(50000)} more than the fixed price.`);
    expect(screen.queryByText("Could not load the invoice plan")).not.toBeInTheDocument();
  });

  it("says neither sentence on a project with no fixed price", async () => {
    stubEconomy(
      project({ billingType: "time-and-materials", financials: { currency: "NOK" } }),
      plan([milestone()], { fixedPrice: undefined }),
    );
    renderWithProviders(<ProjectEconomy projectId={7} />);

    await screen.findByText("Kick-off");
    expect(screen.queryByText(/of the fixed price is not planned/)).not.toBeInTheDocument();
    expect(screen.queryByText(/more than the fixed price/)).not.toBeInTheDocument();
  });

  it("invites a manager to add the first milestone", async () => {
    stubEconomy(project(), plan([], { planned: 0 }));
    renderWithProviders(<ProjectEconomy projectId={7} />);

    expect(await screen.findByText("No billing milestones yet.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add milestone" })).toBeInTheDocument();
  });

  it("offers a viewer with financial rights only the two moves they may make", async () => {
    stubEconomy(
      project({
        capabilities: {
          canManage: false,
          canContribute: false,
          canSeeFinancials: true,
          canManageMilestones: false,
          canSeeCosts: false,
        },
      }),
      plan([
        milestone({
          status: "ready",
          capabilities: capabilities({
            canEdit: false,
            canDelete: false,
            canMarkReady: false,
            canCancel: false,
            canMarkInvoiced: true,
          }),
        }),
      ]),
    );
    renderWithProviders(<ProjectEconomy projectId={7} />);

    await screen.findByText("Kick-off");
    expect(screen.queryByRole("button", { name: "Add milestone" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Actions for Kick-off" }));

    expect(await screen.findByRole("menuitem", { name: "Mark as invoiced" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Edit" })).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Move up" })).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Cancel the milestone" })).not.toBeInTheDocument();
  });

  it("offers a manager everything the milestone's capabilities allow", async () => {
    stubEconomy(project(), plan([milestone()]));
    renderWithProviders(<ProjectEconomy projectId={7} />);

    await screen.findByText("Kick-off");
    await userEvent.click(screen.getByRole("button", { name: "Actions for Kick-off" }));

    expect(await screen.findByRole("menuitem", { name: "Edit" })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "Mark as ready to invoice" })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "Cancel the milestone" })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: "Delete the milestone" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Reopen the milestone" })).not.toBeInTheDocument();
  });

  // A cancelled milestone is listed last but keeps its stored number, so the
  // plan's positions are not the array's indices: the neighbour's own position
  // is what a move has to ask for.
  it("moves a milestone to its open neighbour's stored position, carrying the revision", async () => {
    const fetchMock = stubEconomy(
      project(),
      plan([
        milestone({ id: 1, name: "Kick-off", position: 1, revision: 7 }),
        milestone({ id: 2, name: "Launch", position: 3, revision: 4 }),
        milestone({ id: 3, name: "Handover", position: 2, status: "cancelled" }),
      ]),
    );
    renderWithProviders(<ProjectEconomy projectId={7} />);

    await rowFor("Launch");
    await userEvent.click(screen.getByRole("button", { name: "Actions for Launch" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Move up" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/milestones/2/position", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ position: 1, revision: 4 }),
      }),
    );

    await userEvent.click(screen.getByRole("button", { name: "Actions for Kick-off" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Move down" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/milestones/1/position", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ position: 3, revision: 7 }),
      }),
    );
  });

  it("offers no reordering on a cancelled row, nor a move past the ends", async () => {
    stubEconomy(
      project(),
      plan([
        milestone({ id: 1, name: "Kick-off", position: 1 }),
        milestone({
          id: 3,
          name: "Handover",
          position: 3,
          status: "cancelled",
          capabilities: capabilities({
            canEdit: false,
            canDelete: false,
            canMarkReady: false,
            canCancel: false,
            canReopen: true,
          }),
        }),
      ]),
    );
    renderWithProviders(<ProjectEconomy projectId={7} />);

    await rowFor("Kick-off");
    await userEvent.click(screen.getByRole("button", { name: "Actions for Kick-off" }));
    expect(await screen.findByRole("menuitem", { name: "Edit" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Move up" })).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Move down" })).not.toBeInTheDocument();
    await userEvent.keyboard("{Escape}");

    await userEvent.click(screen.getByRole("button", { name: "Actions for Handover" }));
    expect(await screen.findByRole("menuitem", { name: "Reopen the milestone" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Move up" })).not.toBeInTheDocument();
  });

  it("marks a milestone ready straight from the row", async () => {
    const fetchMock = stubEconomy(project(), plan([milestone({ revision: 2 })]));
    renderWithProviders(<ProjectEconomy projectId={7} />);

    await screen.findByText("Kick-off");
    await userEvent.click(screen.getByRole("button", { name: "Actions for Kick-off" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Mark as ready to invoice" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/milestones/1/status", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ status: "ready", revision: 2 }),
      }),
    );
  });

  it("says out loud when the server refuses a move", async () => {
    stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname === "/api/v1/projects/7") return Promise.resolve(jsonResponse(200, project()));
      if (url.pathname === "/api/v1/projects/7/economy") return Promise.resolve(jsonResponse(200, economy()));
      if (url.pathname === "/api/v1/projects/7/milestones") {
        return Promise.resolve(jsonResponse(200, plan([milestone()])));
      }
      if (url.pathname === "/api/v1/projects/milestones/1/status" && init?.method === "POST") {
        return Promise.resolve(
          jsonResponse(400, {
            title: "Invalid project",
            errors: { status: ["A milestone cannot be marked ready while the project has no currency"] },
          }),
        );
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    await screen.findByText("Kick-off");
    await userEvent.click(screen.getByRole("button", { name: "Actions for Kick-off" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Mark as ready to invoice" }));

    expect(
      await screen.findByText("A milestone cannot be marked ready while the project has no currency"),
    ).toBeInTheDocument();
  });

  // A cancelled milestone's own currency (amount_currency) can drift from the
  // project's while it sits cancelled; the server refuses a reopen that would
  // bring it back on the surface with a mismatched currency (400 on `status`).
  // capabilities.canReopen keeps the button off a fresh read, but a plan open
  // in a stale tab can still send the request — this is the same generic
  // field-error surfacing as any other refused move, pinned for this move too.
  it("says out loud when the server refuses to reopen a milestone stuck in a currency the project has left", async () => {
    stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname === "/api/v1/projects/7") return Promise.resolve(jsonResponse(200, project()));
      if (url.pathname === "/api/v1/projects/7/economy") return Promise.resolve(jsonResponse(200, economy()));
      if (url.pathname === "/api/v1/projects/7/milestones") {
        return Promise.resolve(
          jsonResponse(
            200,
            plan([
              milestone({
                id: 3,
                name: "Handover",
                status: "cancelled",
                currency: "NOK",
                capabilities: capabilities({ canEdit: false, canDelete: false, canCancel: false, canReopen: true }),
              }),
            ]),
          ),
        );
      }
      if (url.pathname === "/api/v1/projects/milestones/3/status" && init?.method === "POST") {
        return Promise.resolve(
          jsonResponse(400, {
            title: "Invalid project",
            errors: {
              status: [
                "A milestone cannot become 'planned' while its amount is in NOK and the project is now in EUR; add a new milestone instead",
              ],
            },
          }),
        );
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    await screen.findByText("Handover");
    await userEvent.click(screen.getByRole("button", { name: "Actions for Handover" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Reopen the milestone" }));

    expect(
      await screen.findByText(
        "A milestone cannot become 'planned' while its amount is in NOK and the project is now in EUR; add a new milestone instead",
      ),
    ).toBeInTheDocument();
  });

  it("translates a revision conflict rather than quoting the project's own wording", async () => {
    stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname === "/api/v1/projects/7") return Promise.resolve(jsonResponse(200, project()));
      if (url.pathname === "/api/v1/projects/7/economy") return Promise.resolve(jsonResponse(200, economy()));
      if (url.pathname === "/api/v1/projects/7/milestones") {
        return Promise.resolve(jsonResponse(200, plan([milestone()])));
      }
      if (init?.method === "POST") {
        return Promise.resolve(
          jsonResponse(409, {
            title: "Project revision conflict",
            detail: "The project has revision 5; the supplied revision was 2.",
          }),
        );
      }
      return Promise.resolve(new Response(null, { status: 404 }));
    });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    await screen.findByText("Kick-off");
    await userEvent.click(screen.getByRole("button", { name: "Actions for Kick-off" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Mark as ready to invoice" }));

    expect(
      await screen.findByText("The milestone was changed by someone else. Reload the plan and try again."),
    ).toBeInTheDocument();
    expect(screen.queryByText(/supplied revision/)).not.toBeInTheDocument();
  });

  it("reports a plan that cannot be read", async () => {
    stubEconomy(project(), null, 500);
    renderWithProviders(<ProjectEconomy projectId={7} />);

    expect(await screen.findByText("Could not load the invoice plan")).toBeInTheDocument();
  });

  // A flat amount remembers the currency it was entered in (docs/projects.md,
  // "Billing milestones and the invoice plan"), so a cancelled milestone can
  // read in a currency the project has since moved off. Each row must format
  // in *its own* currency; only the headline figures and the footer read the
  // plan's current one (totals.currency).
  it("formats a cancelled milestone's amount in the currency it was entered in, not the plan's current one", async () => {
    stubEconomy(
      project({ financials: { currency: "EUR", fixedPriceAmount: 1000000 } }),
      plan(
        [
          milestone({
            id: 2,
            name: "Old scope",
            status: "cancelled",
            amount: 50000,
            effectiveAmount: 50000,
            currency: "NOK",
            capabilities: capabilities({ canEdit: false, canDelete: false, canCancel: false, canReopen: true }),
          }),
        ],
        { currency: "EUR", planned: 0, fixedPrice: 1000000 },
      ),
    );
    renderWithProviders(<ProjectEconomy projectId={7} />);

    const row = await rowFor("Old scope");
    expect(row).toHaveTextContent(money(50000, "NOK"));
    expect(row).not.toHaveTextContent(money(50000, "EUR"));

    const totalsCard = screen.getByTestId("milestone-totals");
    expect(totalsCard).toHaveTextContent(money(0, "EUR"));
    const footer = screen.getByTestId("milestone-plan-footer");
    expect(footer).toHaveTextContent(money(1000000, "EUR"));
  });
});

describe("ProjectEconomy — the budget half", () => {
  it("names the budget the percentage is measured against, and what the work is worth", async () => {
    stubEconomy(project(), plan([milestone()]), 200, {
      economy: economy({ budgetUsed: { basis: "amount", percent: 75, approvedPercent: 50 } }),
    });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    const headline = await screen.findByTestId("budget-headline");
    expect(headline).toHaveTextContent(`75 % of the budget (${money(480000)})`);
    expect(within(headline).getByText("Value of work").parentElement).toHaveTextContent(money(360000));
    expect(within(headline).getByText("Fixed price amount").parentElement).toHaveTextContent(money(1000000));
  });

  it("says a fixed-price basis in its own words", async () => {
    stubEconomy(project(), plan([milestone()]), 200, {
      economy: economy({ budgetUsed: { basis: "fixedPrice", percent: 36, approvedPercent: 24 } }),
    });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    expect(await screen.findByText("36 % of the fixed price")).toBeInTheDocument();
  });

  it("says an hours basis in its own words", async () => {
    stubEconomy(project(), plan([milestone()]), 200, {
      economy: economy({ budgetUsed: { basis: "hours", percent: 78, approvedPercent: 52.5 } }),
    });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    expect(await screen.findByText("78 % of 400 hours")).toBeInTheDocument();
  });

  // An absent budgetUsed is "there is nothing to measure against", which is a
  // different thing from having used none of the budget.
  it("says there is no budget rather than drawing 0 %", async () => {
    stubEconomy(project(), plan([milestone()]), 200, {
      economy: economy({ budgetUsed: undefined, budget: {}, overBudget: false }),
    });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    const headline = await screen.findByTestId("budget-headline");
    expect(headline).toHaveTextContent("No budget set");
    expect(headline).not.toHaveTextContent("0 %");
  });

  // Spec §8's rule is not the line table's alone: a project bar with nothing to
  // measure against is full width whatever was logged, right under a headline
  // saying there is no budget.
  it("draws no bar on the project either when there is no budget to measure against", async () => {
    stubEconomy(project(), plan([milestone()]), 200, {
      economy: economy({ budgetUsed: undefined, budget: {}, actuals: work(20, 10, 5), lines: [] }),
    });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    const card = await screen.findByTestId("project-budget");
    expect(within(card).queryByRole("img")).not.toBeInTheDocument();
    expect(within(card).getByTestId("logged-split")).toHaveTextContent("20 h approved · 10 h submitted · 5 h draft");
    expect(screen.getByTestId("budget-headline")).toHaveTextContent("No budget set");
  });

  // 100.04 % arrives as percent 100 with overBudget true: the badge follows the
  // server's boolean, never the rounded number.
  it("marks the project over budget on the server's word rather than on the rounded percentage", async () => {
    stubEconomy(project(), plan([milestone()]), 200, {
      economy: economy({ overBudget: true, budgetUsed: { basis: "amount", percent: 100, approvedPercent: 80 } }),
    });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    const headline = await screen.findByTestId("budget-headline");
    expect(within(headline).getByText("Over budget")).toBeInTheDocument();
  });

  it("gives someone who cannot see the amounts the hours view instead of a locked note", async () => {
    const fetchMock = stubEconomy(
      project({
        capabilities: {
          canManage: false,
          canContribute: true,
          canSeeFinancials: false,
          canManageMilestones: false,
          canSeeCosts: false,
        },
        financials: undefined,
      }),
      plan([milestone()]),
      200,
      {
        economy: economy({
          currency: undefined,
          budget: { hours: 400 },
          actuals: work(210, 62, 40),
          budgetUsed: { basis: "hours", percent: 78, approvedPercent: 52.5 },
          milestones: undefined,
          lines: [line({ actuals: work(100, 0, 0) })],
        }),
      },
    );
    renderWithProviders(<ProjectEconomy projectId={7} />);

    expect(await screen.findByText("78 % of 400 hours")).toBeInTheDocument();
    expect(within(screen.getByTestId("project-budget-bar")).getByRole("img")).toHaveAccessibleName(
      "312 h of 400 h used: 210 h approved, 62 h submitted, 40 h draft.",
    );
    expect(await rowFor("DEV")).toHaveTextContent("50 %");
    // The invoice plan alone stays behind financial rights, and is not asked for.
    expect(screen.getByTestId("empty-state")).toHaveTextContent("invoice plan");
    expect(fetchMock.actualCalls.some(([url]) => String(url).includes("/milestones"))).toBe(false);
  });

  it("notes the hours that carry no rate, which the amounts leave out", async () => {
    stubEconomy(project(), plan([milestone()]), 200, {
      economy: economy({ actuals: work(210, 62, 40, { unpricedHours: 12, totalAmount: 360000 }) }),
    });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    expect(await screen.findByText("12 h have no rate and are not in the amounts.")).toBeInTheDocument();
  });

  it("says how many hours the margin leaves out, wherever it shows a margin", async () => {
    stubEconomy(project(), plan([milestone()]), 200, {
      economy: economy({
        cost: { approved: 120000, submitted: 60000, draft: 0, total: 180000, margin: 180000, uncostedHours: 12 },
      }),
    });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    const headline = await screen.findByTestId("budget-headline");
    expect(within(headline).getByText("Margin").parentElement).toHaveTextContent(money(180000));
    expect(screen.getByText("The margin leaves out 12 h with no cost.")).toBeInTheDocument();
  });

  // canSeeCosts is not enough: the block is also absent on a currencyless
  // project and without time tracking, so the panel follows the block itself.
  it("shows no margin at all when the server sent no cost block", async () => {
    stubEconomy(
      project({
        capabilities: {
          canManage: true,
          canContribute: true,
          canSeeFinancials: true,
          canManageMilestones: true,
          canSeeCosts: true,
        },
      }),
      plan([milestone()]),
      200,
      { economy: economy({ cost: undefined }) },
    );
    renderWithProviders(<ProjectEconomy projectId={7} />);

    await screen.findByTestId("budget-headline");
    expect(screen.queryByText("Margin")).not.toBeInTheDocument();
  });

  it("keeps the budgets and replaces the bars when time tracking is off", async () => {
    stubEconomy(project(), plan([milestone()]), 200, {
      economy: economy({
        timeTracking: false,
        actuals: undefined,
        budgetUsed: undefined,
        lines: [line({ actuals: undefined, usedPercent: undefined, remainingHours: undefined })],
      }),
    });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    expect(await screen.findByText("Hours appear here when Time tracking is enabled.")).toBeInTheDocument();
    expect(screen.queryByTestId("budget-bar")).not.toBeInTheDocument();
    // The budgets themselves are still the point of the tab.
    expect(await rowFor("DEV")).toHaveTextContent("200 h");
    // budgetUsed is absent for two different reasons; without time tracking it
    // means "nothing to compare with", not "nothing was budgeted".
    const headline = screen.getByTestId("budget-headline");
    expect(headline).toHaveTextContent(`${money(480000)} budgeted`);
    expect(headline).not.toHaveTextContent("No budget set");
  });

  it("names an hours budget when that is all the project has", async () => {
    stubEconomy(project(), plan([milestone()]), 200, {
      economy: economy({ timeTracking: false, actuals: undefined, budgetUsed: undefined, budget: { hours: 400 } }),
    });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    expect(await screen.findByText("400 h budgeted")).toBeInTheDocument();
  });

  it("names the fixed price when there is no budget amount", async () => {
    stubEconomy(project(), plan([milestone()]), 200, {
      economy: economy({
        timeTracking: false,
        actuals: undefined,
        budgetUsed: undefined,
        budget: { fixedPrice: 1000000 },
      }),
    });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    expect(await screen.findByText(`Fixed price ${money(1000000)}`)).toBeInTheDocument();
  });

  // The order is the server's: budget amount, then fixed price, then hours.
  it("puts the fixed price ahead of the hours budget", async () => {
    stubEconomy(project(), plan([milestone()]), 200, {
      economy: economy({
        timeTracking: false,
        actuals: undefined,
        budgetUsed: undefined,
        budget: { fixedPrice: 1000000, hours: 400 },
      }),
    });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    const headline = await screen.findByTestId("budget-headline");
    expect(within(headline).getByText("Budget used").parentElement).toHaveTextContent(`Fixed price ${money(1000000)}`);
    expect(within(headline).getByText("Budget used").parentElement).not.toHaveTextContent("400 h budgeted");
  });

  it("says nothing is budgeted only when nothing is", async () => {
    stubEconomy(project(), plan([milestone()]), 200, {
      economy: economy({ timeTracking: false, actuals: undefined, budgetUsed: undefined, budget: {} }),
    });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    expect(await screen.findByTestId("budget-headline")).toHaveTextContent("No budget set");
  });

  // Spec §8: a line without a budget shows its actuals and no bar — a bar with
  // nothing to measure against is full width, which reads as "100 % used".
  it("draws no bar on a line with no budget, and says the split in words instead", async () => {
    stubEconomy(project(), plan([milestone()]), 200, {
      economy: economy({
        lines: [
          line({
            budgetHours: undefined,
            budgetAmount: undefined,
            usedPercent: undefined,
            remainingHours: undefined,
            actuals: work(20, 10, 5),
          }),
        ],
      }),
    });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    const row = await rowFor("DEV");
    expect(within(row).queryByTestId("budget-bar")).not.toBeInTheDocument();
    expect(row).toHaveTextContent("35 h");
    expect(row).toHaveTextContent("20 h approved · 10 h submitted · 5 h draft");
  });

  it("lists every line, dims a deactivated one and puts the no-line row last", async () => {
    stubEconomy(project(), plan([milestone()]), 200, {
      economy: economy({
        lines: [
          line({ billingLineId: 5, code: "DEV" }),
          line({ billingLineId: 6, code: "OLD", active: false }),
          line({ billingLineId: undefined, code: undefined, active: undefined, budgetHours: undefined }),
        ],
      }),
    });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    await screen.findByText("DEV");
    const names = screen.getAllByTestId("economy-line-name").map((node) => node.textContent);
    expect(names).toEqual(["DEV", "OLD", "No billing line"]);
    expect(within(await rowFor("OLD")).getByText("Inactive")).toBeInTheDocument();
  });

  // A line's percentage can be measured in money while its remaining hours are
  // hours, so over budget and hours left over legitimately sit in one row.
  it("names each line's bar with the basis that line was measured on", async () => {
    stubEconomy(project(), plan([milestone()]), 200, {
      economy: economy({
        lines: [
          line({
            budgetAmount: 120000,
            budgetHours: 40,
            usedPercent: 110,
            remainingHours: 5,
            overBudget: true,
            actuals: work(35, 0, 0, {
              approved: { hours: 35, amount: 132000 },
              submitted: { hours: 0, amount: 0 },
              draft: { hours: 0, amount: 0 },
              totalAmount: 132000,
            }),
          }),
        ],
      }),
    });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    const row = await rowFor("DEV");
    expect(row).toHaveTextContent(`110 % of the budget (${money(120000)})`);
    expect(row).toHaveTextContent("5 h");
    // The bar's numbers live in its label; the column still owes a sighted
    // reader the hours.
    expect(row).toHaveTextContent("35 h");
    expect(within(row).getByText("Over budget")).toBeInTheDocument();
    expect(within(row).getByTestId("budget-bar")).toHaveAccessibleName(
      `${rawMoney(132000)} of ${rawMoney(120000)} used, over budget: ${rawMoney(132000)} approved, ${rawMoney(0)} submitted, ${rawMoney(0)} draft.`,
    );
  });

  it("names the lines table after the budget heading it sits under", async () => {
    stubEconomy(project(), plan([milestone()]), 200, {
      economy: economy({ lines: [line({ billingLineId: 5, code: "DEV" })] }),
    });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    expect(await screen.findByRole("table", { name: "Budget and logged work" })).toBeInTheDocument();
  });

  it("says what the lines' budgets add up to against the project's own", async () => {
    stubEconomy(project(), plan([milestone()]), 200, {
      economy: economy({ budget: { hours: 400, linesHours: 380, amount: 480000, fixedPrice: 1000000 } }),
    });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    expect(
      await screen.findByText("The billing lines' budgets add up to 380 h of the project's 400 h."),
    ).toBeInTheDocument();
  });

  it("puts the task estimates beside the budget", async () => {
    stubEconomy(project(), plan([milestone()]), 200, { economy: economy({ taskEstimateHours: 320 }) });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    expect(await screen.findByText("The tasks are estimated at 320 h.")).toBeInTheDocument();
  });

  it("reports a budget that cannot be read, and still shows the invoice plan", async () => {
    stubEconomy(project(), plan([milestone()]), 200, { economyStatus: 500 });
    renderWithProviders(<ProjectEconomy projectId={7} />);

    expect(await screen.findByText("Could not load the budget")).toBeInTheDocument();
    expect(await screen.findByText("Kick-off")).toBeInTheDocument();
  });
});
