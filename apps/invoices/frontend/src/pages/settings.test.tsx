import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { VatCode } from "../api/vat-codes";
import { jsonResponse, sent } from "../test/api";
import { stubFetch } from "../test/fetch";
import { meta, settings, vatCodes } from "../test/fixtures";
import { renderWithProviders } from "../test/render";
import { SettingsPage } from "./settings";

const path = (input: RequestInfo | URL) => String(input);

/**
 * The fetch fake, a small model of the rules the page relies on: the codes are
 * state, and removing the latest period reopens the one before it, as the
 * server does (D3). `refuse` answers a "METHOD url" with a refusal instead.
 */
/** The caller the page is for: meta's `canManage` is what shows it (D12). */
const manager = { canCreate: true, canIssue: true, canManage: true };

const server = (refuse: Record<string, Response> = {}) => {
  let codes: VatCode[] = vatCodes();
  return stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = path(input);
    const method = init?.method ?? "GET";
    const refusal = refuse[`${method} ${url}`];
    if (refusal) return refusal.clone();
    if (url === "/api/v1/invoices/meta") return jsonResponse(200, meta({ capabilities: manager }));
    if (url === "/api/v1/invoices/settings")
      return jsonResponse(200, method === "PUT" ? settings({ revision: 6 }) : settings());
    if (url === "/api/v1/invoices/vat-codes") return jsonResponse(200, codes);
    const period = /^\/api\/v1\/invoices\/vat-codes\/(\d+)\/rates\/(\d+)$/.exec(url);
    if (period && method === "DELETE") {
      const [id, rateId] = [Number(period[1]), Number(period[2])];
      codes = codes.map((c) => {
        if (c.id !== id) return c;
        const rates = c.rates.filter((r) => r.id !== rateId);
        const reopened = { ...rates[rates.length - 1], validTo: undefined };
        return { ...c, rates: [...rates.slice(0, -1), reopened], revision: c.revision + 1 };
      });
      return jsonResponse(
        200,
        codes.find((c) => c.id === id),
      );
    }
    return new Response(null, { status: 404 });
  });
};

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
    expect(within(row).getByText("25 %")).toBeInTheDocument();
    await userEvent.click(within(row).getByRole("button", { name: "Rate periods" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("Jan 1, 2026 – Dec 31, 2026")).toBeInTheDocument();
    expect(within(dialog).getByText("From Jan 1, 2027")).toBeInTheDocument();

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
});
