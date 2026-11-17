import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { fakeServer, jsonResponse } from "../test/api";
import { commonAnswers, summary } from "../test/fixtures";
import { renderRoute } from "../test/render";

const listAnswer = (items = [summary()]) =>
  fakeServer((call) =>
    call.url.startsWith("/api/v1/purchasing/orders")
      ? jsonResponse(200, { items, total: items.length, page: 1, pageSize: 25 })
      : commonAnswers(call),
  );

describe("the purchase order list", () => {
  it("lists each order with its state, supplier, expected day and received and invoiced as fractions", async () => {
    listAnswer([
      summary({ late: true }),
      summary({ id: 502, number: null, status: "draft", goodsLines: 0, lineCount: 0, late: false }),
    ]);
    renderRoute("/purchasing/orders");

    const rows = within(await screen.findByTestId("purchase-orders")).getAllByRole("row");
    expect(within(rows[1]).getByRole("link", { name: "3001" })).toHaveAttribute("href", "/purchasing/orders/501");
    expect(within(rows[1]).getByRole("link", { name: "Skruer AS" })).toHaveAttribute(
      "href",
      "/purchasing/suppliers/40",
    );
    expect(within(rows[1]).getByText("Sent")).toBeInTheDocument();
    expect(within(rows[1]).getByText("Late")).toBeInTheDocument();
    expect(within(rows[1]).getByText("1 / 4")).toBeInTheDocument();
    expect(within(rows[1]).getByText("0 / 5")).toBeInTheDocument();
    expect(within(rows[2]).getByRole("link", { name: "Draft" })).toHaveAttribute("href", "/purchasing/orders/502");
    expect(screen.getByText("2 purchase orders")).toBeInTheDocument();
  });

  it("asks the server for the chosen states, Late, Unconfirmed, Mine and a search", async () => {
    const fake = listAnswer();
    renderRoute("/purchasing/orders");
    await screen.findByTestId("purchase-orders");

    await userEvent.click(screen.getByRole("checkbox", { name: "Sent" }));
    await userEvent.click(screen.getByRole("checkbox", { name: "Partially received" }));
    await waitFor(() =>
      expect(fake.calls.some((c) => c.url.includes("status=sent&status=partially_received"))).toBe(true),
    );
    await userEvent.click(screen.getByRole("switch", { name: "Late" }));
    await waitFor(() => expect(fake.calls.some((c) => c.url.includes("late=true"))).toBe(true));
    await userEvent.click(screen.getByRole("switch", { name: "Unconfirmed" }));
    await waitFor(() => expect(fake.calls.some((c) => c.url.includes("unconfirmed=true"))).toBe(true));
    await userEvent.click(screen.getByRole("switch", { name: "Mine" }));
    await waitFor(() => expect(fake.calls.some((c) => c.url.includes("mine=true"))).toBe(true));
    await userEvent.type(screen.getByRole("textbox", { name: "Search" }), "LEV-REF");
    await waitFor(() => expect(fake.calls.some((c) => c.url.includes("q=LEV-REF"))).toBe(true));
  });

  it("offers New purchase order only with purchasing:orders-manage", async () => {
    listAnswer();
    renderRoute("/purchasing/orders", { canManageOrders: false });
    await screen.findByTestId("purchase-orders");
    expect(screen.queryByRole("link", { name: "New purchase order" })).not.toBeInTheDocument();
  });
});
