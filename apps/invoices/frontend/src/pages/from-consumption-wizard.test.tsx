import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { describe, expect, it, vi } from "vitest";
import type { SettlementsView } from "../api/consumption";
import type { InvoiceSettings } from "../api/settings";
import { jsonResponse, path, problemResponse, refusal } from "../test/api";
import { requestTo } from "../test/document-server";
import { stubFetch } from "../test/fetch";
import { draft, exemptSettlement, fromWorkDraft, meta, settings, settlementsView, vatCodes } from "../test/fixtures";
import { renderAtHost } from "../test/route-tree";
import { FromConsumptionWizard } from "./-from-consumption-wizard";

interface Options {
  view?: SettlementsView;
  settings?: Partial<InvoiceSettings>;
  /** What POST /from-consumption answers. */
  answer?: () => Response;
}

const SETTLEMENTS = "/api/v1/invoices/consumption/settlements?customerId=2001";
const POST = "/api/v1/invoices/from-consumption";

const server = ({ view = settlementsView(), settings: own = {}, answer }: Options = {}) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = path(input);
    const method = init?.method ?? "GET";
    if (url === "/api/v1/invoices/meta") return jsonResponse(200, meta());
    if (url.startsWith(SETTLEMENTS)) return jsonResponse(200, view);
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
          totalCount: 1,
          totalPages: 1,
          hasNextPage: false,
          hasPreviousPage: false,
        },
      });
    }
    if (url === "/api/v1/invoices/1003") return jsonResponse(200, draft({ id: 1003, revision: 7 }));
    if (url === POST && method === "POST") return answer ? answer() : jsonResponse(201, fromWorkDraft());
    return new Response(null, { status: 404 });
  });

const open = async () => {
  const onClose = vi.fn();
  const { router } = renderAtHost(<FromConsumptionWizard customerId={2001} onClose={onClose} />);
  const dialog = await screen.findByRole("dialog", { name: "Invoice consumption" });
  await within(dialog).findByTestId("metering-point-21");
  return { dialog, router, onClose };
};

const create = (dialog: HTMLElement) => userEvent.click(within(dialog).getByRole("button", { name: "Create draft" }));

describe("the From consumption wizard", () => {
  it("settles through today and lists the settlements per metering point, with their lines and reasons", async () => {
    const fetchMock = server();
    const { dialog } = await open();

    expect(fetchMock.actualCalls.some(([url]) => path(url) === `${SETTLEMENTS}&through=2026-09-12`)).toBe(true);
    const storgata = within(dialog).getByTestId("metering-point-21");
    expect(within(storgata).getByText("707057500000000011 · Storgata 1, 0155 Oslo")).toBeInTheDocument();

    // August: chosen, its lines with the band, the unit and the eight-decimal price.
    const august = within(dialog).getByRole("checkbox", {
      name: "Choose 707057500000000011, Aug 1, 2026 – Aug 31, 2026",
    });
    expect(august).toBeChecked();
    expect(within(storgata).getByText("512.5 kWh")).toBeInTheDocument();
    const lines = within(dialog).getByRole("table", {
      name: "Lines of 707057500000000011, Aug 1, 2026 – Aug 31, 2026",
    });
    expect(within(lines).getByText("Strøm (day)")).toBeInTheDocument();
    expect(within(lines).getByText(/1\.23456789/)).toBeInTheDocument();
    expect(within(lines).getByText("months")).toBeInTheDocument();

    // The July correction: estimated, held by draft 1003, a link to it.
    const july = within(dialog).getByRole("checkbox", {
      name: "Choose 707057500000000011, Jul 1, 2026 – Jul 31, 2026",
    });
    expect(july).toBeDisabled();
    expect(within(storgata).getByText("Correction")).toBeInTheDocument();
    expect(within(storgata).getByText("The consumption is partly estimated")).toBeInTheDocument();
    expect(within(storgata).getByRole("link", { name: "On draft 1003" })).toHaveAttribute("href", "/invoices/1003");

    // The cabin's quarter: blocked, in words, with Energy's detail.
    const cabin = within(dialog).getByTestId("metering-point-22");
    expect(within(cabin).getByRole("checkbox")).toBeDisabled();
    expect(within(cabin).getByText("Spot prices are missing — NO1 2026-05-03")).toBeInTheDocument();

    expect(within(dialog).getByTestId("consumption-chosen")).toHaveTextContent("1 chosen: NOK 812.34");
  });

  it("asks only for the VAT codes the chosen lines' hints need, from the settings' energy codes", async () => {
    const view = settlementsView();
    view.settlements.push(exemptSettlement());
    server({ view, settings: { energyVatCodes: { energy: 2, energyExempt: 5 } } });
    const { dialog } = await open();

    expect(await within(dialog).findByRole("combobox", { name: "VAT code for electricity" })).toHaveValue(
      "31 — Utgående mva 15 %",
    );
    expect(within(dialog).getByRole("combobox", { name: "VAT code for exempt electricity (Nord-Norge)" })).toHaveValue(
      "5 — Fritatt innenlands 0 %",
    );

    // Untick the exempt one: its code is no longer asked for.
    await userEvent.click(
      within(dialog).getByRole("checkbox", { name: "Choose 707057500000000035, Aug 1, 2026 – Aug 31, 2026" }),
    );
    expect(
      within(dialog).queryByRole("combobox", { name: "VAT code for exempt electricity (Nord-Norge)" }),
    ).not.toBeInTheDocument();
  });

  it("invoices every line outside the VAT act for a seller not registered", async () => {
    const fetchMock = server({ settings: { vatRegistered: false } });
    const { dialog } = await open();
    expect(await within(dialog).findByRole("combobox", { name: "VAT code for electricity" })).toHaveValue(
      "3G — Gammel sats (no longer offered)",
    );
    await create(dialog);
    await waitFor(() => expect(requestTo(fetchMock, "POST", POST)?.vatCodes).toEqual({ energy: 9 }));
  });

  it("creates the draft of the settlements chosen, naming them, and opens it", async () => {
    const view = settlementsView();
    view.settlements.push(exemptSettlement());
    const fetchMock = server({ view });
    const { dialog, router, onClose } = await open();

    await userEvent.click(within(dialog).getByRole("combobox", { name: "VAT code for electricity" }));
    await userEvent.click(await screen.findByRole("option", { name: "31 — Utgående mva 15 %" }));
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Note" }), "Takk");
    await create(dialog);

    await waitFor(() => expect(router.state.location.pathname).toBe("/invoices/1002"));
    expect(requestTo(fetchMock, "POST", POST)).toEqual({
      customerId: 2001,
      through: "2026-09-12",
      settlementIds: [4001, 4004],
      vatCodes: { energy: 2, energyExempt: 5 },
      note: "Takk",
    });
    expect(onClose).toHaveBeenCalled();
    expect(await screen.findByText("The draft is created")).toBeInTheDocument();
  });

  it("takes nothing once every settlement is unticked", async () => {
    server();
    const { dialog } = await open();
    await userEvent.click(
      within(dialog).getByRole("checkbox", { name: "Choose 707057500000000011, Aug 1, 2026 – Aug 31, 2026" }),
    );
    expect(within(dialog).getByRole("button", { name: "Create draft" })).toBeDisabled();
    expect(within(dialog).queryByRole("combobox", { name: "VAT code for electricity" })).not.toBeInTheDocument();
  });

  it("adds the consumption to one of the customer's drafts at the revision it read", async () => {
    const fetchMock = server({ answer: () => jsonResponse(200, draft({ id: 1003, revision: 8 })) });
    const { dialog, router } = await open();

    await userEvent.click(within(dialog).getByRole("combobox", { name: "Put the consumption on" }));
    await userEvent.click(await screen.findByRole("option", { name: /^Draft 1003 — NOK\s500\.00$/ }));
    const add = await within(dialog).findByRole("button", { name: "Add to draft 1003" });
    await waitFor(() => expect(add).toBeEnabled());
    await userEvent.click(add);

    await waitFor(() => expect(router.state.location.pathname).toBe("/invoices/1003"));
    expect(requestTo(fetchMock, "POST", POST)).toMatchObject({ invoiceId: 1003, revision: 7, settlementIds: [4001] });
    expect(await screen.findByText("The consumption is added to the draft")).toBeInTheDocument();
  });

  it("says there is nothing to invoice through the day", async () => {
    server({ view: settlementsView({ settlements: [] }) });
    renderAtHost(<FromConsumptionWizard customerId={2001} onClose={() => {}} />);
    expect(await screen.findByText("Nothing to invoice through this day")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create draft" })).toBeDisabled();
  });

  it("names a blocked settlement the server refused, with its blocker, and stays open", async () => {
    const view = settlementsView();
    view.settlements[0] = { ...view.settlements[0], blocked: "incomplete", blockedDetail: "14 intervals" };
    server({
      view,
      answer: () => refusal(409, "settlement_blocked", { sourceKind: "energy.settlement", sourceId: 4001 }),
    });
    const { dialog } = await open();
    await create(dialog);

    const alert = await within(dialog).findByRole("alert");
    expect(within(alert).getByText("A chosen settlement cannot be billed yet.")).toBeInTheDocument();
    expect(
      within(alert).getByText(
        "The settlement refused: 707057500000000011, Aug 1, 2026 – Aug 31, 2026 Readings are missing: 14 intervals.",
      ),
    ).toBeInTheDocument();
    expect(within(alert).queryByText("The server's English.")).not.toBeInTheDocument();
  });

  it("names the document that holds a settlement, with a link to it", async () => {
    server({
      answer: () =>
        refusal(409, "source_held_elsewhere", {
          sourceKind: "energy.settlement",
          sourceId: 4001,
          heldBy: { invoiceId: 990, number: 985, status: "issued" },
        }),
    });
    const { dialog } = await open();
    await create(dialog);

    const alert = await within(dialog).findByRole("alert");
    expect(
      within(alert).getByText("A chosen settlement is already on another draft or an issued invoice."),
    ).toBeInTheDocument();
    expect(within(alert).getByRole("link", { name: "On invoice 985" })).toHaveAttribute("href", "/invoices/990");
  });

  it.each([
    ["consumption_unavailable", "The Energy module is switched off, so no consumption can be invoiced."],
    [
      "cadence_not_allowed",
      "A chosen settlement is of a yearly supply to a person, and a consumer is billed at least quarterly.",
    ],
    ["no_consumption_due", /^Nothing is left to invoice through this day/],
    ["too_many_sources", /^One document holds at most 5 000 sources/],
    ["too_many_lines", /^The settlements make more than 500 lines/],
    ["source_not_for_customer", "A chosen settlement belongs to another customer."],
    ["source_not_invoiceable", /^A chosen settlement can no longer be invoiced/],
    ["currency_not_nok", "A chosen settlement is not in NOK, the only currency this module invoices in."],
  ])("says %s in words", async (code, words) => {
    server({ answer: () => refusal(409, code) });
    const { dialog } = await open();
    await create(dialog);
    const alert = await within(dialog).findByRole("alert");
    expect(within(alert).getByText(words)).toBeInTheDocument();
  });

  it("puts a refused VAT code on its own input", async () => {
    server({
      answer: () => problemResponse(400, "Invalid request", { "vatCodes.energy": ["VAT code 1 is not active"] }),
    });
    const { dialog } = await open();
    await create(dialog);
    await waitFor(() =>
      expect(within(dialog).getByRole("combobox", { name: "VAT code for electricity" })).toHaveAccessibleDescription(
        "Choose a VAT code that is offered for new lines.",
      ),
    );
    expect(within(dialog).queryByRole("alert")).not.toBeInTheDocument();
  });

  it("words the wizard from the nb catalog", async () => {
    setLanguagePreference("nb");
    try {
      server();
      renderAtHost(<FromConsumptionWizard customerId={2001} onClose={() => {}} />);
      const dialog = await screen.findByRole("dialog", { name: "Fakturer forbruk" });
      expect(await within(dialog).findByText("Forbruket er delvis stipulert")).toBeInTheDocument();
      expect(within(dialog).getByText("Korreksjon")).toBeInTheDocument();
      expect(within(dialog).getByText("Spotpriser mangler — NO1 2026-05-03")).toBeInTheDocument();
      expect(within(dialog).getAllByText("Strøm (dag)").length).toBeGreaterThan(0);
      expect(within(dialog).getByRole("combobox", { name: "Mva-kode for strøm" })).toBeInTheDocument();
    } finally {
      setLanguagePreference("auto");
    }
  });
});
