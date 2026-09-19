import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { BillingLine, Project } from "../api/projects";
import { stubFetch } from "../test/fetch";
import { renderWithProviders } from "../test/render";
import { ProjectBilling } from "./project-billing";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/**
 * The same formatter the page uses, so the assertion does not hard-code a
 * locale's separators. The non-breaking space Intl puts after the currency is
 * normalised away, as jest-dom normalises the rendered text.
 */
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
    financials: { currency: "NOK", fixedPriceAmount: 250000, budgetAmount: 300000, defaultBillRate: 1250 },
    managers: [],
    revision: 3,
    createdAt: "2026-01-01T10:00:00Z",
    updatedAt: "2026-01-02T10:00:00Z",
    ...overrides,
  }) as Project;

const line = (overrides: Partial<BillingLine> = {}): BillingLine => ({
  id: 1,
  code: "PM",
  trackableCode: "KVEWEBS-PM",
  variantId: 31,
  variantMissing: false,
  productName: "Project manager hour",
  sku: "PM-H",
  unit: "hour",
  active: true,
  pricing: { mode: "list", listPrice: { amount: 1450, currency: "NOK" } },
  createdAt: "2026-01-01T10:00:00Z",
  updatedAt: "2026-01-01T10:00:00Z",
  ...overrides,
});

const stubBilling = (row: Project, lines: BillingLine[] = [line()]) =>
  stubFetch((input: RequestInfo | URL) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/v1/projects/7") return Promise.resolve(jsonResponse(200, row));
    if (url.pathname === "/api/v1/projects/7/billing-lines") return Promise.resolve(jsonResponse(200, lines));
    if (url.pathname === "/api/v1/products") return Promise.resolve(jsonResponse(200, { data: [] }));
    return Promise.resolve(new Response(null, { status: 404 }));
  });

describe("ProjectBilling", () => {
  it("tells someone who may not see the amounts why the tab is empty, and asks for nothing", async () => {
    const fetchMock = stubBilling(
      project({
        capabilities: { canManage: false, canContribute: false, canSeeFinancials: false, canManageMilestones: false },
        financials: undefined,
      }),
    );
    renderWithProviders(<ProjectBilling projectId={7} />);

    expect(await screen.findByText("You cannot see this project's amounts.")).toBeInTheDocument();
    expect(screen.getByTestId("empty-state")).toHaveTextContent("Ask a project manager");
    expect(fetchMock.actualCalls.some(([url]) => String(url).includes("billing-lines"))).toBe(false);
  });

  it("summarises the billing type and the amounts", async () => {
    stubBilling(project());
    renderWithProviders(<ProjectBilling projectId={7} />);

    const summary = await screen.findByTestId("financial-summary");
    expect(summary).toHaveTextContent("Fixed price");
    expect(summary).toHaveTextContent(money(250000));
    expect(summary).toHaveTextContent(money(300000));
    expect(summary).toHaveTextContent("NOK");
  });

  // The rate a time entry falls back to is only readable here: the project
  // form writes it, and nothing else on the tab shows what it stands at.
  it("shows the default bill rate in the project's currency", async () => {
    stubBilling(project());
    renderWithProviders(<ProjectBilling projectId={7} />);

    const summary = await screen.findByTestId("financial-summary");
    expect(within(summary).getByText("Default bill rate")).toBeInTheDocument();
    expect(summary).toHaveTextContent(money(1250));
  });

  it("says so when no default bill rate is set", async () => {
    stubBilling(project({ financials: { currency: "NOK" } }));
    renderWithProviders(<ProjectBilling projectId={7} />);

    const summary = await screen.findByTestId("financial-summary");
    const rate = within(summary).getByText("Default bill rate").closest("div") as HTMLElement;
    expect(rate).toHaveTextContent("—");
  });

  it("hides the lines section when the products module is off", async () => {
    const fetchMock = stubBilling(project({ billingLinesAvailable: false }));
    renderWithProviders(<ProjectBilling projectId={7} />);

    expect(
      await screen.findByText("Billing lines need the products module, which is not enabled."),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add billing line" })).not.toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([url]) => String(url).includes("billing-lines"))).toBe(false);
  });

  it("renders a line's trackable code, product, unit and list price", async () => {
    stubBilling(project());
    renderWithProviders(<ProjectBilling projectId={7} />);

    const row = (await screen.findByText("KVEWEBS-PM")).closest("tr") as HTMLElement;
    expect(within(row).getByText("Project manager hour")).toBeInTheDocument();
    expect(within(row).getByText("hour")).toBeInTheDocument();
    expect(within(row).getByText("List price")).toBeInTheDocument();
    expect(row).toHaveTextContent(money(1450));
    expect(within(row).getByText("Active")).toBeInTheDocument();
  });

  it("spells out a fixed amount and a discount as their own rules", async () => {
    stubBilling(project(), [
      line({ id: 2, code: "FIX", trackableCode: "KVEWEBS-FIX", pricing: { mode: "fixed", fixedAmount: 1450 } }),
      line({
        id: 3,
        code: "DIS",
        trackableCode: "KVEWEBS-DIS",
        active: false,
        pricing: { mode: "discount", discountPercent: 10, listPrice: { amount: 1450, currency: "NOK" } },
      }),
    ]);
    renderWithProviders(<ProjectBilling projectId={7} />);

    const fixed = (await screen.findByText("KVEWEBS-FIX")).closest("tr") as HTMLElement;
    expect(fixed).toHaveTextContent(`Fixed ${money(1450)}`);
    const discount = screen.getByText("KVEWEBS-DIS").closest("tr") as HTMLElement;
    expect(discount).toHaveTextContent("10 % off list price");
    expect(within(discount).getByText("Inactive")).toBeInTheDocument();
  });

  it("shows a line's planning budget, and whichever half of it is set", async () => {
    stubBilling(project(), [
      line({ budgetHours: 120, pricing: { mode: "list", budgetAmount: 96000 } }),
      line({ id: 2, code: "DEV", trackableCode: "KVEWEBS-DEV", budgetHours: 40, pricing: { mode: "list" } }),
      line({ id: 3, code: "QA", trackableCode: "KVEWEBS-QA", pricing: { mode: "list" } }),
    ]);
    renderWithProviders(<ProjectBilling projectId={7} />);

    const both = (await screen.findByText("KVEWEBS-PM")).closest("tr") as HTMLElement;
    expect(both).toHaveTextContent(`120 h · ${money(96000)}`);
    const hoursOnly = screen.getByText("KVEWEBS-DEV").closest("tr") as HTMLElement;
    expect(hoursOnly).toHaveTextContent("40 h");
    expect(hoursOnly).not.toHaveTextContent("·");
    const neither = screen.getByText("KVEWEBS-QA").closest("tr") as HTMLElement;
    expect(within(neither).getAllByText("—").length).toBeGreaterThan(0);
  });

  it("names a variant the catalog has forgotten", async () => {
    stubBilling(project(), [line({ variantMissing: true, productName: null, sku: null, unit: null })]);
    renderWithProviders(<ProjectBilling projectId={7} />);

    const row = (await screen.findByText("KVEWEBS-PM")).closest("tr") as HTMLElement;
    expect(within(row).getByText("Unknown product")).toBeInTheDocument();
  });

  it("offers no line actions to someone who may not manage the project", async () => {
    stubBilling(
      project({
        capabilities: { canManage: false, canContribute: false, canSeeFinancials: true, canManageMilestones: false },
      }),
    );
    renderWithProviders(<ProjectBilling projectId={7} />);

    await screen.findByText("KVEWEBS-PM");
    expect(screen.queryByRole("button", { name: "Add billing line" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit PM" })).not.toBeInTheDocument();
  });

  it("opens the form on an existing line for a manager", async () => {
    stubBilling(project());
    renderWithProviders(<ProjectBilling projectId={7} />);

    await screen.findByText("KVEWEBS-PM");
    await userEvent.click(screen.getByRole("button", { name: "Edit PM" }));

    const dialog = await screen.findByRole("dialog", { name: "Edit billing line" });
    await waitFor(() => expect(within(dialog).getByLabelText(/line code/i)).toHaveValue("PM"));
  });

  // Every line's edit button used to share the one name "Edit billing line",
  // which is unusable from a screen reader's list of buttons on a page with
  // more than one line — this pins that each is named after its own line.
  it("names each line's edit button after its own code", async () => {
    stubBilling(project(), [line(), line({ id: 2, code: "DEV", trackableCode: "KVEWEBS-DEV" })]);
    renderWithProviders(<ProjectBilling projectId={7} />);

    expect(await screen.findByRole("button", { name: "Edit PM" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Edit DEV" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit billing line" })).not.toBeInTheDocument();
  });

  it("says when the project has no lines yet", async () => {
    stubBilling(project(), []);
    renderWithProviders(<ProjectBilling projectId={7} />);

    expect(await screen.findByText("No billing lines yet.")).toBeInTheDocument();
  });
});
