import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { jsonResponse, sent } from "../test/api";
import { APPROVER, capabilities, OTHER, outlay } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";
import { stubExpensesApi } from "../test/server";

const owed = (overrides = {}) =>
  outlay({
    id: 501,
    status: "approved",
    description: "Hotel",
    grossAmount: 2400,
    vatAmount: 0,
    netAmount: 2400,
    owedToEmployee: 2400,
    owner: { userId: OTHER, displayName: "Grace Hopper", active: true },
    capabilities: capabilities({ canMarkReimbursed: true }),
    ...overrides,
  });

const row = async (description: string) =>
  (await screen.findByText(description)).closest("[data-expense]") as HTMLElement;

beforeEach(() => {
  // jsdom implements neither, and the download hands the blob to the browser.
  URL.createObjectURL = vi.fn(() => "blob:receipt");
  URL.revokeObjectURL = vi.fn();
  HTMLAnchorElement.prototype.click = vi.fn();
});

describe("ReimbursementsPage", () => {
  it("groups what is owed per person with their totals per currency", async () => {
    stubExpensesApi({
      entries: [owed(), owed({ id: 502, description: "Taxi", owedToEmployee: 100, grossAmount: 100 })],
    });
    renderRoute("/expenses/reimbursements");

    const card = (await screen.findByText("Grace Hopper")).closest("[data-reimbursement-group]") as HTMLElement;
    expect(card).toHaveTextContent("2,500.00");
    expect(within(card).getByRole("table", { name: "Grace Hopper's expenses" })).toBeInTheDocument();
  });

  it("records a payroll run for the selection, with the day and the reference", async () => {
    const fetchMock = stubExpensesApi({ entries: [owed()] });
    renderRoute("/expenses/reimbursements");

    await userEvent.click(within(await row("Hotel")).getByRole("checkbox"));
    await userEvent.click(await screen.findByRole("button", { name: "Mark 1 as paid back" }));
    const dialog = await screen.findByRole("dialog", { name: "Record a payroll run" });
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Payroll reference" }), "LØNN-2026-09");
    await userEvent.click(within(dialog).getByRole("button", { name: "Mark as paid back" }));

    await waitFor(() => expect(sent(fetchMock, "POST").url).toBe("/api/v1/expenses/reimbursed"));
    expect(sent(fetchMock, "POST").body).toMatchObject({
      entryIds: [501],
      reference: "LØNN-2026-09",
      date: expect.stringMatching(/^\d{4}-\d{2}-\d{2}$/),
    });
  });

  it("undoes a payment from the half that shows what has been paid", async () => {
    const fetchMock = stubExpensesApi({
      entries: [
        owed({
          reimbursement: { at: "2026-09-20T10:00:00Z", by: APPROVER, date: "2026-09-20", reference: "LØNN-2026-09" },
          capabilities: capabilities({ canUndoReimbursed: true }),
        }),
      ],
    });
    const { router } = renderRoute("/expenses/reimbursements");

    await screen.findByRole("radio", { name: "Waiting to be paid" });
    await userEvent.click(screen.getByRole("radio", { name: "Already paid" }));
    await waitFor(() => expect(router.state.location.search).toMatchObject({ state: "reimbursed", page: 1 }));

    const paid = await row("Hotel");
    expect(paid).toHaveTextContent("LØNN-2026-09");
    await userEvent.click(within(paid).getByRole("checkbox"));
    await userEvent.click(await screen.findByRole("button", { name: "Undo 1 payments" }));

    await waitFor(() =>
      expect(sent(fetchMock, "POST")).toEqual({
        url: "/api/v1/expenses/reimbursed/undo",
        body: { entryIds: [501] },
      }),
    );
  });

  it("exports everything waiting with the filters, and never with an empty selection", async () => {
    const fetchMock = stubExpensesApi({ entries: [owed()] });
    renderRoute("/expenses/reimbursements");

    await screen.findByText("Hotel");
    expect(screen.getByRole("button", { name: "Export 0 selected" })).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Export everything waiting" }));

    await waitFor(() => {
      const [url] = fetchMock.actualCalls.find(([candidate]) => String(candidate).includes("export.csv")) ?? [];
      expect(String(url)).toContain("state=waiting");
      expect(String(url)).not.toContain("entryIds");
    });
    expect(await screen.findByText("expenses-reimbursements-2026-09-20.csv")).toBeInTheDocument();
  });

  it("exports exactly the selection, and the filters are then left out", async () => {
    const fetchMock = stubExpensesApi({ entries: [owed()] });
    renderRoute("/expenses/reimbursements");

    await userEvent.click(within(await row("Hotel")).getByRole("checkbox"));
    await userEvent.click(await screen.findByRole("button", { name: "Export 1 selected" }));

    await waitFor(() => {
      const [url] = fetchMock.actualCalls.find(([candidate]) => String(candidate).includes("export.csv")) ?? [];
      expect(String(url)).toContain("entryIds=501");
      expect(String(url)).not.toContain("state=");
    });
  });

  it("shows the row-cap refusal, which carries no field errors at all", async () => {
    stubExpensesApi({
      entries: [owed()],
      csv: jsonResponse(400, {
        title: "Too many rows to export",
        status: 400,
        detail: "6000 rows would be exported; narrow the filter to at most 5000.",
      }),
    });
    renderRoute("/expenses/reimbursements");

    await screen.findByText("Hotel");
    await userEvent.click(screen.getByRole("button", { name: "Export everything waiting" }));

    expect(await screen.findByText(/narrow the filter/)).toBeInTheDocument();
  });

  it("says so, rather than failing, when the caller may not manage expenses", async () => {
    stubExpensesApi({ entries: [], reimbursements: new Response(null, { status: 403 }) });
    renderRoute("/expenses/reimbursements");

    expect(await screen.findByText("You cannot see what is owed back")).toBeInTheDocument();
  });

  it("names the column the payroll checkboxes sit in", async () => {
    stubExpensesApi({ entries: [owed()] });
    renderRoute("/expenses/reimbursements");

    const table = await screen.findByRole("table", { name: "Grace Hopper's expenses" });
    expect(within(table).getByRole("columnheader", { name: "Select" })).toBeInTheDocument();
  });
});
