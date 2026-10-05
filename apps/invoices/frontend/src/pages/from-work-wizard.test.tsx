import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { InvoiceSettings } from "../api/settings";
import type { WorkView } from "../api/work";
import { UninvoicedWorkPanel } from "../components/uninvoiced-work-panel";
import { jsonResponse, path, refusal } from "../test/api";
import { requestTo } from "../test/document-server";
import { stubFetch } from "../test/fetch";
import { draft, fromWorkDraft, meta, settings, vatCodes, workView } from "../test/fixtures";
import { renderAtHost } from "../test/route-tree";

/**
 * Three selectable hours on P-41 — Kari at 1200 on two days and two work
 * types, Ola at 1500 — an outlay and a mileage expense, and a milestone: the
 * view the line counts are worked out on.
 */
const wideView = (): WorkView => {
  const view = workView();
  const [apollo] = view.projects;
  const hour = apollo.hours[0];
  apollo.hours = [
    hour,
    { ...hour, id: 804, date: "2026-09-02", workTypeId: 6, workTypeName: "Rådgivning" },
    {
      ...hour,
      id: 805,
      date: "2026-09-02",
      userId: "00000000-0000-0000-0000-00000000000b",
      rate: 1500,
      billRate: 1500,
      amount: 6000,
    },
  ];
  apollo.expenses = [
    apollo.expenses[0],
    { ...apollo.expenses[0], id: 903, kind: "outlay", description: "Hotell", billAmount: 1200, netAmount: 1200 },
  ];
  return { ...view, projects: [apollo] };
};

interface Options {
  /** The view, or a function giving it per read — work that moves under the panel. */
  view?: WorkView | (() => WorkView);
  /** Whether the customer has more drafts than one page of the list holds. */
  moreDrafts?: boolean;
  settings?: Partial<InvoiceSettings>;
  /** What POST /from-work answers. */
  answer?: () => Response;
}

const server = ({ view = workView(), settings: own = {}, answer, moreDrafts = false }: Options = {}) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = path(input);
    const method = init?.method ?? "GET";
    if (url === "/api/v1/invoices/meta") return jsonResponse(200, meta());
    if (url === "/api/v1/invoices/work?customerId=2001") {
      return jsonResponse(200, typeof view === "function" ? view() : view);
    }
    if (url === "/api/v1/invoices/settings") return jsonResponse(200, settings(own));
    if (url === "/api/v1/invoices/vat-codes") return jsonResponse(200, vatCodes());
    if (url === "/api/v1/invoices?customerId=2001&status=draft&kind=invoice&pageSize=100") {
      return jsonResponse(200, {
        data: [
          {
            id: 1003,
            kind: "invoice",
            status: "draft",
            state: "draft",
            customerId: 2001,
            currency: "NOK",
            grossTotal: 500,
          },
        ],
        pagination: {
          page: 1,
          pageSize: 100,
          totalCount: moreDrafts ? 101 : 1,
          totalPages: moreDrafts ? 2 : 1,
          hasNextPage: moreDrafts,
          hasPreviousPage: false,
        },
      });
    }
    if (url === "/api/v1/invoices/1003") return jsonResponse(200, draft({ id: 1003, revision: 7, timesheet: true }));
    if (url === "/api/v1/invoices/from-work" && method === "POST") {
      return answer ? answer() : jsonResponse(201, fromWorkDraft());
    }
    return new Response(null, { status: 404 });
  });

/** Opens the wizard on Kari's hours, or on every row of the wide view. */
const openWizard = async (everything = false) => {
  const { router } = renderAtHost(<UninvoicedWorkPanel customerId={2001} />);
  if (everything) {
    await userEvent.click(await screen.findByRole("checkbox", { name: "Choose all Hours on P-41" }));
    await userEvent.click(screen.getByRole("checkbox", { name: "Choose all Expenses on P-41" }));
    await userEvent.click(screen.getByRole("checkbox", { name: "Choose all Milestones on P-41" }));
  } else {
    await userEvent.click(await screen.findByRole("checkbox", { name: "Choose Kari Nordmann, Sep 1, 2026" }));
  }
  await userEvent.click(screen.getByRole("button", { name: "Invoice the chosen work" }));
  const dialog = await screen.findByRole("dialog", { name: "Invoice the work" });
  return { dialog, router };
};

describe("the wizard", () => {
  it("says how many lines each grouping would make of the chosen work", async () => {
    server({ view: wideView() });
    const { dialog } = await openWizard(true);

    expect(within(dialog).getByTestId("wizard-selection")).toHaveTextContent("6 chosen: NOK 27,205.00");
    // Hours split by rate within every grouping; the two expense kinds are a
    // line each but itemised; the milestone is always its own.
    expect(within(dialog).getByRole("radio", { name: "One line per project — 5 lines" })).toBeChecked();
    expect(within(dialog).getByRole("radio", { name: "Per work type — 6 lines" })).toBeInTheDocument();
    expect(within(dialog).getByRole("radio", { name: "Per person — 5 lines" })).toBeInTheDocument();
    expect(within(dialog).getByRole("radio", { name: "Per day — 6 lines" })).toBeInTheDocument();
    expect(within(dialog).getByRole("radio", { name: "Itemised — 6 lines" })).toBeInTheDocument();
  });

  it("pre-fills the settings' VAT code per kind and timesheet default, for the kinds chosen", async () => {
    server({
      view: wideView(),
      settings: { workVatCodes: { hours: 1, expenses: 2, milestones: 5 }, timesheetDefault: true },
    });
    const { dialog } = await openWizard(true);

    expect(await within(dialog).findByRole("combobox", { name: "VAT code for hours" })).toHaveValue(
      "3 — Utgående mva 25 %",
    );
    expect(within(dialog).getByRole("combobox", { name: "VAT code for expenses" })).toHaveValue(
      "31 — Utgående mva 15 %",
    );
    expect(within(dialog).getByRole("combobox", { name: "VAT code for milestones" })).toHaveValue(
      "5 — Fritatt innenlands 0 %",
    );
    expect(within(dialog).getByRole("checkbox", { name: /Attach a timesheet/ })).toBeChecked();
  });

  it("asks only for the codes of the kinds chosen, and every kind outside the VAT act for a seller not registered", async () => {
    const fetchMock = server({ settings: { vatRegistered: false } });
    const { dialog } = await openWizard();

    expect(within(dialog).queryByRole("combobox", { name: "VAT code for expenses" })).not.toBeInTheDocument();
    expect(await within(dialog).findByRole("combobox", { name: "VAT code for hours" })).toHaveValue(
      "3G — Gammel sats (no longer offered)",
    );
    await userEvent.click(within(dialog).getByRole("button", { name: "Create draft" }));
    await waitFor(() =>
      expect(requestTo(fetchMock, "POST", "/api/v1/invoices/from-work")?.vatCodes).toEqual({ hours: 9 }),
    );
  });

  it("creates the draft of the chosen work and opens it", async () => {
    const fetchMock = server();
    const { dialog, router } = await openWizard();

    await userEvent.click(within(dialog).getByRole("radio", { name: /^Per person/ }));
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Note" }), "Takk for oppdraget");
    await userEvent.click(within(dialog).getByRole("button", { name: "Create draft" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/invoices/1002"));
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/from-work")).toEqual({
      customerId: 2001,
      sources: [{ kind: "time.entry", id: 801, revision: 2 }],
      grouping: "person",
      vatCodes: { hours: 1 },
      note: "Takk for oppdraget",
    });
    expect(await screen.findByText("The draft is created")).toBeInTheDocument();
  });

  it("adds the work to one of the customer's drafts at the revision it read, with a timesheet as asked", async () => {
    const fetchMock = server({ answer: () => jsonResponse(200, draft({ id: 1003, revision: 8 })) });
    const { dialog, router } = await openWizard();

    await userEvent.click(within(dialog).getByRole("combobox", { name: "Put the work on" }));
    await userEvent.click(await screen.findByRole("option", { name: /^Draft 1003 — NOK\s500\.00$/ }));
    const add = await within(dialog).findByRole("button", { name: "Add to draft 1003" });
    // The target's own flag is what an append keeps unless the person says.
    await waitFor(() => expect(within(dialog).getByRole("checkbox", { name: /Attach a timesheet/ })).toBeChecked());
    await userEvent.click(within(dialog).getByRole("checkbox", { name: /Attach a timesheet/ }));
    await waitFor(() => expect(add).toBeEnabled());
    await userEvent.click(add);

    await waitFor(() => expect(router.state.location.pathname).toBe("/invoices/1003"));
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/from-work")).toMatchObject({
      invoiceId: 1003,
      revision: 7,
      timesheet: false,
    });
    expect(await screen.findByText("The work is added to the draft")).toBeInTheDocument();
  });

  it("names the document that holds the work, with a link to it, and stays open", async () => {
    server({
      answer: () =>
        refusal(409, "source_held_elsewhere", {
          heldBy: { invoiceId: 1005, status: "draft" },
          sourceKind: "time.entry",
          sourceId: 801,
        }),
    });
    const { dialog, router } = await openWizard();
    await userEvent.click(within(dialog).getByRole("button", { name: "Create draft" }));

    const alert = await within(dialog).findByRole("alert");
    expect(alert).toHaveTextContent("Some of this work is already on another draft or an issued invoice.");
    expect(within(alert).getByRole("link", { name: "On draft 1005" })).toHaveAttribute("href", "/invoices/1005");
    expect(alert).not.toHaveTextContent("The server's English.");
    expect(router.state.location.pathname).toBe("/here");
  });

  it("chooses the grouping too_many_lines suggests, and says so", async () => {
    server({ answer: () => refusal(409, "too_many_lines", { suggestedGrouping: "date" }) });
    const { dialog } = await openWizard();
    await userEvent.click(within(dialog).getByRole("radio", { name: /^Itemised/ }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Create draft" }));

    const alert = await within(dialog).findByRole("alert");
    expect(alert).toHaveTextContent("Grouped this way the work makes more than 500 lines.");
    expect(alert).toHaveTextContent("“Per day” fits in 500 lines, so the lines are now grouped that way.");
    expect(within(dialog).getByRole("radio", { name: /^Per day/ })).toBeChecked();
  });

  it.each([
    ["source_changed", "Some of the chosen work changed since it was listed."],
    ["source_not_invoiceable", "Some of the chosen work can no longer be invoiced"],
    ["source_not_selectable", "Some of the chosen work is on a fixed-price or non-billable project"],
    ["projects_unavailable", "Work is invoiced per project, and the Projects module is switched off."],
    ["source_not_for_customer", "Some of the chosen work belongs to a project that does not bill this customer."],
    ["mixed_currency", "The chosen work is in more than one currency"],
    ["currency_not_nok", "The chosen work is not in NOK"],
    ["too_many_sources", "One document holds at most 5 000 pieces of work."],
    ["work_unavailable", "No module that records billable work"],
    ["invoice_issued", "The document is already issued."],
    ["customer_blocked", "The customer is blocked for invoicing."],
  ])("says %s in words", async (code, words) => {
    server({ answer: () => refusal(409, code) });
    const { dialog } = await openWizard();
    await userEvent.click(within(dialog).getByRole("button", { name: "Create draft" }));

    const alert = await within(dialog).findByRole("alert");
    expect(alert).toHaveTextContent(words);
    expect(alert).not.toHaveTextContent("The server's English.");
  });

  it("says a draft that moved on since it was read, and reads it again", async () => {
    const fetchMock = server({
      answer: () => jsonResponse(409, { title: "Conflict", status: 409, detail: "Invoice revision 8 is current" }),
    });
    const { dialog } = await openWizard();
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Put the work on" }));
    await userEvent.click(await screen.findByRole("option", { name: /^Draft 1003 — NOK\s500\.00$/ }));
    const add = await within(dialog).findByRole("button", { name: "Add to draft 1003" });
    await waitFor(() => expect(add).toBeEnabled());
    await userEvent.click(add);

    expect(await within(dialog).findByRole("alert")).toHaveTextContent("The invoice changed; try again.");
    await waitFor(() =>
      expect(fetchMock.actualCalls.filter(([url]) => path(url) === "/api/v1/invoices/1003").length).toBe(2),
    );
  });

  it("puts a refused VAT code on its own input", async () => {
    server({
      answer: () =>
        jsonResponse(400, {
          title: "Invalid",
          status: 400,
          errors: { "vatCodes.hours": ["VAT code 9 is not active"] },
        }),
    });
    const { dialog } = await openWizard();
    await userEvent.click(within(dialog).getByRole("button", { name: "Create draft" }));

    await waitFor(() =>
      expect(within(dialog).getByRole("combobox", { name: "VAT code for hours" })).toHaveAccessibleDescription(
        "Choose a VAT code that is offered for new lines.",
      ),
    );
    expect(within(dialog).queryByRole("alert")).not.toBeInTheDocument();
  });

  // R26: what a refusal leaves chosen is what can still be chosen. The work
  // is read again after a source_ refusal, and a row now held drops out of
  // the choice, its totals and the next body.
  it("reads the work again after a source_changed refusal, naming the row, and leaves out what is now held", async () => {
    let held = false;
    const view = () => {
      const v = workView();
      if (held)
        v.projects[0].hours[0] = {
          ...v.projects[0].hours[0],
          selectable: false,
          reason: "held",
          heldBy: { invoiceId: 1007, status: "draft" },
        };
      return v;
    };
    let posts = 0;
    const fetchMock = server({
      view,
      answer: () => {
        posts += 1;
        if (posts > 1) return jsonResponse(201, fromWorkDraft());
        held = true;
        return refusal(409, "source_changed", { sourceKind: "time.entry", sourceId: 801 });
      },
    });
    renderAtHost(<UninvoicedWorkPanel customerId={2001} />);
    await userEvent.click(await screen.findByRole("checkbox", { name: "Choose Kari Nordmann, Sep 1, 2026" }));
    await userEvent.click(screen.getByRole("checkbox", { name: "Choose Fase 1" }));
    await userEvent.click(screen.getByRole("button", { name: "Invoice the chosen work" }));
    const dialog = await screen.findByRole("dialog", { name: "Invoice the work" });
    expect(within(dialog).getByTestId("wizard-selection")).toHaveTextContent("2 chosen: NOK 14,800.00");
    await userEvent.click(within(dialog).getByRole("button", { name: "Create draft" }));

    const alert = await within(dialog).findByRole("alert");
    expect(alert).toHaveTextContent("Some of the chosen work changed since it was listed.");
    expect(alert).toHaveTextContent("The work refused: Kari Nordmann, Sep 1, 2026.");
    await waitFor(() =>
      expect(within(dialog).getByTestId("wizard-selection")).toHaveTextContent("1 chosen: NOK 10,000.00"),
    );
    expect(screen.getByTestId("work-chosen")).toHaveTextContent("1 chosen: NOK 10,000.00");

    await userEvent.click(within(dialog).getByRole("button", { name: "Create draft" }));
    await waitFor(() => expect(posts).toBe(2));
    const bodies = fetchMock.actualCalls
      .filter(([url, init]) => path(url) === "/api/v1/invoices/from-work" && init?.method === "POST")
      .map(([, init]) => JSON.parse(String(init?.body)));
    expect(bodies[1].sources).toEqual([{ kind: "projects.milestone", id: 951, revision: 1 }]);
  });

  it("names the row a 400 on sources[i] is about", async () => {
    server({
      answer: () =>
        jsonResponse(400, { title: "Invalid", status: 400, errors: { "sources[1]": ["sources[1] is named twice"] } }),
    });
    renderAtHost(<UninvoicedWorkPanel customerId={2001} />);
    await userEvent.click(await screen.findByRole("checkbox", { name: "Choose Kari Nordmann, Sep 1, 2026" }));
    await userEvent.click(screen.getByRole("checkbox", { name: "Choose Fase 1" }));
    await userEvent.click(screen.getByRole("button", { name: "Invoice the chosen work" }));
    const dialog = await screen.findByRole("dialog", { name: "Invoice the work" });
    await userEvent.click(within(dialog).getByRole("button", { name: "Create draft" }));

    const alert = await within(dialog).findByRole("alert");
    expect(alert).toHaveTextContent("The chosen work could not be taken as it is.");
    expect(alert).toHaveTextContent("The work refused: Fase 1.");
    expect(alert).not.toHaveTextContent("Kari Nordmann");
  });

  it("says when the customer has more drafts than the list offers", async () => {
    server({ moreDrafts: true });
    const { dialog } = await openWizard();
    expect(await within(dialog).findByText(/Only 100 of the customer's drafts are listed/)).toBeInTheDocument();
  });
});
