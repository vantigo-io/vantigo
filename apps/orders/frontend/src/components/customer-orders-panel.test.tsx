import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { commonAnswers, fakeServer, jsonResponse } from "../test/api";
import { renderPanel } from "../test/render";

const summary = {
  customerId: 42,
  openQuotes: [
    { currency: "NOK", count: 2, amount: 12500 },
    { currency: "EUR", count: 1, amount: 900 },
  ],
  orderedNotDelivered: [{ currency: "NOK", count: 1, amount: 4000 }],
  deliveredNotInvoiced: [],
};

const server = () =>
  fakeServer((call) => {
    if (call.url === "/api/v1/orders/customers/42/summary") return jsonResponse(200, summary);
    if (call.url.startsWith("/api/v1/orders/quotes?"))
      return jsonResponse(200, {
        items: [
          {
            id: 1001,
            number: 1042,
            status: "sent",
            revision: 1,
            customerId: 42,
            customerName: "Acme AS",
            currency: "NOK",
            net: 10000,
            gross: 12500,
            validUntil: "2026-12-01",
            updatedAt: "2026-11-01T08:00:00Z",
          },
        ],
        total: 1,
      });
    if (call.url.startsWith("/api/v1/orders/orders?"))
      return jsonResponse(200, {
        items: [
          {
            id: 2001,
            number: 5001,
            status: "confirmed",
            onHold: false,
            locked: false,
            deliveryStatus: "none",
            invoiceStatus: "none",
            done: false,
            customerId: 42,
            customerName: "Acme AS",
            currency: "NOK",
            net: 3200,
            gross: 4000,
            updatedAt: "2026-11-01T08:00:00Z",
          },
        ],
        total: 1,
      });
    return commonAnswers(call);
  });

describe("the customer page's Orders tab", () => {
  it("shows the summary per currency, the open quotes and the open orders of the customer", async () => {
    const fake = server();
    renderPanel({ customerId: 42, canCreateQuote: true, canCreateOrder: true });

    const tiles = await screen.findAllByTestId("summary-tile");
    expect(tiles[0]).toHaveTextContent("NOK 12,500.00");
    expect(tiles[0]).toHaveTextContent("€900.00");
    expect(tiles[0]).toHaveTextContent("2 documents");
    expect(tiles[1]).toHaveTextContent("NOK 4,000.00");
    expect(tiles[2]).toHaveTextContent("—");
    expect(within(await screen.findByTestId("quote-table")).getByRole("link", { name: "1042" })).toHaveAttribute(
      "href",
      "/orders/quotes/1001",
    );
    expect(within(await screen.findByTestId("order-table")).getByRole("link", { name: "5001" })).toHaveAttribute(
      "href",
      "/orders/orders/2001",
    );
    expect(
      fake.calls.some((c) => c.url === "/api/v1/orders/quotes?customerId=42&status=draft&status=sent&pageSize=10"),
    ).toBe(true);
    expect(fake.calls.some((c) => c.url === "/api/v1/orders/orders?customerId=42&open=true&pageSize=10")).toBe(true);
  });

  it("shows every order when asked", async () => {
    const fake = server();
    renderPanel({ customerId: 42, canCreateQuote: false, canCreateOrder: false });
    await screen.findByTestId("order-table");

    await userEvent.click(screen.getByRole("radio", { name: "All" }));
    await waitFor(() =>
      expect(fake.calls.some((c) => c.url === "/api/v1/orders/orders?customerId=42&pageSize=10")).toBe(true),
    );
  });

  it("opens the editors with the customer filled in", async () => {
    server();
    renderPanel({ customerId: 42, canCreateQuote: true, canCreateOrder: true });

    expect(await screen.findByRole("link", { name: "New quote" })).toHaveAttribute(
      "href",
      "/orders/quotes/new?customerId=42",
    );
    expect(screen.getByRole("link", { name: "New order" })).toHaveAttribute("href", "/orders/orders/new?customerId=42");
  });

  it("offers each new document only as the host allows it", async () => {
    server();
    renderPanel({ customerId: 42, canCreateQuote: false, canCreateOrder: true });

    await screen.findAllByTestId("summary-tile");
    expect(screen.queryByRole("link", { name: "New quote" })).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "New order" })).toBeInTheDocument();
  });
});
