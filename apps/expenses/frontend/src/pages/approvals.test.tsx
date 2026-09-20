import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { problemResponse, sent } from "../test/api";
import { capabilities, mileage, OTHER, outlay, projectOptions, submitted } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";
import { stubExpensesApi } from "../test/server";

const group = async (person: string) =>
  (await screen.findByText(person)).closest("[data-approval-group]") as HTMLElement;

const row = async (description: string) =>
  (await screen.findByText(description)).closest("[data-expense]") as HTMLElement;

const openDrawer = async (description: string) => {
  await userEvent.click(within(await row(description)).getByRole("button", { name: `Open ${description}` }));
  return screen.findByRole("dialog", { name: description });
};

describe("ApprovalsPage", () => {
  it("groups the queue per person, with their totals, receipts missing and replaced rates", async () => {
    stubExpensesApi({
      entries: [
        submitted({ id: 701, description: "Hotel", grossAmount: 2400, vatAmount: 0 }),
        mileage({
          id: 702,
          description: "Site visit",
          status: "submitted",
          owner: { userId: OTHER, displayName: "Grace Hopper", active: true },
          capabilities: capabilities({ canApprove: true, canOverrideRate: true }),
          rateOverride: { byUser: { userId: OTHER, displayName: "Grace Hopper", active: true }, tableValue: 5.3 },
        }),
      ],
    });
    renderRoute("/expenses/approvals");

    const card = await group("Grace Hopper");
    expect(card).toHaveTextContent("1 without a receipt");
    expect(card).toHaveTextContent("1 with a replaced rate");
    // Totals are per currency and nothing is converted.
    expect(card).toHaveTextContent("3,036.00");
    expect(within(card).getByRole("table", { name: "Grace Hopper's expenses" })).toBeInTheDocument();
    expect(within(await row("Hotel")).getByText("Missing")).toBeInTheDocument();
  });

  it("offers a checkbox only on the expenses the caller may approve", async () => {
    stubExpensesApi({
      entries: [
        submitted({ id: 701, description: "Hotel" }),
        submitted({ id: 702, description: "Somebody else's", capabilities: capabilities({ canApprove: false }) }),
      ],
    });
    renderRoute("/expenses/approvals");

    expect(within(await row("Hotel")).getByRole("checkbox")).toBeInTheDocument();
    expect(within(await row("Somebody else's")).queryByRole("checkbox")).not.toBeInTheDocument();
  });

  it("approves a selection picked across the queue", async () => {
    const fetchMock = stubExpensesApi({
      entries: [submitted({ id: 701, description: "Hotel" }), submitted({ id: 702, description: "Taxi" })],
    });
    renderRoute("/expenses/approvals");

    await userEvent.click(within(await row("Hotel")).getByRole("checkbox"));
    await userEvent.click(within(await row("Taxi")).getByRole("checkbox"));
    await userEvent.click(await screen.findByRole("button", { name: "Approve 2 selected" }));

    await waitFor(() =>
      expect(sent(fetchMock, "POST")).toEqual({ url: "/api/v1/expenses/approve", body: { entryIds: [701, 702] } }),
    );
    expect(await screen.findByText("Approved")).toBeInTheDocument();
  });

  it("puts a batch refusal against the row it names and moves nothing", async () => {
    stubExpensesApi({
      entries: [submitted({ id: 701, description: "Hotel" }), submitted({ id: 702, description: "Taxi" })],
      write: (method, path) =>
        method === "POST" && path === "/api/v1/expenses/approve"
          ? problemResponse(400, "Invalid approval", {
              entryIds: ["Expense 702 is dated before 2026-04-01, the lock date"],
            })
          : undefined,
    });
    renderRoute("/expenses/approvals");

    await userEvent.click(within(await row("Hotel")).getByRole("checkbox"));
    await userEvent.click(within(await row("Taxi")).getByRole("checkbox"));
    await userEvent.click(await screen.findByRole("button", { name: "Approve 2 selected" }));

    expect(await within(await row("Taxi")).findByText(/the lock date/)).toBeInTheDocument();
    expect(within(await row("Hotel")).queryByText(/the lock date/)).not.toBeInTheDocument();
  });

  it("rejects from the drawer with a reason the server is given", async () => {
    const fetchMock = stubExpensesApi({ entries: [submitted({ id: 701, description: "Hotel" })] });
    renderRoute("/expenses/approvals");

    const drawer = await openDrawer("Hotel");
    await userEvent.click(within(drawer).getByRole("button", { name: "Reject" }));
    const dialog = await screen.findByRole("dialog", { name: "Send the expenses back?" });
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Reason" }), "The receipt is unreadable");
    await userEvent.click(within(dialog).getByRole("button", { name: "Reject" }));

    await waitFor(() =>
      expect(sent(fetchMock, "POST")).toEqual({
        url: "/api/v1/expenses/reject",
        body: { entryIds: [701], reason: "The receipt is unreadable" },
      }),
    );
  });

  it("refuses to reject without a reason", async () => {
    const fetchMock = stubExpensesApi({ entries: [submitted({ id: 701, description: "Hotel" })] });
    renderRoute("/expenses/approvals");

    const drawer = await openDrawer("Hotel");
    await userEvent.click(within(drawer).getByRole("button", { name: "Reject" }));
    const dialog = await screen.findByRole("dialog", { name: "Send the expenses back?" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Reject" }));

    expect(await within(dialog).findByText("Write why the expenses are sent back")).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([url]) => String(url).endsWith("/reject"))).toBe(false);
  });

  it("replaces a mileage rate, showing what the table said and carrying the drawer's revision", async () => {
    const line = mileage({
      id: 702,
      description: "Site visit",
      status: "submitted",
      revision: 3,
      passengers: 2,
      passengerRate: 1,
      grossAmount: 876,
      owner: { userId: OTHER, displayName: "Grace Hopper", active: true },
      capabilities: capabilities({ canApprove: true, canOverrideRate: true }),
    });
    const fetchMock = stubExpensesApi({ entries: [line] });
    renderRoute("/expenses/approvals");

    const drawer = await openDrawer("Site visit");
    await userEvent.click(within(drawer).getByRole("button", { name: "Replace the rate" }));
    const dialog = await screen.findByRole("dialog", { name: "Replace the mileage rate" });

    expect(within(dialog).getByTestId("rate-change")).toHaveTextContent("5.30");
    const rate = within(dialog).getByRole("textbox", { name: "Rate per kilometre" });
    await userEvent.clear(rate);
    await userEvent.type(rate, "6");
    expect(within(dialog).getByTestId("rate-change")).toHaveTextContent("6.00");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT").url).toBe("/api/v1/expenses/entries/702/rate"));
    expect(sent(fetchMock, "PUT").body).toMatchObject({ rate: 6, revision: 3, passengerRate: 1 });
  });

  it("prices an expense from its project's side without ever touching the expense itself", async () => {
    const priceable = submitted({
      id: 701,
      description: "Hotel",
      revision: 5,
      billable: true,
      netAmount: 2000,
      grossAmount: 2000,
      project: { id: 1001, code: "KVEM1000", name: "Kverneland web" },
      capabilities: capabilities({ canApprove: true, canSetBilling: true, canSeeBilling: true }),
      billing: { billAmount: 0 },
    });
    const fetchMock = stubExpensesApi({ entries: [priceable], projects: projectOptions });
    renderRoute("/expenses/approvals");

    const drawer = await openDrawer("Hotel");
    await userEvent.click(within(drawer).getByRole("button", { name: "Price for the customer" }));
    const dialog = await screen.findByRole("dialog", { name: "What the customer is billed" });
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Markup" }), "20");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT").url).toBe("/api/v1/expenses/entries/701/billing"));
    expect(sent(fetchMock, "PUT").body).toMatchObject({ billable: true, markupPercent: 20, revision: 5 });
    expect(await screen.findByText(/The customer is billed/)).toBeInTheDocument();
  });

  it("takes an approval back from the approved half, which the URL remembers", async () => {
    const fetchMock = stubExpensesApi({
      entries: [
        outlay({
          id: 501,
          status: "approved",
          description: "Hotel",
          capabilities: capabilities({ canUnapprove: true }),
        }),
      ],
    });
    const { router } = renderRoute("/expenses/approvals");

    await screen.findByRole("radio", { name: "Waiting" });
    await userEvent.click(screen.getByRole("radio", { name: "Approved" }));
    await waitFor(() => expect(router.state.location.search).toMatchObject({ state: "approved", page: 1 }));

    const drawer = await openDrawer("Hotel");
    await userEvent.click(within(drawer).getByRole("button", { name: "Take the approval back" }));

    await waitFor(() =>
      expect(sent(fetchMock, "POST")).toEqual({ url: "/api/v1/expenses/unapprove", body: { entryIds: [501] } }),
    );
  });

  it("says so, rather than failing, when the caller approves nothing at all", async () => {
    stubExpensesApi({ entries: [], approvals: new Response(null, { status: 403 }) });
    renderRoute("/expenses/approvals");

    expect(await screen.findByText("You approve nobody's expenses")).toBeInTheDocument();
  });

  it("offers the lines of the expense's own project, to a pricer who books nowhere", async () => {
    // GET /projects would answer nothing at all for this caller — pricing is a
    // different right from booking, and the dialog is judged by the pricing one.
    const priceable = submitted({
      id: 701,
      description: "Hotel",
      billable: true,
      netAmount: 2000,
      grossAmount: 2000,
      project: { id: 1001, code: "KVEM1000", name: "Kverneland web" },
      capabilities: capabilities({ canApprove: true, canSetBilling: true, canSeeBilling: true }),
      billing: { billAmount: 0 },
    });
    const fetchMock = stubExpensesApi({
      entries: [priceable],
      projects: [],
      billingLines: [
        { id: 3001, code: "PM", active: true },
        { id: 3002, code: "DEV", active: true },
      ],
    });
    renderRoute("/expenses/approvals");

    const drawer = await openDrawer("Hotel");
    await userEvent.click(within(drawer).getByRole("button", { name: "Price for the customer" }));
    const dialog = await screen.findByRole("dialog", { name: "What the customer is billed" });

    const line = within(dialog).getByRole("combobox", { name: "Line" });
    expect(line).toBeEnabled();
    await userEvent.click(line);
    expect(await screen.findByRole("option", { name: "DEV" })).toBeInTheDocument();
    expect(fetchMock.actualCalls.some(([url]) => String(url).endsWith("/entries/701/billing-lines"))).toBe(true);
  });

  it("shows the line the expense already carries even once the project has dropped it", async () => {
    const priceable = submitted({
      id: 701,
      description: "Hotel",
      revision: 5,
      billable: true,
      netAmount: 2000,
      grossAmount: 2000,
      project: { id: 1001, code: "KVEM1000", name: "Kverneland web" },
      billingLine: { id: 3009, code: "OLD" },
      capabilities: capabilities({ canApprove: true, canSetBilling: true, canSeeBilling: true }),
      billing: { billAmount: 0 },
    });
    const fetchMock = stubExpensesApi({
      entries: [priceable],
      projects: [],
      billingLines: [
        { id: 3001, code: "PM", active: true },
        { id: 3009, code: "OLD", active: false },
      ],
    });
    renderRoute("/expenses/approvals");

    const drawer = await openDrawer("Hotel");
    await userEvent.click(within(drawer).getByRole("button", { name: "Price for the customer" }));
    const dialog = await screen.findByRole("dialog", { name: "What the customer is billed" });

    await waitFor(() => expect(within(dialog).getByRole("combobox", { name: "Line" })).toHaveValue("OLD (inactive)"));
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    // A line nobody touched is echoed back exactly as it was loaded.
    await waitFor(() => expect(sent(fetchMock, "PUT").url).toBe("/api/v1/expenses/entries/701/billing"));
    expect(sent(fetchMock, "PUT").body).toMatchObject({ billingLineId: 3009, revision: 5 });
  });

  it("carries the revision the first write answered into the second one in the same drawer", async () => {
    // The drawer's whole design is one open, several writes. A refactor that
    // read the revision off the row instead of the last answer would make every
    // second action a 409, which the fake reproduces.
    const line = mileage({
      id: 702,
      description: "Site visit",
      status: "submitted",
      revision: 3,
      billable: true,
      project: { id: 1001, code: "KVEM1000", name: "Kverneland web" },
      owner: { userId: OTHER, displayName: "Grace Hopper", active: true },
      capabilities: capabilities({ canOverrideRate: true, canSetBilling: true, canSeeBilling: true }),
      billing: { billAmount: 0 },
    });
    const fetchMock = stubExpensesApi({ entries: [line], billingLines: [{ id: 3001, code: "PM", active: true }] });
    renderRoute("/expenses/approvals");

    const drawer = await openDrawer("Site visit");
    await userEvent.click(within(drawer).getByRole("button", { name: "Replace the rate" }));
    const override = await screen.findByRole("dialog", { name: "Replace the mileage rate" });
    const rate = within(override).getByRole("textbox", { name: "Rate per kilometre" });
    await userEvent.clear(rate);
    await userEvent.type(rate, "6");
    await userEvent.click(within(override).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT").url).toBe("/api/v1/expenses/entries/702/rate"));

    await userEvent.click(await within(drawer).findByRole("button", { name: "Price for the customer" }));
    const pricing = await screen.findByRole("dialog", { name: "What the customer is billed" });
    await userEvent.type(within(pricing).getByRole("textbox", { name: "Customer rate per kilometre" }), "9");
    await userEvent.click(within(pricing).getByRole("button", { name: "Save" }));

    await waitFor(() => {
      const [, init] = fetchMock.actualCalls.find(([url]) => String(url).endsWith("/entries/702/billing")) ?? [];
      // 3 was the opened revision; the override answered 4, and that is what
      // the pricing has to carry — anything else is the 409 the fake answers.
      expect(JSON.parse(String(init?.body))).toMatchObject({ revision: 4 });
    });
    expect(await screen.findByText(/The customer is billed/)).toBeInTheDocument();
  });

  it("keeps the passenger supplement the line was frozen with when the field is left empty", async () => {
    const line = mileage({
      id: 702,
      description: "Site visit",
      status: "submitted",
      passengers: 2,
      passengerRate: 1,
      grossAmount: 876,
      owner: { userId: OTHER, displayName: "Grace Hopper", active: true },
      capabilities: capabilities({ canOverrideRate: true }),
    });
    const fetchMock = stubExpensesApi({ entries: [line] });
    renderRoute("/expenses/approvals");

    const drawer = await openDrawer("Site visit");
    await userEvent.click(within(drawer).getByRole("button", { name: "Replace the rate" }));
    const dialog = await screen.findByRole("dialog", { name: "Replace the mileage rate" });

    await userEvent.clear(within(dialog).getByRole("textbox", { name: "Passenger supplement per kilometre" }));

    // The request omits it and the server keeps 1.00, so the preview says the
    // supplement becomes 1.00 rather than promising a change to nothing.
    const preview = (within(dialog).getByTestId("passenger-rate-change").textContent ?? "").replace(/\u00a0/g, " ");
    expect(preview).toMatch(/was .*1\.00.* becomes .*1\.00/);
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT").url).toBe("/api/v1/expenses/entries/702/rate"));
    expect(sent(fetchMock, "PUT").body).not.toHaveProperty("passengerRate");
  });

  it("names the column the approval checkboxes sit in", async () => {
    stubExpensesApi({ entries: [submitted({ id: 701, description: "Hotel" })] });
    renderRoute("/expenses/approvals");

    const table = await screen.findByRole("table", { name: "Grace Hopper's expenses" });
    expect(within(table).getByRole("columnheader", { name: "Select" })).toBeInTheDocument();
  });

  it("says when an approval was decided even when nobody is named", async () => {
    stubExpensesApi({
      entries: [
        outlay({
          id: 501,
          status: "approved",
          description: "Hotel",
          decision: { status: "approved", at: "2026-09-19T12:00:00Z" },
          capabilities: capabilities({ canUnapprove: true }),
        }),
      ],
    });
    renderRoute("/expenses/approvals");

    await screen.findByRole("radio", { name: "Waiting" });
    await userEvent.click(screen.getByRole("radio", { name: "Approved" }));

    const drawer = await openDrawer("Hotel");
    expect(within(drawer).getByText(/Approved on/)).toBeInTheDocument();
  });
});
