import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { PurchasingReceivingOrder } from "../api/purchasing";
import { fakeServer, jsonResponse } from "../test/api";
import { commonAnswers, receiveForm } from "../test/fixtures";
import { renderRoute } from "../test/render";

const toReceive = (overrides: Partial<PurchasingReceivingOrder> = {}): PurchasingReceivingOrder => ({
  id: 501,
  number: 3001,
  status: "partially_received",
  supplierName: "Skruer AS",
  siteId: 1,
  siteName: "Hovedlager",
  expectedOn: "2026-11-20",
  late: false,
  goodsLines: 3,
  openLines: 2,
  ...overrides,
});

const serve = (items: PurchasingReceivingOrder[]) =>
  fakeServer((call) => {
    if (call.method === "GET" && call.url === "/api/v1/purchasing/receiving") return jsonResponse(200, { items });
    if (call.method === "GET" && call.url === "/api/v1/purchasing/orders/501/receive")
      return jsonResponse(200, receiveForm());
    return commonAnswers(call);
  });

describe("the orders to receive", () => {
  it("lists each order with its supplier, site, expected day and lines left — no price — and opens its receive screen", async () => {
    serve([
      toReceive({ late: true, expectedOn: "2026-11-10" }),
      toReceive({ id: 502, number: 3002, status: "sent", openLines: 1, goodsLines: 1 }),
    ]);
    const { router } = renderRoute("/purchasing/receive", {
      canRead: false,
      canManageOrders: false,
      canSendOrders: false,
    });
    await screen.findByTestId("receiving-list");
    const [first, second] = screen.getAllByTestId("receiving-row");
    expect(within(first).getByText("Skruer AS")).toBeInTheDocument();
    expect(within(first).getByText("Hovedlager")).toBeInTheDocument();
    expect(within(first).getByText("2 of 3")).toBeInTheDocument();
    expect(within(first).getByText("Late")).toBeInTheDocument();
    expect(within(second).getByText("1 of 1")).toBeInTheDocument();
    expect(screen.getByTestId("receiving-list").textContent).not.toMatch(/NOK|\bkr\b|price/i);
    await userEvent.click(within(first).getByRole("link", { name: "3001" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/purchasing/receive/501"));
  });

  it("says so when nothing is to be received", async () => {
    serve([]);
    renderRoute("/purchasing/receive");
    expect(await screen.findByText("Nothing to receive")).toBeInTheDocument();
  });
});
