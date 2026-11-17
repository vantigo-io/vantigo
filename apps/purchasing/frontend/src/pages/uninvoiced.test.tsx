import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { PurchasingInvoiceUninvoiced } from "../api/purchasing";
import { fakeServer, jsonResponse } from "../test/api";
import { commonAnswers } from "../test/fixtures";
import { renderRoute } from "../test/render";

const list: PurchasingInvoiceUninvoiced = {
  asOf: "2026-11-17",
  rows: [
    {
      supplierId: 40,
      supplierName: "Skruer AS",
      orderId: 501,
      orderNumber: 3001,
      orderLineId: 11,
      position: 1,
      name: "Skrue 10 mm",
      unit: "stk",
      received: "1000",
      invoiced: "400",
      quantity: "600",
      unitNet: "0.4550",
      amount: "273.00",
      currency: "NOK",
    },
  ],
  totals: [{ currency: "NOK", amount: "273.00" }],
};

describe("received, not invoiced", () => {
  it("lists the lines as of a day with their totals, and downloads the same list as CSV", async () => {
    const server = fakeServer((call) => {
      if (call.method === "GET" && call.url.startsWith("/api/v1/purchasing/received-not-invoiced/csv"))
        return new Response("supplier_id;supplier\r\n", {
          status: 200,
          headers: { "Content-Type": "text/csv", "Content-Disposition": 'attachment; filename="r.csv"' },
        });
      if (call.method === "GET" && call.url.startsWith("/api/v1/purchasing/received-not-invoiced"))
        return jsonResponse(200, list);
      return commonAnswers(call);
    });
    URL.createObjectURL = vi.fn(() => "blob:csv");
    URL.revokeObjectURL = vi.fn();
    renderRoute("/purchasing/received-not-invoiced");
    const rows = await screen.findByTestId("uninvoiced-rows");
    expect(rows).toHaveTextContent("Skruer AS");
    expect(rows).toHaveTextContent("3001");
    expect(rows).toHaveTextContent("1,000 stk");
    expect(screen.getByTestId("uninvoiced-totals")).toHaveTextContent("Total NOK");
    expect(server.sent("GET", /received-not-invoiced\?/)?.url).toMatch(/asOf=\d{4}-\d{2}-\d{2}/);
    await userEvent.click(screen.getByRole("button", { name: "Download CSV" }));
    await waitFor(() => expect(server.count("GET", /received-not-invoiced\/csv\?asOf=/)).toBe(1));
  });

  it("says when everything received is invoiced", async () => {
    fakeServer((call) => {
      if (call.method === "GET" && call.url.startsWith("/api/v1/purchasing/received-not-invoiced"))
        return jsonResponse(200, { ...list, rows: [], totals: [] });
      return commonAnswers(call);
    });
    renderRoute("/purchasing/received-not-invoiced");
    expect(await screen.findByText(/Everything received by .* is invoiced\./)).toBeInTheDocument();
  });
});
