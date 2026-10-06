import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { InvoiceSettings } from "../api/settings";
import type { VatCode, VatCodeCreateInput, VatCodeUpdateInput } from "../api/vat-codes";
import { jsonResponse, sent } from "../test/api";
import { stubFetch } from "../test/fetch";
import { meta, settings, vatCodes } from "../test/fixtures";
import { renderWithProviders } from "../test/render";
import { SettingsPage } from "./settings";

const path = (input: RequestInfo | URL) => String(input);

/** The caller the page is for: meta's `canManage` is what shows it (D12). */
const manager = {
  canCreate: true,
  canImportBankFiles: false,
  canIssue: true,
  canManage: true,
  canRegisterPayments: false,
  canRunReminders: false,
  canSend: false,
  canSendEhf: false,
};

/** What the fake server holds, which a test may change under the page as another user would. */
interface World {
  settings: InvoiceSettings;
  codes: VatCode[];
}

const world = (): World => ({ settings: settings(), codes: vatCodes() });

/**
 * The fetch fake, a small model of the rules the page relies on: the settings
 * and the codes are state; the settings are saved only at their current
 * revision — a stale one is a 409 without a code — and removing the latest
 * period reopens the one before it, as the server does (D2, D3). `refuse`
 * answers a "METHOD url" with a refusal instead.
 */
const server = (refuse: Record<string, Response> = {}, state: World = world()) =>
  stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = path(input);
    const method = init?.method ?? "GET";
    const refusal = refuse[`${method} ${url}`];
    if (refusal) return refusal.clone();
    if (url === "/api/v1/invoices/meta") return jsonResponse(200, meta({ capabilities: manager }));
    if (url === "/api/v1/invoices/settings" && method === "PUT") {
      const { revision, ...body } = JSON.parse(String(init?.body)) as InvoiceSettings;
      if (revision !== state.settings.revision) {
        return jsonResponse(409, {
          title: "Invoice settings revision conflict",
          status: 409,
          detail: `The Invoice settings has revision ${state.settings.revision}; the supplied revision was ${revision}.`,
        });
      }
      state.settings = {
        ...state.settings,
        ...body,
        missingSellerFields: state.settings.missingSellerFields.filter((f) => !body[f as keyof typeof body]),
        revision: revision + 1,
      };
      return jsonResponse(200, state.settings);
    }
    if (url === "/api/v1/invoices/settings") return jsonResponse(200, state.settings);
    if (url === "/api/v1/invoices/vat-codes" && method === "GET") return jsonResponse(200, state.codes);
    const body = init?.body ? JSON.parse(String(init.body)) : {};
    if (url === "/api/v1/invoices/vat-codes" && method === "POST") {
      const { ratePercent, validFrom, ...own } = body as VatCodeCreateInput;
      const created: VatCode = {
        ...own,
        id: 100 + state.codes.length,
        active: true,
        inUse: false,
        revision: 1,
        rates: [{ id: 2000 + state.codes.length, ratePercent, validFrom }],
      };
      state.codes = [...state.codes, created];
      return jsonResponse(201, created);
    }
    const one = /^\/api\/v1\/invoices\/vat-codes\/(\d+)$/.exec(url);
    if (one && method === "PUT") {
      const { revision, ...own } = body as VatCodeUpdateInput;
      const code = state.codes.find((c) => c.id === Number(one[1]));
      if (!code) return new Response(null, { status: 404 });
      if (revision !== code.revision) {
        return jsonResponse(409, {
          title: "VAT code revision conflict",
          status: 409,
          detail: `The VAT code has revision ${code.revision}; the supplied revision was ${revision}.`,
        });
      }
      const updated: VatCode = { ...code, ...own, revision: revision + 1 };
      state.codes = state.codes.map((c) => (c.id === code.id ? updated : c));
      return jsonResponse(200, updated);
    }
    // A new period: the open one closes the day before it starts (D3).
    const rates = /^\/api\/v1\/invoices\/vat-codes\/(\d+)\/rates$/.exec(url);
    if (rates && method === "POST") {
      const { ratePercent, validFrom } = body as { ratePercent: number; validFrom: string };
      const dayBefore = new Date(Date.parse(`${validFrom}T00:00:00Z`) - 86_400_000).toISOString().slice(0, 10);
      state.codes = state.codes.map((c) => {
        if (c.id !== Number(rates[1])) return c;
        const open = { ...c.rates[c.rates.length - 1], validTo: dayBefore };
        const added = { id: 3000 + c.rates.length, ratePercent, validFrom };
        return { ...c, rates: [...c.rates.slice(0, -1), open, added], revision: c.revision + 1 };
      });
      return jsonResponse(
        201,
        state.codes.find((c) => c.id === Number(rates[1])),
      );
    }
    const period = /^\/api\/v1\/invoices\/vat-codes\/(\d+)\/rates\/(\d+)$/.exec(url);
    if (period && method === "DELETE") {
      const [id, rateId] = [Number(period[1]), Number(period[2])];
      state.codes = state.codes.map((c) => {
        if (c.id !== id) return c;
        const rates = c.rates.filter((r) => r.id !== rateId);
        const reopened = { ...rates[rates.length - 1], validTo: undefined };
        return { ...c, rates: [...rates.slice(0, -1), reopened], revision: c.revision + 1 };
      });
      return jsonResponse(
        200,
        state.codes.find((c) => c.id === id),
      );
    }
    return new Response(null, { status: 404 });
  });

const refusal = (code: string) =>
  jsonResponse(409, { type: "about:blank", title: "Refused", status: 409, code, detail: "The server's English." });

describe("the invoice settings", () => {
  it("lists what issuing still needs and saves the seller with its revision", async () => {
    const fetchMock = server();
    renderWithProviders(<SettingsPage />);

    const checklist = await screen.findByRole("list", { name: "What issuing needs" });
    expect(within(checklist).getByText("Postal code is missing")).toBeInTheDocument();
    expect(within(checklist).getByText("Bank account is missing")).toBeInTheDocument();
    expect(within(checklist).getByText("Legal name")).toBeInTheDocument();

    await userEvent.type(screen.getByRole("textbox", { name: "Postal code" }), "0155");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT").url).toBe("/api/v1/invoices/settings"));
    expect(sent(fetchMock, "PUT").body).toMatchObject({
      postalCode: "0155",
      revision: 5,
      seriesStart: 1000,
      defaultCurrency: "NOK",
    });
  });

  it("sends the KID agreement back as it read it", async () => {
    const state = world();
    state.settings = settings({ peppolId: "9908:974760673", kidLength: 7, kidAlgorithm: "mod10" });
    const fetchMock = server({}, state);
    renderWithProviders(<SettingsPage />);

    await userEvent.type(await screen.findByRole("textbox", { name: "Postal code" }), "0155");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT").url).toBe("/api/v1/invoices/settings"));
    expect(sent(fetchMock, "PUT").body).toMatchObject({
      peppolId: "9908:974760673",
      kidLength: 7,
      kidAlgorithm: "mod10",
    });
  });

  it("sends a Peppol id derived from the organisation number as null, so a changed number derives it again", async () => {
    const state = world();
    state.settings = settings({ peppolId: "0192:974760673" });
    const fetchMock = server({}, state);
    renderWithProviders(<SettingsPage />);

    const number = await screen.findByRole("textbox", { name: "Organisation number" });
    await userEvent.clear(number);
    await userEvent.type(number, "923609016");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT").url).toBe("/api/v1/invoices/settings"));
    expect(sent(fetchMock, "PUT").body).toMatchObject({ organisationNumber: "923609016", peppolId: null });
  });

  it("sends a Peppol id that is not the derived one back as it read it", async () => {
    const state = world();
    state.settings = settings({ peppolId: "9908:974760673" });
    const fetchMock = server({}, state);
    renderWithProviders(<SettingsPage />);

    await userEvent.type(await screen.findByRole("textbox", { name: "Postal code" }), "0155");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT").url).toBe("/api/v1/invoices/settings"));
    expect(sent(fetchMock, "PUT").body).toMatchObject({ peppolId: "9908:974760673" });
  });

  it("sends the three nullable fields as null when the settings have none", async () => {
    const fetchMock = server();
    renderWithProviders(<SettingsPage />);

    await userEvent.type(await screen.findByRole("textbox", { name: "Postal code" }), "0155");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(sent(fetchMock, "PUT").url).toBe("/api/v1/invoices/settings"));
    const body = sent(fetchMock, "PUT").body;
    expect(body).toHaveProperty("peppolId", null);
    expect(body).toHaveProperty("kidLength", null);
    expect(body).toHaveProperty("kidAlgorithm", null);
  });

  it.each([
    [true, "Mail is configured (SMTP)"],
    [false, "Mail is not configured (SMTP), so documents cannot be sent by e-mail. Issuing does not need it."],
  ])(
    "says in the checklist whether mail is configured (%s), which never holds issuing back",
    async (mailAvailable, words) => {
      server({
        "GET /api/v1/invoices/meta": jsonResponse(200, meta({ capabilities: manager, mailAvailable })),
      });
      renderWithProviders(<SettingsPage />);

      const checklist = await screen.findByRole("list", { name: "What issuing needs" });
      const line = await within(checklist).findByText(words);
      expect(line.closest("li")).toHaveAttribute("data-informative", "true");
    },
  );

  it("shows the series start read-only once anything is issued", async () => {
    server();
    renderWithProviders(<SettingsPage />);
    const start = await screen.findByRole("textbox", { name: "The number series starts at" });
    expect(start).toBeDisabled();
    expect(screen.getByText("Locked: documents are issued from this series.")).toBeInTheDocument();
  });

  it("shows a code's rate periods, adds one from a date and removes the latest future one", async () => {
    const fetchMock = server();
    renderWithProviders(<SettingsPage />);

    const row = (await screen.findByText("Utgående mva 25 %")).closest("tr") as HTMLElement;
    // Written by the locale's percent format, not a suffix: "25%" in en.
    expect(within(row).getByText("25%")).toBeInTheDocument();
    await userEvent.click(within(row).getByRole("button", { name: "Rate periods" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("Jan 1, 2026 – Dec 31, 2026")).toBeInTheDocument();
    expect(within(dialog).getByText("From Jan 1, 2027")).toBeInTheDocument();

    // A new period from a date: the open one closes the day before.
    const rate = within(dialog).getByRole("textbox", { name: "New rate %" });
    await userEvent.clear(rate);
    await userEvent.type(rate, "27");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Valid from" }), "Jun 1, 2027");
    await userEvent.click(within(dialog).getByRole("button", { name: "Change the rate from this date" }));
    await waitFor(() => expect(sent(fetchMock, "POST").url).toBe("/api/v1/invoices/vat-codes/1/rates"));
    expect(sent(fetchMock, "POST").body).toEqual({ ratePercent: 27, validFrom: "2027-06-01" });
    expect(await within(dialog).findByText("Jan 1, 2027 – May 31, 2027")).toBeInTheDocument();
    expect(within(dialog).getByText("From Jun 1, 2027")).toBeInTheDocument();
    expect(within(dialog).getByText("27%")).toBeInTheDocument();

    // It is the latest and still ahead, so it may go; the one before reopens.
    await userEvent.click(within(dialog).getByRole("button", { name: "Remove this period" }));
    expect(await within(dialog).findByText("From Jan 1, 2027")).toBeInTheDocument();
    expect(within(dialog).queryByText("From Jun 1, 2027")).not.toBeInTheDocument();

    await userEvent.click(within(dialog).getByRole("button", { name: "Remove this period" }));
    await waitFor(() =>
      expect(
        fetchMock.actualCalls.some(
          ([url, init]) => path(url) === "/api/v1/invoices/vat-codes/1/rates/1002" && init?.method === "DELETE",
        ),
      ).toBe(true),
    );
    // The period before it is open again, and the future one is gone.
    expect(await within(dialog).findByText("From Jan 1, 2026")).toBeInTheDocument();
    expect(within(dialog).queryByText("From Jan 1, 2027")).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("button", { name: "Remove this period" })).not.toBeInTheDocument();
  });

  // A period already in force has priced documents, so the page offers no
  // removal for it — only for a latest period still ahead of today (D3).
  it("offers no removal for a latest period already in force", async () => {
    const [first] = vatCodes();
    const inForce: VatCode = {
      ...first,
      rates: [
        { id: 1001, ratePercent: 25, validFrom: "2026-01-01", validTo: "2026-06-30" },
        { id: 1002, ratePercent: 26, validFrom: "2026-07-01" },
      ],
    };
    stubFetch((input: RequestInfo | URL) => {
      const url = path(input);
      if (url === "/api/v1/invoices/meta") return jsonResponse(200, meta({ capabilities: manager }));
      if (url === "/api/v1/invoices/settings") return jsonResponse(200, settings());
      if (url === "/api/v1/invoices/vat-codes") return jsonResponse(200, [inForce]);
      return new Response(null, { status: 404 });
    });
    renderWithProviders(<SettingsPage />);

    const row = (await screen.findByText("Utgående mva 25 %")).closest("tr") as HTMLElement;
    await userEvent.click(within(row).getByRole("button", { name: "Rate periods" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("From Jul 1, 2026")).toBeInTheDocument();
    expect(within(dialog).queryByRole("button", { name: "Remove this period" })).not.toBeInTheDocument();
  });

  it("words a refused period removal in the reader's language", async () => {
    server({ "DELETE /api/v1/invoices/vat-codes/1/rates/1002": refusal("rate_period_in_use") });
    renderWithProviders(<SettingsPage />);

    const row = (await screen.findByText("Utgående mva 25 %")).closest("tr") as HTMLElement;
    await userEvent.click(within(row).getByRole("button", { name: "Rate periods" }));
    await userEvent.click(
      within(await screen.findByRole("dialog")).getByRole("button", { name: "Remove this period" }),
    );
    expect(
      await screen.findByText("A document is issued on or after this period's start, so the period stays."),
    ).toBeInTheDocument();
  });

  // The host guards the route with invoices:manage; the page asks meta too,
  // so a caller whose access changed under it sees why rather than a form
  // every save of which is refused.
  it("shows a caller without invoices:manage why, and no form", async () => {
    stubFetch((input: RequestInfo | URL) => {
      const url = path(input);
      if (url === "/api/v1/invoices/meta") return jsonResponse(200, meta());
      if (url === "/api/v1/invoices/settings") return jsonResponse(200, settings());
      if (url === "/api/v1/invoices/vat-codes") return jsonResponse(200, vatCodes());
      return new Response(null, { status: 404 });
    });
    renderWithProviders(<SettingsPage />);

    expect(await screen.findByText("Changing the invoice settings needs invoices:manage.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add a VAT code" })).not.toBeInTheDocument();
  });

  it("starts a new code's first period today", async () => {
    server();
    renderWithProviders(<SettingsPage />);
    await screen.findByText("Utgående mva 25 %");

    await userEvent.click(screen.getByRole("button", { name: "Add a VAT code" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("textbox", { name: "Valid from" })).toHaveValue("Sep 12, 2026");
  });

  it("says the settings changed on a stale save, keeps the edits, and reloads the latest on request", async () => {
    const state = world();
    const fetchMock = server({}, state);
    renderWithProviders(<SettingsPage />);

    const city = await screen.findByRole("textbox", { name: "City" });
    await userEvent.clear(city);
    await userEvent.type(city, "Bergen");
    // Another user saves revision 6 in the meantime.
    state.settings = settings({ revision: 6, legalName: "Kraft-Verket Holding AS" });
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("The settings changed")).toBeInTheDocument();
    expect(screen.getByText(/Someone else saved the settings/)).toBeInTheDocument();
    expect(screen.queryByText(/the supplied revision was 5/)).not.toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "City" })).toHaveValue("Bergen");
    expect(sent(fetchMock, "PUT").body.revision).toBe(5);

    await userEvent.click(screen.getByRole("button", { name: "Reload" }));
    await waitFor(() =>
      expect(screen.getByRole("textbox", { name: "Legal name" })).toHaveValue("Kraft-Verket Holding AS"),
    );
    expect(screen.getByRole("textbox", { name: "City" })).toHaveValue("Oslo");
    expect(screen.queryByText("The settings changed")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("never throws unsaved edits away on a background refetch, and says the settings changed", async () => {
    const state = world();
    server({}, state);
    const { queryClient } = renderWithProviders(<SettingsPage />);

    const city = await screen.findByRole("textbox", { name: "City" });
    await userEvent.clear(city);
    await userEvent.type(city, "Bergen");
    state.settings = settings({ revision: 6, legalName: "Kraft-Verket Holding AS" });
    await queryClient.refetchQueries({ queryKey: ["invoices", "settings"] });

    expect(await screen.findByText("The settings changed")).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "City" })).toHaveValue("Bergen");
    expect(screen.getByRole("textbox", { name: "Legal name" })).toHaveValue("Kraft-Verket AS");
  });

  // Each field has one rule, so the catalog's words say what the server
  // checked — in the reader's language, never the server's English — and each
  // lands on its own input rather than one at a time in a notification.
  it("puts every refused field's words on its own input", async () => {
    server({
      "PUT /api/v1/invoices/settings": jsonResponse(400, {
        title: "Invalid invoice settings",
        status: 400,
        errors: {
          organisationNumber: ["An organisation number is nine digits with a valid check digit"],
          bankAccount: ["A bank account number is eleven digits with a valid check digit"],
          bic: ["A BIC is 8 or 11 letters and digits"],
        },
      }),
    });
    renderWithProviders(<SettingsPage />);

    await userEvent.type(await screen.findByRole("textbox", { name: "Bank account" }), "12345678901");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(screen.getByRole("textbox", { name: "Organisation number" })).toHaveAccessibleDescription(
        "Nine digits with a valid check digit.",
      ),
    );
    expect(screen.getByRole("textbox", { name: "Bank account" })).toHaveAccessibleDescription(
      "Eleven digits with a valid check digit.",
    );
    expect(screen.getByRole("textbox", { name: "BIC" })).toHaveAccessibleDescription("8 or 11 letters and digits.");
    expect(screen.queryByText(/is nine digits with a valid check digit/)).not.toBeInTheDocument();
    expect(screen.queryByText("Could not save the settings")).not.toBeInTheDocument();

    // Changing a field takes its refusal away; the others stay until the next save.
    await userEvent.type(screen.getByRole("textbox", { name: "BIC" }), "X");
    expect(screen.getByRole("textbox", { name: "BIC" })).not.toHaveAccessibleDescription("8 or 11 letters and digits.");
    expect(screen.getByRole("textbox", { name: "Bank account" })).toHaveAccessibleDescription(
      "Eleven digits with a valid check digit.",
    );
  });

  it("says when the VAT codes cannot be loaded, rather than an empty card", async () => {
    server({ "GET /api/v1/invoices/vat-codes": jsonResponse(500, { title: "Boom", status: 500 }) });
    renderWithProviders(<SettingsPage />);

    expect(await screen.findByText("Could not load the VAT codes")).toBeInTheDocument();
    expect(await screen.findByRole("textbox", { name: "City" })).toBeInTheDocument();
    expect(screen.queryByTestId("content-skeleton")).not.toBeInTheDocument();
  });

  // Only S is taxed above 0 %: the server refuses any other category a rate
  // that is not, so the new code's rate follows the category, as a new
  // period's does.
  it("starts a new code's rate at the category's: 0 % for all but S", async () => {
    server();
    renderWithProviders(<SettingsPage />);
    await screen.findByText("Utgående mva 25 %");

    await userEvent.click(screen.getByRole("button", { name: "Add a VAT code" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("textbox", { name: "Rate %" })).toHaveValue("25");
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Category" }));
    await userEvent.click(await screen.findByRole("option", { name: "Z" }));
    expect(within(dialog).getByRole("textbox", { name: "Rate %" })).toHaveValue("0");
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Category" }));
    await userEvent.click(await screen.findByRole("option", { name: "S" }));
    expect(within(dialog).getByRole("textbox", { name: "Rate %" })).toHaveValue("25");
  });

  it("creates a code with its first period, at the category's rate", async () => {
    const fetchMock = server();
    renderWithProviders(<SettingsPage />);
    await screen.findByText("Utgående mva 25 %");

    await userEvent.click(screen.getByRole("button", { name: "Add a VAT code" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Code" }), "6");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Name" }), "Utenfor mva-loven");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "SAF-T code" }), "6");
    await userEvent.click(within(dialog).getByRole("combobox", { name: "Category" }));
    await userEvent.click(await screen.findByRole("option", { name: "O" }));
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Exemption reason" }), "Utenfor mva-loven");
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "POST").url).toBe("/api/v1/invoices/vat-codes"));
    expect(sent(fetchMock, "POST").body).toEqual({
      code: "6",
      name: "Utenfor mva-loven",
      safTCode: "6",
      ehfCategory: "O",
      exemptionReason: "Utenfor mva-loven",
      ratePercent: 0,
      validFrom: "2026-09-12",
    });
    const row = (await screen.findByText("Utenfor mva-loven")).closest("tr") as HTMLElement;
    expect(within(row).getByText("0%")).toBeInTheDocument();
  });

  it("edits a code with its revision, and a code no longer offered says so", async () => {
    const fetchMock = server();
    renderWithProviders(<SettingsPage />);
    await screen.findByText("Utgående mva 15 %");

    await userEvent.click(screen.getByRole("button", { name: "Edit VAT code 31" }));
    const dialog = await screen.findByRole("dialog");
    const name = within(dialog).getByRole("textbox", { name: "Name" });
    await userEvent.clear(name);
    await userEvent.type(name, "Utgående mva 15 % (næringsmidler)");
    await userEvent.click(within(dialog).getByRole("checkbox", { name: "Offered for new lines" }));
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT").url).toBe("/api/v1/invoices/vat-codes/2"));
    expect(sent(fetchMock, "PUT").body).toEqual({
      code: "31",
      name: "Utgående mva 15 % (næringsmidler)",
      safTCode: "31",
      ehfCategory: "S",
      active: false,
      revision: 1,
    });
    const row = (await screen.findByText("Utgående mva 15 % (næringsmidler)")).closest("tr") as HTMLElement;
    expect(within(row).getByText("Inactive")).toBeInTheDocument();
  });

  it("says a VAT code changed on a stale edit, never the server's English, and reloads it", async () => {
    const state = world();
    server({}, state);
    renderWithProviders(<SettingsPage />);
    await screen.findByText("Utgående mva 15 %");

    await userEvent.click(screen.getByRole("button", { name: "Edit VAT code 31" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Name" }), " (mat)");
    // Another user renames it in the meantime.
    state.codes = state.codes.map((c) => (c.id === 2 ? { ...c, name: "Redusert sats", revision: 2 } : c));
    await userEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(await within(dialog).findByText("The VAT code changed")).toBeInTheDocument();
    expect(screen.queryByText(/the supplied revision was 1/)).not.toBeInTheDocument();
    expect(within(dialog).getByRole("textbox", { name: "Name" })).toHaveValue("Utgående mva 15 % (mat)");

    await userEvent.click(within(dialog).getByRole("button", { name: "Reload" }));
    await waitFor(() =>
      expect(within(screen.getByRole("dialog")).getByRole("textbox", { name: "Name" })).toHaveValue("Redusert sats"),
    );
    expect(screen.queryByText("The VAT code changed")).not.toBeInTheDocument();
  });
});

describe("the Work to invoice card", () => {
  const workCard = () => screen.findByTestId("work-card");

  it("shows the code per kind of work and the timesheet's default and label, and saves the changes", async () => {
    const fetchMock = server();
    renderWithProviders(<SettingsPage />);

    const card = await workCard();
    await waitFor(() =>
      expect(within(card).getByRole("combobox", { name: "VAT code for hours" })).toHaveValue("3 — Utgående mva 25 %"),
    );
    expect(within(card).getByRole("checkbox", { name: "Attach a timesheet to new invoices" })).not.toBeChecked();
    expect(within(card).getByRole("combobox", { name: "How the timesheet names each person" })).toHaveValue(
      "Initials (KN)",
    );

    await userEvent.click(within(card).getByRole("combobox", { name: "VAT code for expenses" }));
    await userEvent.click(await screen.findByRole("option", { name: "31 — Utgående mva 15 %" }));
    await userEvent.click(within(card).getByRole("checkbox", { name: "Attach a timesheet to new invoices" }));
    await userEvent.click(within(card).getByRole("combobox", { name: "How the timesheet names each person" }));
    await userEvent.click(await screen.findByRole("option", { name: "Full name" }));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(sent(fetchMock, "PUT").url).toBe("/api/v1/invoices/settings"));
    expect(sent(fetchMock, "PUT").body).toMatchObject({
      workVatCodes: { hours: 1, expenses: 2, milestones: 1 },
      timesheetDefault: true,
      timesheetPersonLabel: "name",
      revision: 5,
    });
  });

  // The server refuses a changed code that is inactive: only the codes
  // offered for new lines are offered, with the one stored.
  it("offers the active codes, never an inactive one that is not stored", async () => {
    server();
    renderWithProviders(<SettingsPage />);
    const card = await workCard();
    await waitFor(() =>
      expect(within(card).getByRole("combobox", { name: "VAT code for hours" })).toHaveValue("3 — Utgående mva 25 %"),
    );
    await userEvent.click(within(card).getByRole("combobox", { name: "VAT code for hours" }));
    expect(await screen.findByRole("option", { name: "31 — Utgående mva 15 %" })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: /3G — Gammel sats/ })).not.toBeInTheDocument();
  });

  it("names a stored code that is no longer offered", async () => {
    const state = world();
    state.settings = settings({ workVatCodes: { hours: 9, expenses: 1, milestones: 1 } });
    server({}, state);
    renderWithProviders(<SettingsPage />);
    const card = await workCard();
    await waitFor(() =>
      expect(within(card).getByRole("combobox", { name: "VAT code for hours" })).toHaveValue(
        "3G — Gammel sats (no longer offered)",
      ),
    );
  });

  it("puts a refused code and a refused label on their own inputs, in words", async () => {
    server({
      "PUT /api/v1/invoices/settings": jsonResponse(400, {
        title: "Invalid invoice settings",
        status: 400,
        errors: {
          "workVatCodes.milestones": ["VAT code 9 is not active"],
          timesheetPersonLabel: ["timesheetPersonLabel must be initials, number or name"],
        },
      }),
    });
    renderWithProviders(<SettingsPage />);

    const card = await workCard();
    await userEvent.click(within(card).getByRole("checkbox", { name: "Attach a timesheet to new invoices" }));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(within(card).getByRole("combobox", { name: "VAT code for milestones" })).toHaveAccessibleDescription(
        "Choose a VAT code that exists and is offered for new lines.",
      ),
    );
    expect(
      within(card).getByRole("combobox", { name: "How the timesheet names each person" }),
    ).toHaveAccessibleDescription(expect.stringContaining("Initials, number or name."));
    expect(screen.queryByText("Could not save the settings")).not.toBeInTheDocument();
    expect(screen.queryByText(/is not active/)).not.toBeInTheDocument();

    // Changing one code takes its own refusal away and leaves the other's.
    await userEvent.click(within(card).getByRole("combobox", { name: "VAT code for milestones" }));
    await userEvent.click(await screen.findByRole("option", { name: "5 — Fritatt innenlands 0 %" }));
    expect(within(card).getByRole("combobox", { name: "VAT code for milestones" })).not.toHaveAccessibleDescription(
      "Choose a VAT code that exists and is offered for new lines.",
    );
    expect(
      within(card).getByRole("combobox", { name: "How the timesheet names each person" }),
    ).toHaveAccessibleDescription(expect.stringContaining("Initials, number or name."));
  });
});
