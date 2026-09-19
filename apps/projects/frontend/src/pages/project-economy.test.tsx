import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
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

const project = (overrides: Partial<Project> = {}): Project =>
  ({
    id: 7,
    code: "KVEWEBS",
    name: "Website",
    status: "active",
    billingType: "fixed-price",
    internal: false,
    capabilities: { canManage: true, canContribute: true, canSeeFinancials: true, canManageMilestones: true },
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
  cancelled: 0,
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

const stubEconomy = (row: Project, body: BillingMilestonePlan | null = plan([milestone()]), status = 200) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/projects/7") return Promise.resolve(jsonResponse(200, row));
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
        capabilities: { canManage: false, canContribute: false, canSeeFinancials: false, canManageMilestones: false },
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

  it("sums the plan into the three headline figures", async () => {
    stubEconomy(project(), plan([milestone()], { planned: 300000, ready: 200000, invoiced: 100000 }));
    renderWithProviders(<ProjectEconomy projectId={7} />);

    const headline = await screen.findByTestId("milestone-totals");
    expect(headline).toHaveTextContent(money(300000));
    expect(headline).toHaveTextContent(money(200000));
    expect(headline).toHaveTextContent(money(100000));
    expect(within(headline).getByText("Ready to invoice")).toBeInTheDocument();
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
        capabilities: { canManage: false, canContribute: false, canSeeFinancials: true, canManageMilestones: false },
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
    await userEvent.click(screen.getByRole("button", { name: "Milestone actions" }));

    expect(await screen.findByRole("menuitem", { name: "Mark as invoiced" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Edit" })).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Move up" })).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Cancel the milestone" })).not.toBeInTheDocument();
  });

  it("offers a manager everything the milestone's capabilities allow", async () => {
    stubEconomy(project(), plan([milestone()]));
    renderWithProviders(<ProjectEconomy projectId={7} />);

    await screen.findByText("Kick-off");
    await userEvent.click(screen.getByRole("button", { name: "Milestone actions" }));

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

    const launch = await rowFor("Launch");
    await userEvent.click(within(launch).getByRole("button", { name: "Milestone actions" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Move up" }));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith("/api/v1/projects/milestones/2/position", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ position: 1, revision: 4 }),
      }),
    );

    const kickoff = await rowFor("Kick-off");
    await userEvent.click(within(kickoff).getByRole("button", { name: "Milestone actions" }));
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

    const only = await rowFor("Kick-off");
    await userEvent.click(within(only).getByRole("button", { name: "Milestone actions" }));
    expect(await screen.findByRole("menuitem", { name: "Edit" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Move up" })).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Move down" })).not.toBeInTheDocument();
    await userEvent.keyboard("{Escape}");

    const cancelled = screen.getByText("Handover").closest("tr") as HTMLElement;
    await userEvent.click(within(cancelled).getByRole("button", { name: "Milestone actions" }));
    expect(await screen.findByRole("menuitem", { name: "Reopen the milestone" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Move up" })).not.toBeInTheDocument();
  });

  it("marks a milestone ready straight from the row", async () => {
    const fetchMock = stubEconomy(project(), plan([milestone({ revision: 2 })]));
    renderWithProviders(<ProjectEconomy projectId={7} />);

    await screen.findByText("Kick-off");
    await userEvent.click(screen.getByRole("button", { name: "Milestone actions" }));
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
    await userEvent.click(screen.getByRole("button", { name: "Milestone actions" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Mark as ready to invoice" }));

    expect(
      await screen.findByText("A milestone cannot be marked ready while the project has no currency"),
    ).toBeInTheDocument();
  });

  it("translates a revision conflict rather than quoting the project's own wording", async () => {
    stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname === "/api/v1/projects/7") return Promise.resolve(jsonResponse(200, project()));
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
    await userEvent.click(screen.getByRole("button", { name: "Milestone actions" }));
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
});
