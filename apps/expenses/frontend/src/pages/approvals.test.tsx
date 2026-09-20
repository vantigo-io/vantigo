import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { problemResponse, sent } from "../test/api";
import {
  APPROVER,
  capabilities,
  claim,
  claimCapabilities,
  ME,
  mileage,
  OTHER,
  outlay,
  perDiemLine,
  perDiemRates,
  projectOptions,
  rates,
  submitted,
} from "../test/fixtures";
import { renderRoute } from "../test/route-tree";
import { stubExpensesApi } from "../test/server";

const group = async (person: string) =>
  (await screen.findByText(person)).closest("[data-approval-group]") as HTMLElement;

const row = async (description: string) =>
  (await screen.findByText(description)).closest("[data-expense]") as HTMLElement;

const claimRow = async (purpose: string) => (await screen.findByText(purpose)).closest("[data-claim]") as HTMLElement;

const openDrawer = async (description: string) => {
  await userEvent.click(within(await row(description)).getByRole("button", { name: `Open ${description}` }));
  return screen.findByRole("dialog", { name: description });
};

const openClaimDrawer = async (purpose: string) => {
  await userEvent.click(within(await claimRow(purpose)).getByRole("button", { name: `Open ${purpose}` }));
  return screen.findByRole("dialog", { name: purpose });
};

const GRACE = { userId: OTHER, displayName: "Grace Hopper", active: true };

/**
 * A submitted trip of somebody else's, and its two lines: an outlay with no
 * receipt and a per diem day. A line's status and owner are the claim's, and
 * a line carries no approve of its own — the claim is the unit.
 */
const tripWithLines = (overrides: Parameters<typeof claim>[0] = {}) => ({
  trip: claim({
    id: 1012,
    status: "submitted",
    owner: GRACE,
    submittedAt: "2026-03-12T09:00:00Z",
    capabilities: claimCapabilities({ canApprove: true }),
    ...overrides,
  }),
  lines: [
    outlay({
      id: 801,
      claimId: 1012,
      description: "Hotel Bergen",
      status: "submitted",
      entryDate: "2026-03-09",
      owner: GRACE,
      grossAmount: 2400,
      vatAmount: 0,
      netAmount: 2400,
      owedToEmployee: 2400,
      capabilities: capabilities(),
    }),
    perDiemLine({ id: 802, claimId: 1012, status: "submitted", owner: GRACE, capabilities: capabilities() }),
  ],
});

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

  it("puts the person who has been waiting longest first, trips counted", async () => {
    // Paged by person, so the order is the queue's whole promise. A person
    // whose only unit is a trip takes their place in it like anybody else.
    const { trip, lines } = tripWithLines({ owner: GRACE });
    stubExpensesApi({
      entries: [
        submitted({
          id: 701,
          description: "Taxi",
          entryDate: "2026-05-02",
          owner: { userId: ME, displayName: "Ada Lovelace", active: true },
        }),
        ...lines,
      ],
      claims: [trip],
      rates: [...rates, ...perDiemRates],
    });
    renderRoute("/expenses/approvals");

    await screen.findByText("Grace Hopper");
    const people = [...document.querySelectorAll("[data-approval-group]")].map(
      (card) => card.querySelector("h5")?.textContent,
    );
    // Grace's trip departed on 9 March; Ada's taxi is from May.
    expect(people).toEqual(["Grace Hopper", "Ada Lovelace"]);
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

  it("approves from the drawer and closes it", async () => {
    // The drawer's success handler reads the answer — `{ entries, claims }` —
    // and only then saves what moved and closes. Asserting the request alone
    // would pass against a fake that answers the shape the operations had
    // before travel claims existed, while nothing after the notification ever
    // ran.
    const fetchMock = stubExpensesApi({ entries: [submitted({ id: 701, description: "Hotel" })] });
    renderRoute("/expenses/approvals");

    const drawer = await openDrawer("Hotel");
    await userEvent.click(within(drawer).getByRole("button", { name: "Approve" }));

    await waitFor(() =>
      expect(sent(fetchMock, "POST")).toEqual({ url: "/api/v1/expenses/approve", body: { entryIds: [701] } }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Hotel" })).not.toBeInTheDocument());
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

  it("lists a trip as one row of its own, and never its lines among the loose expenses", async () => {
    const { trip, lines } = tripWithLines();
    stubExpensesApi({
      entries: [submitted({ id: 701, description: "Taxi" }), ...lines],
      claims: [trip],
      rates: [...rates, ...perDiemRates],
    });
    renderRoute("/expenses/approvals");

    const card = await group("Grace Hopper");
    const trips = within(card).getByRole("table", { name: "Grace Hopper's travel claims" });
    const one = within(trips).getByText("Montasje hos kunden").closest("[data-claim]") as HTMLElement;
    expect(one).toHaveTextContent("Bergen");
    expect(one).toHaveTextContent("2 expenses");
    // The trip's own figures, per currency: 2 400 + 1 012.
    expect(one).toHaveTextContent("3,412.00");
    // A flag is a word as well as a colour.
    expect(within(one).getByText("1 without a receipt")).toBeInTheDocument();

    // The trip's lines are the trip's: the loose table holds only the taxi.
    const loose = within(card).getByRole("table", { name: "Grace Hopper's expenses" });
    expect(within(loose).getByText("Taxi")).toBeInTheDocument();
    expect(within(loose).queryByText("Hotel Bergen")).not.toBeInTheDocument();
  });

  it("approves a trip and a loose expense in one request carrying both lists", async () => {
    const { trip, lines } = tripWithLines();
    const fetchMock = stubExpensesApi({
      entries: [submitted({ id: 701, description: "Taxi" }), ...lines],
      claims: [trip],
      rates: [...rates, ...perDiemRates],
    });
    renderRoute("/expenses/approvals");

    await userEvent.click(within(await row("Taxi")).getByRole("checkbox"));
    await userEvent.click(within(await claimRow("Montasje hos kunden")).getByRole("checkbox"));
    await userEvent.click(await screen.findByRole("button", { name: "Approve 2 selected" }));

    // One request, both lists. Two would be two batches, and the server moves
    // a batch all or nothing.
    await waitFor(() =>
      expect(sent(fetchMock, "POST")).toEqual({
        url: "/api/v1/expenses/approve",
        body: { entryIds: [701], claimIds: [1012] },
      }),
    );
    expect(
      fetchMock.actualCalls.filter(([url, init]) => String(url).endsWith("/approve") && init?.method === "POST"),
    ).toHaveLength(1);
  });

  it("puts a refusal named on claimIds against the trip's own row", async () => {
    const { trip, lines } = tripWithLines();
    stubExpensesApi({
      entries: [submitted({ id: 701, description: "Taxi" }), ...lines],
      claims: [trip],
      rates: [...rates, ...perDiemRates],
      write: (method, path) =>
        method === "POST" && path === "/api/v1/expenses/approve"
          ? problemResponse(400, "Invalid approval", {
              claimIds: ["Travel claim 1012 departed before 2026-04-01, the lock date"],
            })
          : undefined,
    });
    renderRoute("/expenses/approvals");

    await userEvent.click(within(await row("Taxi")).getByRole("checkbox"));
    await userEvent.click(within(await claimRow("Montasje hos kunden")).getByRole("checkbox"));
    await userEvent.click(await screen.findByRole("button", { name: "Approve 2 selected" }));

    // The two units number independently, so the sentence goes to the trip and
    // not to whatever expense happens to be numbered 1012.
    expect(await within(await claimRow("Montasje hos kunden")).findByText(/the lock date/)).toBeInTheDocument();
    expect(within(await row("Taxi")).queryByText(/the lock date/)).not.toBeInTheDocument();
  });

  it("opens the whole trip read-only in a drawer, lines and all", async () => {
    const { trip, lines } = tripWithLines();
    stubExpensesApi({ entries: lines, claims: [trip], rates: [...rates, ...perDiemRates] });
    renderRoute("/expenses/approvals");

    const drawer = await openClaimDrawer("Montasje hos kunden");

    const table = within(drawer).getByRole("table", { name: "The trip's expenses" });
    expect(within(table).getByText("Hotel Bergen")).toBeInTheDocument();
    // A per diem day has no description of its own; what the day *is* names it.
    expect(within(table).getAllByText("Overnight, hotel").length).toBeGreaterThan(0);
    expect(drawer).toHaveTextContent("Day rate");
  });

  it("replaces the day rate on a per diem line from inside the trip's drawer", async () => {
    const { trip, lines } = tripWithLines();
    const day = perDiemLine({
      id: 802,
      claimId: 1012,
      status: "submitted",
      revision: 3,
      owner: GRACE,
      capabilities: capabilities({ canOverrideRate: true }),
    });
    const fetchMock = stubExpensesApi({
      entries: [lines[0], day],
      claims: [trip],
      rates: [...rates, ...perDiemRates],
    });
    renderRoute("/expenses/approvals");

    const drawer = await openClaimDrawer("Montasje hos kunden");
    await userEvent.click(within(drawer).getByRole("button", { name: "Replace the rate on Overnight, hotel" }));
    const dialog = await screen.findByRole("dialog", { name: "Replace the day rate" });
    const rate = within(dialog).getByRole("textbox", { name: "Day rate" });
    await userEvent.clear(rate);
    await userEvent.type(rate, "900");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT").url).toBe("/api/v1/expenses/entries/802/rate"));
    expect(sent(fetchMock, "PUT").body).toEqual({ rate: 900, revision: 3 });
    // A per diem day carries no passenger supplement, so nothing offers one.
    expect(
      within(dialog).queryByRole("textbox", { name: "Passenger supplement per kilometre" }),
    ).not.toBeInTheDocument();
  });

  it("prices a trip's outlay and then marks it invoiced, carrying the revision each write answered", async () => {
    // Pricing and invoicing stay the *line's* own doors — each is about one
    // amount — while the decision above them is the claim's. A per diem day is
    // never billed on, so neither door is offered on one.
    const { trip } = tripWithLines({ status: "approved", capabilities: claimCapabilities() });
    const billable = outlay({
      id: 801,
      claimId: 1012,
      description: "Hotel Bergen",
      status: "approved",
      entryDate: "2026-03-09",
      revision: 5,
      billable: true,
      grossAmount: 2400,
      vatAmount: 0,
      netAmount: 2400,
      owedToEmployee: 2400,
      owner: GRACE,
      project: { id: 1001, code: "KVEM1000", name: "Kverneland web" },
      capabilities: capabilities({ canSetBilling: true, canSeeBilling: true, canMarkInvoiced: true }),
      billing: { billAmount: 0 },
    });
    const day = perDiemLine({ id: 802, claimId: 1012, status: "approved", owner: GRACE, capabilities: capabilities() });
    const fetchMock = stubExpensesApi({
      entries: [billable, day],
      claims: [trip],
      rates: [...rates, ...perDiemRates],
      billingLines: [{ id: 3001, code: "PM", active: true }],
      // Every read of the trip answers the snapshot the first one produced, so
      // a drawer that took the revision off the row it was handed would send
      // the stale 5 the refetch still shows rather than the 6 the pricing
      // answered — which is the whole point of holding what a write answered.
      frozenReads: true,
    });
    renderRoute("/expenses/approvals?state=approved");

    const drawer = await openClaimDrawer("Montasje hos kunden");
    expect(within(drawer).queryByRole("button", { name: /Overnight, hotel/ })).not.toBeInTheDocument();

    await userEvent.click(within(drawer).getByRole("button", { name: "Price Hotel Bergen for the customer" }));
    const pricing = await screen.findByRole("dialog", { name: "What the customer is billed" });
    await userEvent.type(within(pricing).getByRole("textbox", { name: "Markup" }), "20");
    await userEvent.click(within(pricing).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT").body).toMatchObject({ markupPercent: 20, revision: 5 }));

    await userEvent.click(await within(drawer).findByRole("button", { name: "Mark Hotel Bergen invoiced" }));
    const invoice = await screen.findByRole("dialog", { name: "Mark the line invoiced" });
    await userEvent.type(within(invoice).getByRole("textbox", { name: "Invoice reference" }), "F-2026-41");
    await userEvent.click(within(invoice).getByRole("button", { name: "Mark invoiced" }));

    await waitFor(() => {
      const [, init] = fetchMock.actualCalls.find(([url]) => String(url).endsWith("/entries/801/invoiced")) ?? [];
      // 5 was the revision the drawer read; the pricing answered 6, and that is
      // what the invoicing has to carry or the fake answers a 409.
      expect(JSON.parse(String(init?.body))).toEqual({ revision: 6, reference: "F-2026-41" });
    });
  });

  it("takes the invoicing back off a trip's line, once it has been asked out loud", async () => {
    const { trip } = tripWithLines({ status: "approved", capabilities: claimCapabilities() });
    const invoiced = outlay({
      id: 801,
      claimId: 1012,
      description: "Hotel Bergen",
      status: "approved",
      entryDate: "2026-03-09",
      revision: 7,
      billable: true,
      owner: GRACE,
      project: { id: 1001, code: "KVEM1000", name: "Kverneland web" },
      capabilities: capabilities({ canSeeBilling: true, canUndoInvoiced: true }),
      // 01:00 UTC is the 2nd in Oslo and still the 1st in the browser's zone,
      // which this suite pins to America/New_York.
      billing: { billAmount: 2880, markupPercent: 20, invoice: { at: "2026-04-02T01:00:00Z", by: APPROVER } },
    });
    const fetchMock = stubExpensesApi({ entries: [invoiced], claims: [trip], rates: [...rates, ...perDiemRates] });
    renderRoute("/expenses/approvals?state=approved");

    const drawer = await openClaimDrawer("Montasje hos kunden");
    // Every instant on this screen is the installation's calendar, not the
    // reader's — the invoice stamp no less than the trip's own two ends.
    const line801 = drawer.querySelector('[data-expense="801"]') as HTMLElement;
    expect(line801).toHaveTextContent("Invoiced Apr 2, 2026");
    await userEvent.click(within(drawer).getByRole("button", { name: "Undo the invoicing of Hotel Bergen" }));

    // A stamp that says an invoice went out is not taken back on one click.
    const confirm = await screen.findByRole("dialog", { name: "Undo the invoicing?" });
    expect(confirm).toHaveTextContent(/Hotel Bergen goes back to waiting to be billed/);
    await userEvent.click(within(confirm).getByRole("button", { name: "Undo invoicing" }));

    await waitFor(() => expect(sent(fetchMock, "POST").url).toBe("/api/v1/expenses/entries/801/invoiced/undo"));
    expect(sent(fetchMock, "POST").body).toEqual({ revision: 7 });
  });

  it("marks a standalone expense invoiced from its own drawer, and takes it back again", async () => {
    const line = outlay({
      id: 501,
      description: "Hotel",
      status: "approved",
      revision: 4,
      billable: true,
      project: { id: 1001, code: "KVEM1000", name: "Kverneland web" },
      capabilities: capabilities({ canUnapprove: true, canSeeBilling: true, canMarkInvoiced: true }),
      billing: { billAmount: 750 },
    });
    const fetchMock = stubExpensesApi({ entries: [line] });
    renderRoute("/expenses/approvals?state=approved");

    const drawer = await openDrawer("Hotel");
    await userEvent.click(within(drawer).getByRole("button", { name: "Mark invoiced" }));
    const invoice = await screen.findByRole("dialog", { name: "Mark the line invoiced" });
    await userEvent.click(within(invoice).getByRole("button", { name: "Mark invoiced" }));
    await waitFor(() => expect(sent(fetchMock, "POST").url).toBe("/api/v1/expenses/entries/501/invoiced"));

    // The answer turned the capability round, and the undo carries the
    // revision that answer gave — 5, not the 4 the drawer opened at.
    await userEvent.click(await within(drawer).findByRole("button", { name: "Undo invoicing" }));
    const confirm = await screen.findByRole("dialog", { name: "Undo the invoicing?" });
    await userEvent.click(within(confirm).getByRole("button", { name: "Undo invoicing" }));

    await waitFor(() => {
      const [, init] = fetchMock.actualCalls.find(([url]) => String(url).endsWith("/entries/501/invoiced/undo")) ?? [];
      expect(JSON.parse(String(init?.body))).toEqual({ revision: 5 });
    });
  });

  it("approves the whole trip from its drawer and closes it", async () => {
    const { trip, lines } = tripWithLines();
    const fetchMock = stubExpensesApi({ entries: lines, claims: [trip], rates: [...rates, ...perDiemRates] });
    renderRoute("/expenses/approvals");

    const drawer = await openClaimDrawer("Montasje hos kunden");
    await userEvent.click(within(drawer).getByRole("button", { name: "Approve" }));

    await waitFor(() =>
      expect(sent(fetchMock, "POST")).toEqual({ url: "/api/v1/expenses/approve", body: { claimIds: [1012] } }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Montasje hos kunden" })).not.toBeInTheDocument());
  });

  it("shows the server's refusal when a trip that offers an unapprove cannot be unapproved", async () => {
    // `canUnapprove` is the server's answer and it is right, but a line that
    // has been invoiced since still refuses — the sentence has to be read.
    const { trip, lines } = tripWithLines({
      status: "approved",
      capabilities: claimCapabilities({ canUnapprove: true }),
    });
    stubExpensesApi({
      entries: lines.map((line) => ({ ...line, status: "approved" as const })),
      claims: [trip],
      rates: [...rates, ...perDiemRates],
      write: (method, path) =>
        method === "POST" && path === "/api/v1/expenses/unapprove"
          ? problemResponse(400, "Invalid approval", {
              claimIds: ["Travel claim 1012 holds expense 801, which has been invoiced"],
            })
          : undefined,
    });
    renderRoute("/expenses/approvals?state=approved");

    const drawer = await openClaimDrawer("Montasje hos kunden");
    await userEvent.click(within(drawer).getByRole("button", { name: "Take the approval back" }));

    // Twice: at the top, because it is a refusal about the trip, and against
    // the line it names.
    expect(await within(drawer).findAllByText(/has been invoiced/)).toHaveLength(2);
    // …and against line 801, because the sentence names it. The server writes
    // "holds expense 801" in lower case, which is the whole reason this is a
    // real assertion rather than one the top alert already satisfies.
    const line = drawer.querySelector('[data-expense="801"]') as HTMLElement;
    expect(await within(line).findByText(/has been invoiced/)).toBeInTheDocument();
    // The per diem day is named by nobody and carries nothing.
    const day = drawer.querySelector('[data-expense="802"]') as HTMLElement;
    expect(within(day).queryByText(/has been invoiced/)).not.toBeInTheDocument();
  });

  it("keeps an expense and a travel claim with the same id apart, refusal and all", async () => {
    // The two units number independently, so a queue can genuinely hold an
    // expense 1012 and a trip 1012 at once. Two arrays, two maps, two tables —
    // and a refusal on each has to reach its own row.
    const { trip, lines } = tripWithLines();
    stubExpensesApi({
      entries: [submitted({ id: 1012, description: "Taxi" }), ...lines],
      claims: [trip],
      rates: [...rates, ...perDiemRates],
      write: (method, path) =>
        method === "POST" && path === "/api/v1/expenses/approve"
          ? problemResponse(400, "Invalid approval", {
              entryIds: ["Expense 1012 is dated before 2026-04-01, the lock date"],
              claimIds: ["Travel claim 1012 departed before 2026-04-01, the lock date"],
            })
          : undefined,
    });
    renderRoute("/expenses/approvals");

    await userEvent.click(within(await row("Taxi")).getByRole("checkbox"));
    await userEvent.click(within(await claimRow("Montasje hos kunden")).getByRole("checkbox"));
    await userEvent.click(await screen.findByRole("button", { name: "Approve 2 selected" }));

    expect(await within(await row("Taxi")).findByText(/is dated before/)).toBeInTheDocument();
    expect(within(await row("Taxi")).queryByText(/departed before/)).not.toBeInTheDocument();
    expect(await within(await claimRow("Montasje hos kunden")).findByText(/departed before/)).toBeInTheDocument();
    expect(within(await claimRow("Montasje hos kunden")).queryByText(/is dated before/)).not.toBeInTheDocument();
  });

  it("never says nothing is approved above a table of approved trips", async () => {
    const { trip, lines } = tripWithLines({
      status: "approved",
      capabilities: claimCapabilities({ canUnapprove: true }),
    });
    stubExpensesApi({
      entries: lines.map((line) => ({ ...line, status: "approved" as const })),
      claims: [trip],
      rates: [...rates, ...perDiemRates],
    });
    renderRoute("/expenses/approvals?state=approved");

    // Every approved unit here is a trip, which is the common case for this
    // delivery. The empty state is about both halves or it is a lie.
    expect(await screen.findByRole("table", { name: "Travel claims" })).toBeInTheDocument();
    expect(screen.queryByText("Nothing approved yet")).not.toBeInTheDocument();
  });

  it("says so when the approved trips could not be read, rather than showing half the units", async () => {
    stubExpensesApi({
      entries: [
        outlay({
          id: 501,
          status: "approved",
          description: "Hotel",
          capabilities: capabilities({ canUnapprove: true }),
        }),
      ],
      write: () => undefined,
      claims: [],
      claimList: problemResponse(500, "The travel claims are unavailable"),
    });
    renderRoute("/expenses/approvals?state=approved");

    expect(await screen.findByText("Could not load the travel claims")).toBeInTheDocument();
    // …and the expenses that did load are still there.
    expect(await screen.findByText("Hotel")).toBeInTheDocument();
  });

  it("keeps one section's selection when the other section is paged", async () => {
    const { trip, lines } = tripWithLines({
      status: "approved",
      capabilities: claimCapabilities({ canUnapprove: true }),
    });
    const second = {
      ...trip,
      id: 1013,
      purpose: "Kurs i Trondheim",
      departureAt: "2026-02-01T06:00:00Z",
      returnAt: "2026-02-02T15:00:00Z",
    };
    stubExpensesApi({
      entries: [
        outlay({
          id: 501,
          status: "approved",
          description: "Hotel",
          capabilities: capabilities({ canUnapprove: true }),
        }),
        ...lines.map((line) => ({ ...line, status: "approved" as const })),
      ],
      claims: [trip, second],
      rates: [...rates, ...perDiemRates],
      pageSize: 1,
    });
    const { router } = renderRoute("/expenses/approvals?state=approved");

    await userEvent.click(within(await row("Hotel")).getByRole("checkbox"));
    expect(await screen.findByRole("button", { name: "Take 1 approvals back" })).toBeInTheDocument();

    // Paging the trips is not a reason to throw away an expense somebody has
    // just ticked: the two sections page independently.
    await userEvent.click(screen.getByRole("button", { name: "2" }));
    await waitFor(() => expect(router.state.location.search).toMatchObject({ claimPage: 2 }));
    expect(await screen.findByRole("button", { name: "Take 1 approvals back" })).toBeInTheDocument();
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
