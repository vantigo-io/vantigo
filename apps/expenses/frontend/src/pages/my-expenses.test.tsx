import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { problemResponse, sent } from "../test/api";
import { attachment, capabilities, ME, meta, mileage, outlay, stats, withReceipts } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";
import { stubExpensesApi } from "../test/server";

const row = async (description: string) =>
  (await screen.findByText(description)).closest("[data-expense]") as HTMLElement;

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
