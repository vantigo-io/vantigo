import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Warning } from "../api/types";
import { commonAnswers, fakeServer, jsonResponse, refusal } from "../test/api";
import { line, meta, order, quote, settings } from "../test/fixtures";
import { renderRoute } from "../test/render";

// The charts draw nothing in jsdom; the tables beside them are what is read.
vi.mock("@mantine/charts", () => ({ BarChart: () => null }));

const warning = (overrides: Partial<Warning> = {}): Warning => ({
  scope: "customer",
  id: 42,
  name: "Acme AS",
  level: "info",
  text: "Ring før levering",
  updatedAt: "2026-11-01T08:00:00Z",
  ...overrides,
});

const quoteUrl = "/api/v1/orders/quotes/1001";
const orderUrl = "/api/v1/orders/orders/2001";

describe("the sales warnings (O33)", () => {
  it("shows an info warning in yellow and a block in red on a quote", async () => {
    fakeServer((call) => {
      if (call.method === "GET" && call.url === quoteUrl)
        return jsonResponse(
          200,
          quote({
            warnings: [
              warning(),
              warning({ scope: "product", id: 30, name: "Kabel", level: "block", text: "Tilbakekalt" }),
            ],
          }),
        );
      return commonAnswers(call, settings(), meta());
    });
    renderRoute("/orders/quotes/1001", { canManageSettings: false });

    const banners = await screen.findByTestId("warning-banners");
    expect(within(banners).getByText("Ring før levering").closest("[data-level]")).toHaveAttribute(
      "data-level",
      "info",
    );
    expect(within(banners).getByText("Tilbakekalt").closest("[data-level]")).toHaveAttribute("data-level", "block");
    expect(within(banners).getByText(/Someone with Manage Orders settings can override it/)).toBeInTheDocument();
    expect(within(banners).queryByRole("button", { name: "Override the warning" })).not.toBeInTheDocument();
  });

  it("lets a settings holder override a block with a reason, which the mark as sent carries", async () => {
    const fake = fakeServer((call) => {
      if (call.method === "GET" && call.url === quoteUrl)
        return jsonResponse(200, quote({ warnings: [warning({ level: "block", text: "Betaler ikke" })] }));
      if (call.method === "POST" && call.url === `${quoteUrl}/mark-sent`)
        return jsonResponse(200, quote({ status: "sent", updatedAt: "2026-11-02T08:00:00Z" }));
      return commonAnswers(call, settings(), meta());
    });
    renderRoute("/orders/quotes/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Override the warning" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByLabelText(/Reason/), "Forhåndsbetalt");
    await userEvent.click(within(dialog).getByRole("button", { name: "Override the warning" }));
    expect(await screen.findByText("Warning overridden")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Mark as sent" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Mark as sent" }));
    await waitFor(() => expect(fake.sent("POST", `${quoteUrl}/mark-sent`)).toBeDefined());
    expect(fake.sent("POST", `${quoteUrl}/mark-sent`)?.body).toMatchObject({
      overrideWarnings: true,
      overrideReason: "Forhåndsbetalt",
    });
  });

  it("clears an override once the write that carried it is done", async () => {
    const fake = fakeServer((call) => {
      if (call.method === "GET" && call.url === quoteUrl)
        return jsonResponse(200, quote({ warnings: [warning({ level: "block", text: "Betaler ikke" })] }));
      // The same quote back, so the editor is not started afresh by a new version.
      if (call.method === "POST" && call.url === `${quoteUrl}/mark-sent`)
        return jsonResponse(200, quote({ warnings: [warning({ level: "block", text: "Betaler ikke" })] }));
      return commonAnswers(call, settings(), meta());
    });
    renderRoute("/orders/quotes/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Override the warning" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByLabelText(/Reason/), "Forhåndsbetalt");
    await userEvent.click(within(dialog).getByRole("button", { name: "Override the warning" }));
    expect(await screen.findByText("Warning overridden")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Mark as sent" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Mark as sent" }));
    await waitFor(() => expect(fake.sent("POST", `${quoteUrl}/mark-sent`)).toBeDefined());
    await waitFor(() => expect(screen.queryByText("Warning overridden")).not.toBeInTheDocument());
    expect(screen.getByRole("button", { name: "Override the warning" })).toBeInTheDocument();
  });

  it("says a refused confirm in the reader's words", async () => {
    fakeServer((call) => {
      if (call.method === "GET" && call.url === orderUrl)
        return jsonResponse(200, order({ warnings: [warning({ level: "block", text: "Betaler ikke" })] }));
      if (call.method === "POST" && call.url === `${orderUrl}/confirm`) return refusal(409, "blocked_by_warning");
      return commonAnswers(call, settings(), meta());
    });
    renderRoute("/orders/orders/2001", { canManageSettings: false });

    await userEvent.click(await screen.findByRole("button", { name: "Confirm" }));
    expect(await screen.findByText(/A warning blocks this/)).toBeInTheDocument();
  });

  it("lists, sets and removes the warnings in the settings", async () => {
    const fake = fakeServer((call) => {
      if (call.method === "GET" && call.url === "/api/v1/orders/warnings")
        return jsonResponse(200, { items: [warning()] });
      if (call.method === "PUT" && call.url === "/api/v1/orders/warnings/customers/42")
        return jsonResponse(200, warning({ level: "block" }));
      if (call.method === "DELETE") return new Response(null, { status: 204 });
      return commonAnswers(call, settings(), meta());
    });
    renderRoute("/orders/settings");

    const table = await screen.findByTestId("warnings-table");
    expect(within(table).getByText("Acme AS")).toBeInTheDocument();
    await userEvent.click(within(table).getByRole("button", { name: "Edit" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(within(dialog).getByText("Block"));
    await userEvent.click(within(dialog).getByRole("button", { name: "Save the warning" }));
    await waitFor(() => expect(fake.sent("PUT", "/api/v1/orders/warnings/customers/42")).toBeDefined());
    expect(fake.sent("PUT", "/api/v1/orders/warnings/customers/42")?.body).toEqual({
      level: "block",
      text: "Ring før levering",
    });
  });

  it("marks a deleted product's warning, which is only removed", async () => {
    const fake = fakeServer((call) => {
      if (call.method === "GET" && call.url === "/api/v1/orders/warnings")
        return jsonResponse(200, {
          items: [
            warning({ scope: "product", id: 30, name: "Kabel", level: "block", text: "Tilbakekalt", orphaned: true }),
          ],
        });
      if (call.method === "DELETE") return new Response(null, { status: 204 });
      return commonAnswers(call, settings(), meta());
    });
    renderRoute("/orders/settings");

    const table = await screen.findByTestId("warnings-table");
    expect(within(table).getByText("Product deleted")).toBeInTheDocument();
    expect(within(table).queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
    await userEvent.click(within(table).getByRole("button", { name: "Remove" }));
    await waitFor(() => expect(fake.sent("DELETE", "/api/v1/orders/warnings/products/30")).toBeDefined());
  });
});

describe("packagings on lines (O15)", () => {
  it("shows a line entered in a packaging as its count, packaging and size", async () => {
    fakeServer((call) => {
      if (call.method === "GET" && call.url === orderUrl)
        return jsonResponse(
          200,
          order({
            status: "confirmed",
            locked: true,
            lines: [line({ quantity: 45, packagingId: 7, packagingQuantity: 3, packagingName: "Eske" })],
          }),
        );
      return commonAnswers(call, settings(), meta());
    });
    renderRoute("/orders/orders/2001");
    expect(await screen.findByTestId("line-packaging-1")).toHaveTextContent("3 × Eske of 15");
  });

  it("sends a packaging's count rather than the quantity", async () => {
    const fake = fakeServer((call) => {
      if (call.method === "GET" && call.url === quoteUrl)
        return jsonResponse(
          200,
          quote({ lines: [line({ quantity: 45, packagingId: 7, packagingQuantity: 3, packagingName: "Eske" })] }),
        );
      if (call.method === "PUT") return jsonResponse(200, quote({ updatedAt: "2026-11-02T08:00:00Z" }));
      return commonAnswers(call, settings(), meta());
    });
    renderRoute("/orders/quotes/1001");

    const count = await screen.findByLabelText("Number of packagings on line 1");
    await userEvent.clear(count);
    await userEvent.type(count, "4");
    expect(screen.getByTestId("line-packaging-1")).toHaveTextContent("= 60 stk");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(fake.sent("PUT", quoteUrl)).toBeDefined());
    const body = fake.sent("PUT", quoteUrl)?.body as { lines: Record<string, unknown>[] };
    expect(body.lines[0]).toMatchObject({ packagingId: 7, packagingQuantity: 4 });
    expect(body.lines[0]).not.toHaveProperty("quantity");
  });
});

describe("Add from grid (O34)", () => {
  it("adds a line per quantity typed in the grid", async () => {
    fakeServer((call) => {
      if (call.method === "GET" && call.url === quoteUrl) return jsonResponse(200, quote({ lines: [] }));
      if (call.method === "GET" && call.url.startsWith("/api/v1/orders/products/30/grid"))
        return jsonResponse(200, {
          productId: 30,
          productName: "Skjorte",
          currency: "NOK",
          mode: "grid",
          options: [
            { name: "Colour", values: ["Red", "Blue"] },
            { name: "Size", values: ["S", "M"] },
          ],
          cells: [
            { variantId: 901, sku: "SK-S-R", unit: "stk", values: { Colour: "Red", Size: "S" }, price: 10 },
            { variantId: 902, sku: "SK-M-R", unit: "stk", values: { Colour: "Red", Size: "M" }, price: 10 },
            { variantId: 903, sku: "SK-S-B", unit: "stk", values: { Colour: "Blue", Size: "S" }, price: 10 },
          ],
          variants: [],
        });
      return commonAnswers(call, settings(), meta());
    });
    renderRoute("/orders/quotes/1001");

    await userEvent.click(await screen.findByRole("button", { name: "Add from grid" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.type(within(dialog).getByRole("combobox", { name: "Product (search a variant)" }), "kab");
    await userEvent.click(await screen.findByRole("option", { name: /KAB-20/ }));
    const grid = await within(dialog).findByTestId("variant-grid");
    expect(within(grid).queryByLabelText("Quantity of Blue / M")).not.toBeInTheDocument();
    await userEvent.type(within(grid).getByLabelText("Quantity of Red / S"), "2");
    await userEvent.type(within(grid).getByLabelText("Quantity of Blue / S"), "5");
    await userEvent.click(within(dialog).getByRole("button", { name: "Add the lines" }));

    await waitFor(() => expect(screen.getAllByTestId("line-row")).toHaveLength(2));
    expect(screen.getByLabelText("Quantity of line 1")).toHaveValue("2");
    expect(screen.getByLabelText("Quantity of line 2")).toHaveValue("5");
  });
});

describe("the analysis (O35)", () => {
  const analysis = () =>
    fakeServer((call) => {
      if (call.url.startsWith("/api/v1/orders/analysis/hit-rate"))
        return jsonResponse(200, {
          from: "2025-11-05",
          to: "2026-11-05",
          rows: [
            {
              salesperson: { displayName: "Kari Selger" },
              currency: "NOK",
              sent: { count: 4, amount: 4000 },
              accepted: { count: 1, amount: 1000 },
              declined: { count: 1, amount: 1000 },
              expired: { count: 0, amount: 0 },
              cancelled: { count: 0, amount: 0 },
              open: { count: 2, amount: 2000 },
            },
          ],
          totals: [
            {
              currency: "NOK",
              sent: { count: 4, amount: 4000 },
              accepted: { count: 1, amount: 1000 },
              declined: { count: 1, amount: 1000 },
              expired: { count: 0, amount: 0 },
              cancelled: { count: 0, amount: 0 },
              open: { count: 2, amount: 2000 },
            },
          ],
        });
      if (call.url.startsWith("/api/v1/orders/analysis/intake"))
        return jsonResponse(200, {
          from: "2025-11-05",
          to: "2026-11-05",
          groupBy: "product",
          rows: [{ key: "", label: "", currency: "NOK", count: 1, amount: 50 }],
          totals: [{ currency: "NOK", count: 1, amount: 50 }],
        });
      if (call.url.startsWith("/api/v1/orders/analysis/backlog"))
        return jsonResponse(200, {
          asOf: "2026-11-05",
          orderedNotDelivered: [{ currency: "NOK", count: 2, amount: 700 }],
          deliveredNotInvoiced: [
            { currency: "NOK", count: 1, amount: 400, days0To7: 0, days8To30: 0, over30Days: 400 },
          ],
        });
      return commonAnswers(call, settings(), meta());
    });

  it("shows the hit rate per salesperson with its rate", async () => {
    analysis();
    renderRoute("/orders/analysis");
    const table = await screen.findByTestId("hit-rate-table");
    expect(within(table).getByText("Kari Selger")).toBeInTheDocument();
    expect(within(table).getAllByText("25%")).toHaveLength(2);
  });

  it("groups the intake and names the free-text lines", async () => {
    const fake = analysis();
    renderRoute("/orders/analysis");
    await userEvent.click(await screen.findByRole("tab", { name: "Order intake" }));
    await userEvent.click(await screen.findByRole("combobox", { name: "Group by" }));
    await userEvent.click(await screen.findByRole("option", { name: "Product" }));
    await waitFor(() => expect(fake.sent("GET", /groupBy=product/)).toBeDefined());
    expect(await within(await screen.findByTestId("intake-table")).findByText("Free-text lines")).toBeInTheDocument();
  });

  it("ages what is delivered and not invoiced", async () => {
    analysis();
    renderRoute("/orders/analysis");
    await userEvent.click(await screen.findByRole("tab", { name: "Backlog" }));
    const delivered = await screen.findByTestId("backlog-delivered");
    expect(within(delivered).getAllByText(/400\.00/).length).toBeGreaterThan(0);
    expect(within(await screen.findByTestId("backlog-ordered")).getByText("2 orders")).toBeInTheDocument();
  });

  it("is closed without the analysis key", async () => {
    analysis();
    renderRoute("/orders/analysis", { canViewAnalysis: false });
    expect(await screen.findByText(/needs the View sales analysis permission/)).toBeInTheDocument();
  });
});
