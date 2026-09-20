import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { problemResponse, sent } from "../test/api";
import {
  attachment,
  capabilities,
  claim,
  ME,
  meta,
  mileage,
  outlay,
  perDiemLine,
  stats,
  withReceipts,
} from "../test/fixtures";
import { renderRoute } from "../test/route-tree";
import { stubExpensesApi } from "../test/server";

const row = async (description: string) =>
  (await screen.findByText(description)).closest("[data-expense]") as HTMLElement;

const claimRow = async (purpose: string) => (await screen.findByText(purpose)).closest("[data-claim]") as HTMLElement;

/** Picks an option from a Mantine select inside a dialog. */
const choose = async (dialog: HTMLElement, label: string, option: RegExp | string) => {
  await userEvent.click(within(dialog).getByRole("combobox", { name: label }));
  await userEvent.click(await screen.findByRole("option", { name: option }));
};

describe("MyExpensesPage", () => {
  it("lists only the caller's own expenses, with their kind, status and receipts", async () => {
    const fetchMock = stubExpensesApi({
      entries: [
        withReceipts(outlay(), [attachment({ fileName: "taxi.jpg" })]),
        mileage({ id: 601, description: "Site visit" }),
      ],
      stats: stats({ draft: 2 }),
    });
    renderRoute("/expenses");

    const taxi = await row("Taxi to the airport");
    expect(taxi).toHaveTextContent("Draft");
    expect(within(taxi).getByRole("img", { name: "taxi.jpg" })).toBeInTheDocument();
    expect(await row("Site visit")).toHaveTextContent("Mileage");

    // "My expenses" is the caller's own: it never widens for somebody who may
    // also see everybody else's.
    const [url] = fetchMock.actualCalls.find(([candidate]) => String(candidate).includes("/entries?")) ?? [];
    expect(String(url)).toContain(`userId=${ME}`);
  });

  it("shows the three figures of the strip, with what is owed per currency", async () => {
    stubExpensesApi({
      entries: [],
      stats: stats({
        draft: 3,
        submitted: 2,
        unreimbursed: [
          { currency: "NOK", amount: 1250 },
          { currency: "EUR", amount: 40 },
        ],
      }),
    });
    renderRoute("/expenses");

    await waitFor(() => expect(screen.getByTestId("expenses-drafts")).toHaveTextContent("3"));
    expect(screen.getByTestId("expenses-submitted")).toHaveTextContent("2");
    const owed = screen.getByTestId("expenses-owed");
    expect(owed).toHaveTextContent("1,250.00");
    expect(owed).toHaveTextContent("40.00");
  });

  it("keeps every filter in the URL and starts the list again at the first page", async () => {
    stubExpensesApi({ entries: [outlay(), mileage({ id: 601, description: "Site visit" })], pageSize: 1 });
    const { router } = renderRoute("/expenses?page=2");

    await screen.findByText("Taxi to the airport");
    await userEvent.click(screen.getByRole("combobox", { name: "Kind" }));
    await userEvent.click(await screen.findByRole("option", { name: "Mileage" }));

    await waitFor(() => expect(router.state.location.search).toMatchObject({ kind: "mileage", page: 1 }));
    expect(await screen.findByText("Site visit")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByText("Taxi to the airport")).not.toBeInTheDocument());
  });

  it("says why an expense came back and who sent it back", async () => {
    stubExpensesApi({
      entries: [
        outlay({
          status: "rejected",
          capabilities: capabilities({ canEdit: true, canDelete: true, canSubmit: true }),
          decision: {
            status: "rejected",
            at: "2026-09-19T12:00:00Z",
            by: { userId: "33333333-3333-3333-3333-333333333333", displayName: "Grace Hopper", active: true },
            reason: "The receipt is unreadable",
          },
        }),
      ],
    });
    renderRoute("/expenses");

    const taxi = await row("Taxi to the airport");
    expect(taxi).toHaveTextContent("Rejected: The receipt is unreadable");
    expect(taxi).toHaveTextContent("Grace Hopper");
  });

  it("submits one expense from its row", async () => {
    const fetchMock = stubExpensesApi({ entries: [outlay()] });
    renderRoute("/expenses");

    const taxi = await row("Taxi to the airport");
    await userEvent.click(within(taxi).getByRole("button", { name: "Submit Taxi to the airport" }));

    await waitFor(() =>
      expect(sent(fetchMock, "POST")).toEqual({ url: "/api/v1/expenses/submit", body: { entryIds: [501] } }),
    );
    expect(await screen.findByText("Sent for approval")).toBeInTheDocument();
  });

  it("submits a selection and puts each refusal against the row it names", async () => {
    stubExpensesApi({
      entries: [outlay(), outlay({ id: 502, description: "Hotel", grossAmount: 2400 })],
      write: (method, path) =>
        method === "POST" && path === "/api/v1/expenses/submit"
          ? problemResponse(400, "Invalid submission", {
              entryIds: [
                "Expense 502 needs a receipt: an outlay the employee paid for more than 1250.00 cannot be submitted without one",
              ],
            })
          : undefined,
    });
    renderRoute("/expenses");

    await userEvent.click(within(await row("Taxi to the airport")).getByRole("checkbox"));
    await userEvent.click(within(await row("Hotel")).getByRole("checkbox"));
    await userEvent.click(await screen.findByRole("button", { name: "Submit 2 selected" }));

    expect(await within(await row("Hotel")).findByText(/needs a receipt/)).toBeInTheDocument();
    expect(within(await row("Taxi to the airport")).queryByText(/needs a receipt/)).not.toBeInTheDocument();
  });

  it("lists the travel claims beside the single expenses, and asks for the expenses that are units of their own", async () => {
    const fetchMock = stubExpensesApi({
      claims: [claim()],
      entries: [outlay(), perDiemLine()],
    });
    renderRoute("/expenses");

    const trip = await claimRow("Montasje hos kunden");
    expect(trip).toHaveTextContent("Bergen");
    expect(trip).toHaveTextContent("1 expense");
    expect(trip).toHaveTextContent("1,012.00");
    expect(trip).toHaveTextContent("Draft");

    // The trip's own line is the trip's, and is never listed a second time as
    // a loose expense.
    const [url] = fetchMock.actualCalls.find(([candidate]) => String(candidate).includes("/entries?")) ?? [];
    expect(String(url)).toContain("standalone=true");
    expect(await screen.findByRole("table", { name: "My expenses" })).not.toHaveTextContent("Overnight, hotel");
  });

  it("submits a trip and a single expense in one request, and puts each refusal on its own unit", async () => {
    stubExpensesApi({
      claims: [claim()],
      entries: [outlay(), perDiemLine()],
      write: (method, path) =>
        method === "POST" && path === "/api/v1/expenses/submit"
          ? problemResponse(400, "Invalid submission", {
              entryIds: ["Expense 501 needs a receipt"],
              claimIds: ["Travel claim 1012 holds no expenses, so there is nothing to submit"],
            })
          : undefined,
    });
    renderRoute("/expenses");

    await userEvent.click(within(await claimRow("Montasje hos kunden")).getByRole("checkbox"));
    await userEvent.click(within(await row("Taxi to the airport")).getByRole("checkbox"));
    await userEvent.click(await screen.findByRole("button", { name: "Submit 2 selected" }));

    // Both lists are read: a trip's refusal used to be fetched and dropped.
    expect(await within(await claimRow("Montasje hos kunden")).findByText(/holds no expenses/)).toBeInTheDocument();
    expect(await within(await row("Taxi to the airport")).findByText(/needs a receipt/)).toBeInTheDocument();
  });

  it("sends one request for both kinds of unit", async () => {
    const fetchMock = stubExpensesApi({ claims: [claim()], entries: [outlay()] });
    renderRoute("/expenses");

    await userEvent.click(within(await claimRow("Montasje hos kunden")).getByRole("checkbox"));
    await userEvent.click(within(await row("Taxi to the airport")).getByRole("checkbox"));
    await userEvent.click(await screen.findByRole("button", { name: "Submit 2 selected" }));

    await waitFor(() =>
      expect(sent(fetchMock, "POST")).toEqual({
        url: "/api/v1/expenses/submit",
        body: { entryIds: [501], claimIds: [1012] },
      }),
    );
  });

  it("records a travel claim and opens it", async () => {
    const fetchMock = stubExpensesApi({ entries: [] });
    const { router } = renderRoute("/expenses");

    await screen.findByRole("table", { name: "My expenses" });
    await userEvent.click(screen.getByRole("button", { name: "New travel claim" }));
    const dialog = await screen.findByRole("dialog", { name: "New travel claim" });

    await userEvent.type(within(dialog).getByRole("textbox", { name: "What the trip was for" }), "Montasje");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Where it went" }), "Bergen");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Day of departure" }), "Mar 9, 2026");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Day of return" }), "Mar 11, 2026");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "POST").url).toBe("/api/v1/expenses/claims"));
    expect(sent(fetchMock, "POST").body).toMatchObject({
      purpose: "Montasje",
      destination: "Bergen",
      abroad: false,
      departureAt: "2026-03-09T08:00:00+01:00",
      returnAt: "2026-03-11T16:00:00+01:00",
    });
    // The trip exists, so the traveller is taken to it rather than left on a
    // list with an empty row on it.
    await waitFor(() => expect(router.state.location.pathname).toBe("/expenses/claims/9001"));
  });

  it("opens the trip form on arrival when the URL asks for it, and drops the parameter again", async () => {
    // The host's Spotlight has a "New travel claim" quick action and no button
    // of this page to press, so it lands here with `?create=claim` — the same
    // seam every other app's create action uses.
    stubExpensesApi({ entries: [] });
    const { router } = renderRoute("/expenses?create=claim");

    const dialog = await screen.findByRole("dialog", { name: "New travel claim" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));

    // Consumed once: a refresh must not reopen a form nobody asked for again.
    await waitFor(() => expect(router.state.location.searchStr).not.toContain("create"));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "New travel claim" })).not.toBeInTheDocument());
  });

  it("records a new outlay from the form and shows it in the list", async () => {
    const fetchMock = stubExpensesApi({ entries: [outlay()], meta: meta({ projectsAvailable: false }) });
    renderRoute("/expenses");

    await screen.findByRole("table", { name: "My expenses" });
    await userEvent.click(screen.getByRole("button", { name: "New expense" }));
    const dialog = await screen.findByRole("dialog", { name: "New expense" });

    await userEvent.type(within(dialog).getByRole("textbox", { name: "Description" }), "Train ticket");
    await choose(dialog, "Category", "Travel");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Amount including VAT" }), "420");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save draft" }));

    await waitFor(() => expect(sent(fetchMock, "POST").url).toBe("/api/v1/expenses/entries"));
    expect(sent(fetchMock, "POST").body).toMatchObject({
      kind: "outlay",
      entryDate: expect.stringMatching(/^\d{4}-\d{2}-\d{2}$/),
      description: "Train ticket",
      categoryId: 11,
      grossAmount: 420,
      paidBy: "employee",
      currency: "NOK",
    });
    expect(await screen.findByText("Train ticket")).toBeInTheDocument();
  });
});
