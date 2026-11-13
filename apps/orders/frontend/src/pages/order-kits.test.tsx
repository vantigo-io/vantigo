import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { Order } from "../api/types";
import { commonAnswers, fakeServer, jsonResponse } from "../test/api";
import { line, order, settings } from "../test/fixtures";
import { renderRoute } from "../test/render";

// A kit line's child lines (manufacturing phase 2 M145, M146): shown under
// their kit, never edited, never sent.

const url = "/api/v1/orders/orders/2001";

const kitOrder = (): Order =>
  order({
    status: "confirmed",
    confirmedAt: "2026-11-02T08:00:00Z",
    version: 3,
    lines: [
      line({ id: 5001, sku: "SETT-1", name: "Verktøysett", quantity: 2, deliveryMethod: "delivery", available: 3 }),
      line({
        id: 5002,
        position: 2,
        variantId: 302,
        sku: "HYL-1",
        name: "Hylle",
        quantity: 4,
        unitPrice: 0,
        displayUnitPrice: 0,
        lineGross: 0,
        lineNet: 0,
        deliveryMethod: "delivery",
        qtyReserved: 4,
        parentLineId: 5001,
        perKit: 2,
      }),
    ],
  });

describe("an order's kit lines", () => {
  it("shows the kit's children under it and saves the kit alone", async () => {
    const fake = fakeServer((call) => {
      if (call.method === "GET" && call.url === url) return jsonResponse(200, kitOrder());
      if (call.method === "GET" && call.url === `${url}/changes`) return jsonResponse(200, { items: [] });
      if (call.method === "PUT") return jsonResponse(200, kitOrder());
      return commonAnswers(call, settings());
    });
    renderRoute("/orders/orders/2001");

    const child = await screen.findByTestId("kit-child-row");
    expect(child).toHaveTextContent("HYL-1 · Hylle");
    expect(child).toHaveTextContent("4 stk · 2 per kit");
    expect(within(child).getByTestId("line-stock")).toHaveTextContent("Reserved 4");
    expect(screen.getAllByTestId("line-row")).toHaveLength(1);
    expect(screen.getByText("Available 3")).toBeInTheDocument();

    const quantity = screen.getByLabelText("Quantity of line 1");
    await userEvent.clear(quantity);
    await userEvent.type(quantity, "3");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(fake.sent("PUT", url)).toBeDefined());
    const body = fake.sent("PUT", url)?.body as { lines: Record<string, unknown>[] };
    expect(body.lines).toHaveLength(1);
    expect(body.lines[0]).toMatchObject({ id: 5001, quantity: 3 });
  });
});
