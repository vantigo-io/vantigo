import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { commonAnswers, fakeServer, jsonResponse } from "../test/api";
import { renderRoute } from "../test/render";

const quoteRow = (overrides: Record<string, unknown> = {}) => ({
  id: 1001,
  number: 1042,
  status: "sent",
  revision: 2,
  customerId: 42,
  customerName: "Acme AS",
  currency: "NOK",
  net: 1500,
  gross: 1875,
  validUntil: "2026-12-01",
  salesperson: { displayName: "Kari Selger" },
  updatedAt: "2026-11-01T08:00:00Z",
  ...overrides,
});

const orderRow = (overrides: Record<string, unknown> = {}) => ({
  id: 2001,
  number: 5001,
  status: "confirmed",
  onHold: false,
  locked: false,
  deliveryStatus: "partial",
  invoiceStatus: "none",
  done: false,
  customerId: 42,
  customerName: "Acme AS",
  currency: "NOK",
  net: 1500,
  gross: 1875,
  updatedAt: "2026-11-01T08:00:00Z",
  ...overrides,
});

describe("the quotes list", () => {
  it("lists the quotes with their state, revision and amounts, each linking to its editor", async () => {
    fakeServer((call) =>
      call.url.startsWith("/api/v1/orders/quotes")
        ? jsonResponse(200, {
            items: [quoteRow(), quoteRow({ id: 1002, number: 1043, status: "draft", revision: 1 })],
            total: 2,
          })
        : commonAnswers(call),
    );
    renderRoute("/orders/quotes");

    const table = await screen.findByTestId("quote-table");
    const rows = within(table).getAllByRole("row");
    expect(within(rows[1]).getByRole("link", { name: "1042" })).toHaveAttribute("href", "/orders/quotes/1001");
    expect(within(rows[1]).getByText("rev. 2")).toBeInTheDocument();
    expect(within(rows[1]).getByText("Sent")).toBeInTheDocument();
    expect(within(rows[1]).getByText("NOK 1,875.00")).toBeInTheDocument();
    expect(screen.getByText("2 quotes")).toBeInTheDocument();
  });

  it("asks the server for the chosen states, the caller's own and a search", async () => {
    const fake = fakeServer((call) =>
      call.url.startsWith("/api/v1/orders/quotes")
        ? jsonResponse(200, { items: [quoteRow()], total: 1 })
        : commonAnswers(call),
    );
    renderRoute("/orders/quotes");
    await screen.findByTestId("quote-table");

    await userEvent.click(screen.getByRole("checkbox", { name: "Sent" }));
    await userEvent.click(screen.getByRole("checkbox", { name: "Expired" }));
    await waitFor(() => expect(fake.calls.some((c) => c.url.includes("status=sent&status=expired"))).toBe(true));
    await userEvent.click(screen.getByRole("switch", { name: "Only mine" }));
    await waitFor(() =>
      expect(fake.calls.some((c) => c.url.includes("salespersonId=00000000-0000-4000-8000-000000000001"))).toBe(true),
    );
    await userEvent.type(screen.getByRole("textbox", { name: "Search" }), "Acme");
    await waitFor(() => expect(fake.calls.some((c) => c.url.includes("q=Acme"))).toBe(true));
  });

  it("offers New quote only with orders:quotes-manage", async () => {
    fakeServer((call) =>
      call.url.startsWith("/api/v1/orders/quotes") ? jsonResponse(200, { items: [], total: 0 }) : commonAnswers(call),
    );
    renderRoute("/orders/quotes", { canManageQuotes: false });
    expect(await screen.findByText("No quotes")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "New quote" })).not.toBeInTheDocument();
  });

  it("pages through the quotes", async () => {
    const fake = fakeServer((call) =>
      call.url.startsWith("/api/v1/orders/quotes")
        ? jsonResponse(200, { items: [quoteRow()], total: 60 })
        : commonAnswers(call),
    );
    renderRoute("/orders/quotes");
    await screen.findByTestId("quote-table");
    await userEvent.click(screen.getByRole("button", { name: "2" }));
    await waitFor(() => expect(fake.calls.some((c) => c.url.includes("page=2"))).toBe(true));
  });
});

describe("the orders list", () => {
  it("shows the open orders by default, with their delivery and invoice status", async () => {
    const fake = fakeServer((call) =>
      call.url.startsWith("/api/v1/orders/orders")
        ? jsonResponse(200, {
            items: [
              orderRow(),
              orderRow({ id: 2002, number: 5002, done: true, deliveryStatus: "delivered", invoiceStatus: "invoiced" }),
            ],
            total: 2,
          })
        : commonAnswers(call),
    );
    renderRoute("/orders/orders");

    const table = await screen.findByTestId("order-table");
    expect(fake.calls.find((c) => c.url.startsWith("/api/v1/orders/orders"))?.url).toContain("open=true");
    const rows = within(table).getAllByRole("row");
    expect(within(rows[1]).getByText("Partly delivered")).toBeInTheDocument();
    expect(within(rows[1]).getByText("Not invoiced")).toBeInTheDocument();
    expect(within(rows[2]).getByText("Done")).toBeInTheDocument();
  });

  it("shows all orders when asked", async () => {
    const fake = fakeServer((call) =>
      call.url.startsWith("/api/v1/orders/orders")
        ? jsonResponse(200, { items: [orderRow()], total: 1 })
        : commonAnswers(call),
    );
    renderRoute("/orders/orders");
    await screen.findByTestId("order-table");

    await userEvent.click(screen.getByRole("radio", { name: "All" }));
    await waitFor(() =>
      expect(fake.calls.filter((c) => c.url.startsWith("/api/v1/orders/orders")).at(-1)?.url).not.toContain("open="),
    );
  });

  it("offers New order with orders:sales-manage", async () => {
    fakeServer((call) =>
      call.url.startsWith("/api/v1/orders/orders") ? jsonResponse(200, { items: [], total: 0 }) : commonAnswers(call),
    );
    renderRoute("/orders/orders");
    expect(await screen.findByRole("link", { name: "New order" })).toHaveAttribute("href", "/orders/orders/new");
  });
});
