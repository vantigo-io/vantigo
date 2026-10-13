import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { VippsPaymentsDay } from "../api/attempts";
import type { InvoicesMeta } from "../api/meta";
import { jsonResponse, path, refusal } from "../test/api";
import { requestTo } from "../test/document-server";
import { stubFetch } from "../test/fetch";
import { meta, vippsDayReconciliation, vippsPayment, vippsPaymentsDay } from "../test/fixtures";
import { renderRoute } from "../test/route-tree";

// jsdom has no object URLs: the CSV's save makes and revokes one.
const { createObjectURL, revokeObjectURL } = URL;

beforeEach(() => {
  URL.createObjectURL = vi.fn(() => "blob:csv");
  URL.revokeObjectURL = vi.fn();
});

afterEach(() => {
  URL.createObjectURL = createObjectURL;
  URL.revokeObjectURL = revokeObjectURL;
});

/** Yesterday's payments as the server answers them: one on site, one through a pay link refunded in part since. */
const yesterday = () =>
  vippsPaymentsDay({
    date: "2026-09-11",
    payments: [
      vippsPayment({ paidOn: "2026-09-11", capturedAt: "2026-09-11T09:15:00Z" }),
      vippsPayment({
        attemptId: 1002,
        reference: "k3x9q2-2-1002",
        invoiceId: 2,
        invoiceNumber: 2,
        buyerName: "Kari Nordmann",
        flow: "pay_link",
        amount: 500,
        refundedAmount: 100,
        paidOn: "2026-09-11",
        capturedAt: "2026-09-11T19:40:00Z",
      }),
    ],
    count: 2,
    total: 1750,
    onSiteTotal: 1250,
    payLinkTotal: 500,
  });

/**
 * The day list's fake: meta for `capabilities`, the day's list per date —
 * today's by the fixture, yesterday's above, and yesterday's reconciliation
 * once recorded — the CSV, and the reconciliation by `reconcile`.
 */
const server = ({
  capabilities = {},
  reconcile,
}: {
  capabilities?: Partial<InvoicesMeta["capabilities"]>;
  reconcile?: () => Response;
} = {}) => {
  let reconciled = false;
  return stubFetch((input: RequestInfo | URL, init?: RequestInit) => {
    const url = path(input);
    const method = init?.method ?? "GET";
    if (url === "/api/v1/invoices/meta") {
      return jsonResponse(200, meta({ capabilities: { ...meta().capabilities, ...capabilities } }));
    }
    if (url === "/api/v1/invoices/vipps-payments?date=2026-09-12") return jsonResponse(200, vippsPaymentsDay());
    if (url === "/api/v1/invoices/vipps-payments?date=2026-09-11") {
      const day: VippsPaymentsDay = reconciled
        ? { ...yesterday(), reconciliation: vippsDayReconciliation({ payments: 2, total: 1750, note: "Stemmer" }) }
        : yesterday();
      return jsonResponse(200, day);
    }
    if (url === "/api/v1/invoices/vipps-payments.csv?date=2026-09-11") {
      return new Response("﻿Reference;Amount\r\n", {
        status: 200,
        headers: {
          "Content-Type": "text/csv; charset=utf-8",
          "Content-Disposition": 'attachment; filename="vipps-payments-2026-09-11.csv"',
        },
      });
    }
    if (url === "/api/v1/invoices/vipps-payments/reconciliations" && method === "POST") {
      if (reconcile) return reconcile();
      reconciled = true;
      return jsonResponse(201, vippsDayReconciliation({ payments: 2, total: 1750, note: "Stemmer" }));
    }
    return new Response(null, { status: 404 });
  });
};

const toYesterday = async () => {
  await userEvent.click(await screen.findByRole("button", { name: "Previous day" }));
  return screen.findByText("Kari Nordmann");
};

describe("VippsPaymentsPage_ListCsvReconcile", () => {
  it("lists today's Vipps payments with the totals per way paid, under the Payments area's Vipps tab", async () => {
    server();
    renderRoute("/invoices/payments/vipps");

    const table = await screen.findByTestId("vipps-payments");
    const row = within(table).getByText("Acme AS").closest("tr") as HTMLElement;
    expect(within(row).getByRole("link", { name: "Invoice 1" })).toHaveAttribute("href", "/invoices/1");
    expect(within(row).getByText("On site")).toBeInTheDocument();
    expect(within(row).getByText(/NOK\s?1,250\.00/)).toBeInTheDocument();
    const totals = screen.getByTestId("vipps-totals");
    expect(totals).toHaveTextContent("Payments: 1");
    expect(totals).toHaveTextContent(/Total NOK\s?1,250\.00/);
    expect(totals).toHaveTextContent(/On site NOK\s?1,250\.00/);
    expect(totals).toHaveTextContent(/Pay links NOK\s?0\.00/);
    expect(screen.getByRole("tab", { name: "Vipps" })).toHaveAttribute("aria-selected", "true");
    // Today is not over: it is not reconciled yet.
    expect(screen.queryByRole("button", { name: "Mark the day reconciled" })).not.toBeInTheDocument();
    expect(screen.getByText("The day is not over yet, so it cannot be reconciled.")).toBeInTheDocument();
  });

  it("moves to another day and shows a refund beside its payment", async () => {
    server();
    renderRoute("/invoices/payments/vipps");

    const row = (await toYesterday()).closest("tr") as HTMLElement;
    expect(within(row).getByText("Pay link")).toBeInTheDocument();
    expect(within(row).getByText(/NOK\s?100\.00/)).toBeInTheDocument();
    expect(screen.getByTestId("vipps-totals")).toHaveTextContent(/Total NOK\s?1,750\.00/);
  });

  it("downloads the day as CSV", async () => {
    const clicked: HTMLAnchorElement[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      clicked.push(this);
    });
    const fetchMock = server();
    renderRoute("/invoices/payments/vipps");

    await toYesterday();
    await userEvent.click(screen.getByRole("button", { name: "Download CSV" }));
    await waitFor(() => expect(clicked).toHaveLength(1));
    expect(
      fetchMock.actualCalls.some(([url]) => path(url) === "/api/v1/invoices/vipps-payments.csv?date=2026-09-11"),
    ).toBe(true);
    expect(clicked[0].download).toBe("vipps-payments-2026-09-11.csv");
  });

  it("marks a day that is over reconciled, with a note, and shows the record", async () => {
    const fetchMock = server();
    renderRoute("/invoices/payments/vipps");

    await toYesterday();
    await userEvent.click(screen.getByRole("button", { name: "Mark the day reconciled" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("textbox", { name: "Note" }), "Stemmer");
    await userEvent.click(within(dialog).getByRole("button", { name: "Mark reconciled" }));

    expect(await screen.findByText("The day is marked reconciled")).toBeInTheDocument();
    expect(requestTo(fetchMock, "POST", "/api/v1/invoices/vipps-payments/reconciliations")).toEqual({
      day: "2026-09-11",
      note: "Stemmer",
    });
    expect(await screen.findByTestId("vipps-reconciled")).toHaveTextContent(/^Reconciled on Sep 12, 2026/);
    expect(screen.getByTestId("vipps-reconciled")).toHaveTextContent("Note: Stemmer");
    expect(screen.queryByRole("button", { name: "Mark the day reconciled" })).not.toBeInTheDocument();
  });

  it("says day_already_reconciled in words", async () => {
    server({ reconcile: () => refusal(409, "day_already_reconciled") });
    renderRoute("/invoices/payments/vipps");

    await toYesterday();
    await userEvent.click(screen.getByRole("button", { name: "Mark the day reconciled" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Mark reconciled" }));
    expect(await screen.findByText("This day was reconciled already, and stays so.")).toBeInTheDocument();
  });

  it("offers no reconciliation to a reader with invoices:access alone", async () => {
    server({ capabilities: { canRegisterPayments: false, canImportBankFiles: false, canRunReminders: false } });
    renderRoute("/invoices/payments/vipps");

    await toYesterday();
    expect(screen.getByRole("button", { name: "Download CSV" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Mark the day reconciled" })).not.toBeInTheDocument();
  });
});
