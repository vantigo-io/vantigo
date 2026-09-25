import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { jsonResponse, problemResponse, sent } from "../test/api";
import {
  capabilities,
  categoriesWithSubcontractor,
  claim,
  meta,
  mileage,
  OTHER,
  outlay,
  PROJECT,
  projectOptions,
  projectSummary,
  summaryBucket,
  summaryCurrency,
  supplierInvoice,
} from "../test/fixtures";
import { renderWithProviders } from "../test/render";
import { stubExpensesApi } from "../test/server";
import { ProjectExpensesPanel } from "./project-expenses-panel";

const BOOKED = { id: PROJECT, code: "KVEM1000", name: "Kverneland web" };

/** A bare 404, the one answer the summary gives for all three of its reasons. */
const notYours = () => new Response(null, { status: 404 });

const row = async (description: string) =>
  (await screen.findByText(description)).closest("[data-expense]") as HTMLElement;

const panel = (onChanged?: () => void) =>
  renderWithProviders(<ProjectExpensesPanel projectId={PROJECT} onChanged={onChanged} />);

describe("ProjectExpensesPanel", () => {
  it("writes each currency's cost, what it bills and what is ready, and never adds the buckets up", async () => {
    // `total` is deliberately not `approved + submitted + draft`: the server
    // rounds each bucket once on its own, so the three can differ from the
    // total by a cent and the client must render what was published.
    stubExpensesApi({
      entries: [],
      projectSummary: projectSummary({
        lastEntryDate: "2026-03-18",
        projectCurrency: "NOK",
        currencies: [
          summaryCurrency({
            currency: "EUR",
            approved: summaryBucket({ count: 1, cost: 90, billAmount: 100 }),
            total: summaryBucket({ count: 1, cost: 90, billAmount: 100 }),
            readyCount: 1,
            readyAmount: 100,
          }),
          summaryCurrency({
            currency: "NOK",
            approved: summaryBucket({ count: 5, cost: 2400, billAmount: 1750 }),
            submitted: summaryBucket({ count: 1, cost: 700, billAmount: 800 }),
            draft: summaryBucket({ count: 1, cost: 150, billAmount: 0 }),
            total: summaryBucket({ count: 7, cost: 3249.99, billAmount: 2550 }),
            readyCount: 2,
            readyAmount: 1250,
            invoicedCount: 1,
            invoicedAmount: 500,
            unpricedCount: 1,
          }),
        ],
      }),
    });
    panel();

    const nok = await screen.findByTestId("project-expense-currency-NOK");
    // The published total, not 2400 + 700 + 150 = 3250.
    expect(nok).toHaveTextContent("3,249.99");
    expect(nok).not.toHaveTextContent("3,250.00");
    expect(nok).toHaveTextContent("NOK — the project's own currency");
    expect(nok).toHaveTextContent("Approved");
    expect(nok).toHaveTextContent("2,400.00");
    // The two that are not approved say so in words: for somebody who may
    // read the figures without opening the expenses, this is the only place
    // that can tell them apart.
    expect(nok).toHaveTextContent("Submitted — awaiting approval");
    expect(nok).toHaveTextContent("700.00");
    expect(nok).toHaveTextContent("Draft — not submitted yet, or rejected");
    expect(nok).toHaveTextContent("Passed on to the customer");
    expect(nok).toHaveTextContent("2,550.00");
    expect(nok).toHaveTextContent("Ready to invoice");
    expect(nok).toHaveTextContent("1,250.00");
    expect(nok).toHaveTextContent("Invoiced");
    expect(nok).toHaveTextContent("500.00");
    expect(nok).toHaveTextContent("1 billable expense has no price yet");

    // A second currency is a card of its own, labelled as what it is:
    // nothing is converted, and it is not part of the project's economy.
    const eur = screen.getByTestId("project-expense-currency-EUR");
    expect(eur).toHaveTextContent(
      "In another currency (EUR) — not converted, and not part of the project's economy figures",
    );
    expect(eur).toHaveTextContent("90.00");
    expect(eur).not.toHaveTextContent("2,400.00");

    // The project's own currency comes first, whatever order the codes sort in.
    const cards = screen.getAllByTestId(/^project-expense-currency-/);
    expect(cards.map((card) => card.dataset.testid)).toEqual([
      "project-expense-currency-NOK",
      "project-expense-currency-EUR",
    ]);

    expect(screen.getByTestId("project-expense-totals")).toHaveTextContent("Last expense:");
  });

  it("titles every card by its bare currency when the project has none of its own", async () => {
    stubExpensesApi({
      entries: [],
      projectSummary: projectSummary({
        currencies: [summaryCurrency({ currency: "EUR", total: summaryBucket({ count: 1, cost: 90 }) })],
      }),
    });
    panel();

    const eur = await screen.findByTestId("project-expense-currency-EUR");
    expect(eur).not.toHaveTextContent("In another currency");
    expect(eur).not.toHaveTextContent("the project's own currency");
  });

  it("leaves the whole totals block out when they are not this caller's to read, and still lists the expenses", async () => {
    stubExpensesApi({
      entries: [outlay({ project: BOOKED })],
      projectSummary: notYours(),
    });
    panel();

    await row("Taxi to the airport");
    expect(screen.queryByTestId("project-expense-totals")).not.toBeInTheDocument();
    // A 404 is "these figures are not yours", not a failure: nothing is shown
    // in red, because the caller has nothing to fix.
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.queryByText(/Could not load what the expenses come to/)).not.toBeInTheDocument();
  });

  it("says so when the totals could not be read for any other reason", async () => {
    stubExpensesApi({
      entries: [outlay({ project: BOOKED })],
      projectSummary: jsonResponse(500, { title: "The database is away", status: 500 }),
    });
    panel();

    expect(await screen.findByText("Could not load what the expenses come to")).toBeInTheDocument();
  });

  it("offers 'Ready to invoice' only when the totals were readable, and asks the list for exactly those lines", async () => {
    const readable = stubExpensesApi({
      entries: [
        outlay({ id: 511, description: "Priced taxi", project: BOOKED }),
        outlay({ id: 512, description: "Unpriced lunch", project: BOOKED }),
      ],
      projectSummary: projectSummary({
        currencies: [summaryCurrency({ total: summaryBucket({ count: 2, cost: 1000 }), readyCount: 1 })],
      }),
    });
    const { unmount } = panel();

    await userEvent.click(await screen.findByRole("radio", { name: "Ready to invoice" }));
    await waitFor(() => {
      expect(readable.actualCalls.some(([url]) => String(url).includes("toInvoice=true"))).toBe(true);
    });
    const [url] = readable.actualCalls.findLast(([one]) => String(one).includes("toInvoice=true")) ?? [];
    // The filter never travels without the project: invoicing is read one
    // project at a time, and the API refuses it otherwise.
    expect(String(url)).toContain(`projectId=${PROJECT}`);
    unmount();

    stubExpensesApi({ entries: [outlay({ project: BOOKED })], projectSummary: notYours() });
    panel();

    await row("Taxi to the airport");
    expect(screen.queryByRole("radio", { name: "Ready to invoice" })).not.toBeInTheDocument();
  });

  it("says the totals cover expenses the list may not show", async () => {
    // The aggregate and the rows are gated differently on purpose: a
    // `projects:view-financials` holder who does not manage the project reads
    // every figure and is shown none of the receipts behind them.
    stubExpensesApi({
      entries: [outlay({ project: BOOKED })],
      projectSummary: projectSummary({
        currencies: [summaryCurrency({ total: summaryBucket({ count: 7, cost: 3250 }) })],
      }),
    });
    panel();

    expect(await screen.findByTestId("project-expenses-partial")).toHaveTextContent(
      "The totals cover every expense on the project.",
    );
  });

  it("says in words that the totals are readable and the expenses behind them are not", async () => {
    // A `projects:view-financials` holder who does not manage the project:
    // every figure, and not one of the receipts. An empty table here would
    // read as a project nobody has spent anything on.
    stubExpensesApi({
      entries: [],
      projectSummary: projectSummary({
        currencies: [summaryCurrency({ total: summaryBucket({ count: 7, cost: 3250 }) })],
      }),
    });
    panel();

    expect(await screen.findByTestId("project-expenses-hidden")).toHaveTextContent(
      "You can see this project's totals, but not the individual expenses behind them.",
    );
    expect(screen.queryByText("No expenses on this project yet")).not.toBeInTheDocument();
  });

  it("keeps quiet about the gap when the list already shows everything the totals count", async () => {
    stubExpensesApi({
      entries: [outlay({ project: BOOKED })],
      projectSummary: projectSummary({
        currencies: [summaryCurrency({ total: summaryBucket({ count: 1, cost: 500 }) })],
      }),
    });
    panel();

    await row("Taxi to the airport");
    expect(screen.queryByTestId("project-expenses-partial")).not.toBeInTheDocument();
  });

  it("tells a project with nothing on it apart from one with nothing ready to invoice", async () => {
    const { unmount } = (() => {
      stubExpensesApi({ entries: [], projectSummary: projectSummary() });
      return panel();
    })();

    expect(await screen.findByText("No expenses on this project yet")).toBeInTheDocument();
    unmount();

    stubExpensesApi({
      entries: [outlay({ project: BOOKED })],
      projectSummary: projectSummary({
        currencies: [summaryCurrency({ total: summaryBucket({ count: 1, cost: 500 }) })],
      }),
    });
    panel();

    await userEvent.click(await screen.findByRole("radio", { name: "Ready to invoice" }));
    expect(await screen.findByText("Nothing is ready to invoice")).toBeInTheDocument();
  });

  it("offers 'Record a cost' only to somebody who may book on the project", async () => {
    const { unmount } = (() => {
      stubExpensesApi({
        entries: [],
        projects: projectOptions,
        projectSummary: projectSummary({ capabilities: { canRecord: false } }),
      });
      return panel();
    })();

    await screen.findByText("No expenses on this project yet");
    expect(screen.queryByRole("button", { name: "Record a cost" })).not.toBeInTheDocument();
    unmount();

    stubExpensesApi({
      entries: [],
      projects: projectOptions,
      projectSummary: projectSummary({ capabilities: { canRecord: true } }),
    });
    panel();

    expect(await screen.findByRole("button", { name: "Record a cost" })).toBeInTheDocument();
  });

  it("still offers it when the totals are not the caller's to read but the project is one they may book on", async () => {
    const { unmount } = (() => {
      stubExpensesApi({ entries: [], projects: projectOptions, projectSummary: notYours() });
      return panel();
    })();

    expect(await screen.findByRole("button", { name: "Record a cost" })).toBeInTheDocument();
    unmount();

    // Not among the projects this caller may book on: no button, no form that
    // the save would refuse.
    stubExpensesApi({ entries: [], projects: [projectOptions[1]], projectSummary: notYours() });
    panel();

    await screen.findByText("No expenses on this project yet");
    expect(screen.queryByRole("button", { name: "Record a cost" })).not.toBeInTheDocument();
  });

  it("records a cost with the project fixed, as an outlay the company paid", async () => {
    const onChanged = vi.fn();
    const fetchMock = stubExpensesApi({
      entries: [],
      projects: projectOptions,
      projectSummary: projectSummary({ capabilities: { canRecord: true } }),
    });
    panel(onChanged);

    await userEvent.click(await screen.findByRole("button", { name: "Record a cost" }));
    const dialog = await screen.findByRole("dialog");
    // The project is stated, never offered: the form was opened from the
    // project's own page and cannot be pointed somewhere else.
    expect(within(dialog).getByText("Booked on KVEM1000 · Kverneland web")).toBeInTheDocument();
    expect(within(dialog).queryByRole("textbox", { name: "Project" })).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("combobox", { name: "Project" })).not.toBeInTheDocument();

    await userEvent.type(within(dialog).getByRole("textbox", { name: /Description/ }), "Server rack");
    await userEvent.click(within(dialog).getByRole("combobox", { name: /Category/ }));
    await userEvent.click(await screen.findByRole("option", { name: "Travel" }));
    await userEvent.type(within(dialog).getByRole("textbox", { name: /Amount including VAT/ }), "1000");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save draft" }));

    await waitFor(() => expect(sent(fetchMock, "POST").body).toBeDefined());
    const { body } = sent(fetchMock, "POST");
    expect(body.projectId).toBe(PROJECT);
    expect(body.kind).toBe("outlay");
    expect(body.paidBy).toBe("company");
    // The figures on the tab move with it, and so do the project's own.
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
  });

  it("says so when the save is refused on the project the form cannot change", async () => {
    // The one refusal this mode can provoke and the picker mode cannot: a
    // create is never grandfathered, so a project completed — or a person
    // taken off its team — while the form was open is a 400 on `projectId`.
    // There is no input bound to it here, so it has to be said out loud.
    stubExpensesApi({
      entries: [],
      projects: projectOptions,
      projectSummary: projectSummary({ capabilities: { canRecord: true } }),
      write: (method, path) =>
        method === "POST" && path === "/api/v1/expenses/entries"
          ? problemResponse(400, "Invalid expense", {
              projectId: ["Expenses can no longer be booked on project KVEM1000"],
            })
          : undefined,
    });
    panel();

    await userEvent.click(await screen.findByRole("button", { name: "Record a cost" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("textbox", { name: /Description/ }), "Server rack");
    await userEvent.click(within(dialog).getByRole("combobox", { name: /Category/ }));
    await userEvent.click(await screen.findByRole("option", { name: "Travel" }));
    await userEvent.type(within(dialog).getByRole("textbox", { name: /Amount including VAT/ }), "1000");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save draft" }));

    expect(await within(dialog).findByText("Expenses can no longer be booked on project KVEM1000")).toBeInTheDocument();
    // And the form stays open, with what was typed still in it.
    expect(within(dialog).getByRole("textbox", { name: /Description/ })).toHaveValue("Server rack");
  });

  it("tells the host about a cost that was saved even when sending it for approval was refused", async () => {
    // The draft exists — it is in the project's draft bucket and in the
    // Economy tab's cost figures — so the figures moved whatever the
    // submission did.
    const onChanged = vi.fn();
    stubExpensesApi({
      entries: [],
      projects: projectOptions,
      projectSummary: projectSummary({ capabilities: { canRecord: true } }),
      write: (method, path) =>
        method === "POST" && path === "/api/v1/expenses/submit"
          ? problemResponse(400, "Invalid submission", { entryIds: ["A receipt is required over NOK 1,250.00"] })
          : undefined,
    });
    panel(onChanged);

    await userEvent.click(await screen.findByRole("button", { name: "Record a cost" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("textbox", { name: /Description/ }), "Server rack");
    await userEvent.click(within(dialog).getByRole("combobox", { name: /Category/ }));
    await userEvent.click(await screen.findByRole("option", { name: "Travel" }));
    await userEvent.type(within(dialog).getByRole("textbox", { name: /Amount including VAT/ }), "2000");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save and submit" }));

    await waitFor(() => expect(onChanged).toHaveBeenCalled());
  });

  it("opens one expense in the drawer and tells the host when marking it invoiced changed the figures", async () => {
    const onChanged = vi.fn();
    stubExpensesApi({
      entries: [
        outlay({
          project: BOOKED,
          status: "approved",
          billable: true,
          billing: { billAmount: 800 },
          capabilities: capabilities({ canSeeBilling: true, canMarkInvoiced: true }),
        }),
      ],
      projectSummary: projectSummary({
        currencies: [summaryCurrency({ total: summaryBucket({ count: 1, cost: 500 }), readyCount: 1 })],
      }),
    });
    panel(onChanged);

    await userEvent.click(await screen.findByRole("button", { name: "Open Taxi to the airport" }));
    const drawer = await screen.findByRole("dialog", { name: "Taxi to the airport" });
    await userEvent.click(within(drawer).getByRole("button", { name: "Mark invoiced" }));
    const invoice = await screen.findByRole("dialog", { name: "Mark the line invoiced" });
    await userEvent.click(within(invoice).getByRole("button", { name: "Mark invoiced" }));

    await waitFor(() => expect(onChanged).toHaveBeenCalled());
  });

  it("sends a travel claim's line to its trip rather than pretending it stands alone", async () => {
    stubExpensesApi({
      claims: [claim({ id: 1012, project: BOOKED })],
      entries: [mileage({ id: 605, claimId: 1012, description: "Drive to the site", project: BOOKED })],
      projectSummary: projectSummary({
        currencies: [summaryCurrency({ total: summaryBucket({ count: 1, cost: 636 }) })],
      }),
    });
    panel();

    const line = await row("Drive to the site");
    expect(within(line).getByRole("link", { name: "Open the travel claim of Drive to the site" })).toHaveAttribute(
      "href",
      "/expenses/claims/1012",
    );
  });

  it("leaves All unfiltered, starts the list again, and measures the sentence against what is ready", async () => {
    // Three things in one, because they are one behaviour: the chip decides
    // both what is asked for and what the sentence is compared against.
    // `toInvoice=false` now *means* the parameter left out, so a chip bound to
    // a boolean would send it and be answered under rules nobody asked for.
    const fetchMock = stubExpensesApi({
      entries: [
        // Distinct days, because the list is newest first: which row lands on
        // which page has to be a fact about the fixture, not about ids.
        outlay({
          id: 511,
          description: "Ready one",
          entryDate: "2026-09-20",
          project: BOOKED,
          status: "approved",
          billable: true,
          billAmount: 800,
        }),
        outlay({
          id: 512,
          description: "Ready two",
          entryDate: "2026-09-19",
          project: BOOKED,
          status: "approved",
          billable: true,
          billAmount: 900,
        }),
        outlay({ id: 513, description: "Not ready", project: BOOKED }),
      ],
      pageSize: 1,
      projectSummary: projectSummary({
        currencies: [summaryCurrency({ total: summaryBucket({ count: 9, cost: 5000 }), readyCount: 2 })],
      }),
    });
    panel();

    await userEvent.click(await screen.findByRole("radio", { name: "Ready to invoice" }));
    await screen.findByText("Ready one");
    await userEvent.click(screen.getByRole("button", { name: "2" }));
    await screen.findByText("Ready two");

    await userEvent.click(screen.getByRole("radio", { name: "All" }));
    await waitFor(() => expect(screen.getByRole("radio", { name: "All" })).toBeChecked());

    // Changing the chip starts the list again: page 2 of "ready" is not page 2
    // of everything, and a stale page number would show an empty table.
    await screen.findByText("Ready one");
    const asked = fetchMock.actualCalls.map(([url]) => String(url)).filter((url) => url.includes("/entries?"));
    const last = asked.at(-1) ?? "";
    expect(last).not.toContain("toInvoice");
    expect(last).toContain("page=1");

    // Under "All" the sentence is measured against Σ total.count (9 vs 3), so
    // it is there; under "Ready" it was Σ readyCount (2 vs 2), so it was not.
    expect(screen.getByTestId("project-expenses-partial")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("radio", { name: "Ready to invoice" }));
    await screen.findByText("Ready one");
    expect(screen.queryByTestId("project-expenses-partial")).not.toBeInTheDocument();
  });

  it("never claims nothing is ready while rows are waiting on an earlier page", async () => {
    // Mark the last line on page 2 invoiced and the list drops to one page. A
    // page number left behind asks for a page that no longer exists, gets no
    // rows, and would read as a project with nothing ready at all.
    const entries = [
      outlay({
        id: 511,
        description: "Ready one",
        entryDate: "2026-09-20",
        project: BOOKED,
        status: "approved",
        billable: true,
        billAmount: 800,
        capabilities: capabilities({ canSeeBilling: true }),
      }),
      outlay({
        id: 512,
        description: "Ready two",
        entryDate: "2026-09-18",
        project: BOOKED,
        status: "approved",
        billable: true,
        billAmount: 900,
        capabilities: capabilities({ canSeeBilling: true, canMarkInvoiced: true }),
      }),
    ];
    stubExpensesApi({ entries, pageSize: 1 });
    panel();

    await userEvent.click(await screen.findByRole("radio", { name: "Ready to invoice" }));
    await screen.findByText("Ready one");
    await userEvent.click(screen.getByRole("button", { name: "2" }));

    await userEvent.click(await screen.findByRole("button", { name: "Open Ready two" }));
    const drawer = await screen.findByRole("dialog", { name: "Ready two" });
    await userEvent.click(within(drawer).getByRole("button", { name: "Mark invoiced" }));
    const invoice = await screen.findByRole("dialog", { name: "Mark the line invoiced" });
    await userEvent.click(within(invoice).getByRole("button", { name: "Mark invoiced" }));

    expect(await screen.findByText("Ready one")).toBeInTheDocument();
    expect(screen.queryByText("Nothing is ready to invoice")).not.toBeInTheDocument();
  });

  it("asks for a page that exists, whatever the list is empty of", async () => {
    // The server answers an empty list with `totalPages: 0` and refuses
    // `page=0` with a 400 — so "clamp to the last page" has to mean the last
    // page there *is*. This is the day-one state of every project, the finance
    // reader's normal state and "nothing ready", all at once.
    const fetchMock = stubExpensesApi({
      entries: [],
      projectSummary: projectSummary({
        currencies: [summaryCurrency({ total: summaryBucket({ count: 7, cost: 3250 }), readyCount: 0 })],
      }),
    });
    panel();

    // Totals the caller may read over a list they may not: the sentence, not
    // an error, and not an empty table.
    expect(await screen.findByTestId("project-expenses-hidden")).toBeInTheDocument();
    expect(screen.queryByText("Could not load the project's expenses")).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("radio", { name: "Ready to invoice" }));
    expect(await screen.findByText("Nothing is ready to invoice")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("radio", { name: "All" }));
    await screen.findByTestId("project-expenses-hidden");

    const asked = fetchMock.actualCalls.map(([url]) => String(url)).filter((url) => url.includes("/entries?"));
    expect(asked.length).toBeGreaterThan(0);
    expect(asked.filter((url) => url.includes("page=0"))).toEqual([]);
  });

  it("shows the empty state of a project nobody has recorded anything on", async () => {
    const fetchMock = stubExpensesApi({ entries: [], projectSummary: projectSummary() });
    panel();

    expect(await screen.findByText("No expenses on this project yet")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(fetchMock.actualCalls.map(([url]) => String(url)).filter((url) => url.includes("page=0"))).toEqual([]);
  });

  it("offers no filter at all on a project with nothing recorded", async () => {
    stubExpensesApi({ entries: [], projectSummary: projectSummary() });
    panel();

    await screen.findByText("No expenses on this project yet");
    expect(screen.queryByRole("radio", { name: "Ready to invoice" })).not.toBeInTheDocument();
    expect(screen.queryByRole("radio", { name: "All" })).not.toBeInTheDocument();
  });

  it("names the filter set as a radio group", async () => {
    stubExpensesApi({
      entries: [],
      projectSummary: projectSummary({
        currencies: [summaryCurrency({ total: summaryBucket({ count: 1, cost: 10 }) })],
      }),
    });
    panel();

    expect(await screen.findByRole("radiogroup", { name: "Which expenses to show" })).toBeInTheDocument();
  });

  it("holds the totals' place while they are being read", () => {
    stubExpensesApi({ entries: [], projectSummary: projectSummary() });
    panel();

    expect(screen.getByTestId("project-expense-totals-loading")).toBeInTheDocument();
  });

  it("counts a billable line with no price as unpriced rather than ready", async () => {
    // `bill_amount IS NULL` is not something a response can say — the server
    // renders `billAmount: 0` for it — so the fixture says it outright, and
    // the figures follow the column rather than the rendering.
    stubExpensesApi({
      entries: [
        outlay({
          id: 521,
          description: "Mileage with no customer rate",
          project: BOOKED,
          status: "approved",
          billable: true,
          billAmount: null,
          netAmount: 400,
          capabilities: capabilities({ canSeeBilling: true }),
        }),
      ],
      projectCurrency: "NOK",
    });
    panel();

    const nok = await screen.findByTestId("project-expense-currency-NOK");
    expect(nok).toHaveTextContent("1 billable expense has no price yet");
    expect(within(nok).getByText("Ready to invoice").parentElement).toHaveTextContent("0");

    await userEvent.click(screen.getByRole("radio", { name: "Ready to invoice" }));
    expect(await screen.findByText("Nothing is ready to invoice")).toBeInTheDocument();
  });

  it("shows the server's own refusal when the ready filter is the one thing refused", async () => {
    // Refused on **that request only**: the rows under "All" are answered, so
    // the alert can only be about the chip. (`entryList`, which replaces every
    // list answer, made the earlier version of this test pass before the chip
    // was ever clicked.) The chip is offered only when the summary was
    // readable, so this is rights going between the two reads.
    stubExpensesApi({
      entries: [outlay({ description: "Taxi to the airport", project: BOOKED })],
      refuseToInvoice: true,
      projectSummary: projectSummary({
        currencies: [summaryCurrency({ total: summaryBucket({ count: 1, cost: 500 }), readyCount: 1 })],
      }),
    });
    panel();

    await row("Taxi to the airport");

    await userEvent.click(screen.getByRole("radio", { name: "Ready to invoice" }));
    const alert = await screen.findByText("Could not load the project's expenses");
    // What the server actually said, not merely that something failed.
    expect(alert.closest("[role='alert']")).toHaveTextContent("You do not have permission to access this resource.");
    expect(screen.queryByText("Nothing is ready to invoice")).not.toBeInTheDocument();

    // And "All" still answers, so the tab is not a dead end.
    await userEvent.click(screen.getByRole("radio", { name: "All" }));
    expect(await screen.findByText("Taxi to the airport")).toBeInTheDocument();
  });

  it("re-reads the totals when the ready filter is refused, because the rights just changed", async () => {
    // The figures above the alert are ones this caller may no longer be able
    // to read; leaving them on screen from cache would be showing money the
    // server has just said no to.
    const fetchMock = stubExpensesApi({
      entries: [outlay({ project: BOOKED })],
      refuseToInvoice: true,
      projectSummary: projectSummary({
        currencies: [summaryCurrency({ total: summaryBucket({ count: 1, cost: 500 }), readyCount: 1 })],
      }),
    });
    panel();

    await row("Taxi to the airport");
    const before = fetchMock.actualCalls.filter(([url]) => String(url).includes("/summary")).length;
    await userEvent.click(screen.getByRole("radio", { name: "Ready to invoice" }));
    await screen.findByText("Could not load the project's expenses");

    await waitFor(() =>
      expect(fetchMock.actualCalls.filter(([url]) => String(url).includes("/summary")).length).toBeGreaterThan(before),
    );
  });

  it("does not present the previous filter's rows as the answer to the new one", async () => {
    // `keepPreviousData` keeps the old page on screen while the new one is in
    // flight, which is what stops the table flashing — but undimmed it reads
    // as "these are the ready lines", and a count taken from it would flash
    // the "totals cover more" sentence at the wrong moment.
    const stub = stubExpensesApi({
      entries: [
        outlay({ id: 511, description: "Not ready at all", entryDate: "2026-09-20", project: BOOKED }),
        outlay({
          id: 512,
          description: "Ready and priced",
          project: BOOKED,
          status: "approved",
          billable: true,
          billAmount: 800,
        }),
      ],
      hold: [`/api/v1/expenses/entries?projectId=${PROJECT}&toInvoice=true&page=1`],
      projectSummary: projectSummary({
        currencies: [summaryCurrency({ total: summaryBucket({ count: 9, cost: 500 }), readyCount: 1 })],
      }),
    });
    panel();

    await screen.findByText("Not ready at all");
    await userEvent.click(screen.getByRole("radio", { name: "Ready to invoice" }));

    await waitFor(() => expect(screen.getByTestId("project-expenses-rows")).toHaveAttribute("aria-busy", "true"));
    // Still the previous filter's rows — and no sentence measured against
    // them: under "Ready" the count to compare with is Σ readyCount (1), and
    // a page of not-ready rows would make it look short.
    expect(screen.getByText("Not ready at all")).toBeInTheDocument();
    expect(screen.queryByTestId("project-expenses-partial")).not.toBeInTheDocument();

    stub.release();
    await screen.findByText("Ready and priced");
    await waitFor(() => expect(screen.getByTestId("project-expenses-rows")).toHaveAttribute("aria-busy", "false"));
    expect(screen.queryByText("Not ready at all")).not.toBeInTheDocument();
  });

  it("names the claim link after the row it is on", async () => {
    stubExpensesApi({
      claims: [claim({ id: 1012, project: BOOKED })],
      entries: [mileage({ id: 605, claimId: 1012, description: "Drive to the site", project: BOOKED })],
      projectSummary: projectSummary({
        currencies: [summaryCurrency({ total: summaryBucket({ count: 1, cost: 636 }) })],
      }),
    });
    panel();

    const line = await row("Drive to the site");
    expect(within(line).getByRole("link", { name: "Open the travel claim of Drive to the site" })).toBeInTheDocument();
  });

  it("renders the not-available state rather than crashing without the projects module", async () => {
    stubExpensesApi({ entries: [], meta: meta({ projectsAvailable: false }) });
    panel();

    expect(await screen.findByText("Projects are not enabled")).toBeInTheDocument();
    expect(screen.queryByTestId("project-expense-totals")).not.toBeInTheDocument();
  });
});

describe("ProjectExpensesPanel — supplier invoices", () => {
  it("offers 'Record a supplier invoice' to the project's financial side, fixed to the project and the kind", async () => {
    // A finance reader: may see the money, on no team — so no "Record a cost".
    const fetchMock = stubExpensesApi({
      entries: [],
      meta: meta({ categories: categoriesWithSubcontractor }),
      canRecordSupplierInvoice: true,
      summaryProject: projectOptions[0],
      projectCurrency: "NOK",
    });
    panel();

    await userEvent.click(await screen.findByRole("button", { name: "Record a supplier invoice" }));
    expect(screen.queryByRole("button", { name: "Record a cost" })).not.toBeInTheDocument();
    const dialog = await screen.findByRole("dialog", { name: "New expense" });
    // The button named its kind, so there is no kind to choose.
    expect(within(dialog).queryByRole("radio", { name: "Outlay" })).not.toBeInTheDocument();
    expect(within(dialog).getByText("Booked on KVEM1000 · Kverneland web")).toBeInTheDocument();
    expect(within(dialog).getByRole("switch", { name: "Billable" })).toBeChecked();
    expect(within(dialog).getByRole("combobox", { name: "Category" })).toHaveValue("Subcontractor");

    await userEvent.type(within(dialog).getByRole("textbox", { name: "Description" }), "Rørleggerarbeid");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Supplier" }), "Rør & Varme AS");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Invoice number" }), "F-1");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Amount including VAT" }), "12500");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save draft" }));

    await waitFor(() => expect(sent(fetchMock, "POST").url).toBe("/api/v1/expenses/entries"));
    expect(sent(fetchMock, "POST").body).toMatchObject({
      kind: "supplier_invoice",
      projectId: PROJECT,
      billable: true,
      categoryId: 14,
    });
  });

  it("offers no supplier invoice without the capability, and 'Record a cost' offers no such kind", async () => {
    stubExpensesApi({ entries: [], projects: projectOptions, canRecord: true, canRecordSupplierInvoice: false });
    panel();

    await userEvent.click(await screen.findByRole("button", { name: "Record a cost" }));
    expect(screen.queryByRole("button", { name: "Record a supplier invoice" })).not.toBeInTheDocument();
    const dialog = await screen.findByRole("dialog", { name: "New expense" });
    expect(within(dialog).getByRole("radio", { name: "Outlay" })).toBeInTheDocument();
    expect(within(dialog).queryByRole("radio", { name: "Supplier invoice" })).not.toBeInTheDocument();
  });

  it("shows a finance reader the project's supplier invoices, and says the totals cover more than them", async () => {
    // The totals hold three lines; the one this reader may open is a
    // colleague's supplier invoice (design D4) — the outlays behind the rest
    // are not theirs.
    stubExpensesApi({
      entries: [
        supplierInvoice({
          owner: { userId: OTHER, displayName: "Grace Hopper", active: true },
          capabilities: capabilities(),
        }),
      ],
      projectSummary: projectSummary({
        projectCurrency: "NOK",
        currencies: [
          summaryCurrency({
            approved: summaryBucket({ count: 3, cost: 11000, billAmount: 11000 }),
            total: summaryBucket({ count: 3, cost: 11000, billAmount: 11000 }),
          }),
        ],
      }),
    });
    panel();

    const found = await row("Rørleggerarbeid, uke 38");
    expect(within(found).getByText("Supplier invoice")).toBeInTheDocument();
    expect(within(found).getByText("Grace Hopper")).toBeInTheDocument();
    expect(await screen.findByTestId("project-expenses-partial")).toHaveTextContent(
      "the supplier invoices if you may see the project's money",
    );
  });

  it("writes the supplier invoices' share beneath a currency's total, and nothing where there is none", async () => {
    stubExpensesApi({
      entries: [],
      projectSummary: projectSummary({
        projectCurrency: "NOK",
        currencies: [
          summaryCurrency({
            currency: "EUR",
            approved: summaryBucket({ count: 1, cost: 90, billAmount: 100 }),
            total: summaryBucket({ count: 1, cost: 90, billAmount: 100 }),
          }),
          summaryCurrency({
            approved: summaryBucket({ count: 3, cost: 14000, billAmount: 15400 }),
            total: summaryBucket({ count: 3, cost: 14000, billAmount: 15400 }),
            supplierInvoices: {
              approved: summaryBucket({ count: 1, cost: 10000, billAmount: 11000 }),
              submitted: summaryBucket(),
              draft: summaryBucket(),
              total: summaryBucket({ count: 1, cost: 10000, billAmount: 11000 }),
            },
          }),
        ],
      }),
    });
    panel();

    const share = await screen.findByTestId("project-expense-supplier-invoices-NOK");
    expect(share).toHaveTextContent("Of which supplier invoices");
    expect(share).toHaveTextContent("1 expense");
    expect(share).toHaveTextContent("10,000.00");
    expect(share).toHaveTextContent("11,000.00");
    // The total above it is still every line.
    expect(screen.getByTestId("project-expense-currency-NOK")).toHaveTextContent("14,000.00");
    expect(screen.queryByTestId("project-expense-supplier-invoices-EUR")).not.toBeInTheDocument();
  });
});
