import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { ManufacturingUnbuild } from "../api/orders";
import { bomCreateBody, editorProblems, emptyEditor, newOutput } from "../lib/bom-body";
import { jsonResponse } from "../test/api";
import { sent } from "../test/helpers";
import { renderRoute } from "../test/route-tree";
import { orderFixture, stubManufacturingApi } from "../test/server";

// BoM depth (manufacturing phase 2 M120–M128): by-products in the editor's
// body, the order's by-products, Split, Unbuild, Merge and the unbuilds list.

const ref = (id: number, sku: string) => ({
  id,
  sku,
  productName: sku,
  unit: "pcs",
  productType: "goods",
  productStatus: "active",
});

describe("the BoM editor's by-products", () => {
  it("sends every by-product with its share, and judges the shares together", () => {
    const state = emptyEditor(ref(1, "TABLE"));
    state.outputs = [
      { ...newOutput(ref(2, "SAWDUST")), quantity: "0,5", costShare: "10" },
      newOutput(ref(3, "OFFCUT")),
    ];
    expect(bomCreateBody(state).outputs).toEqual([
      { variantId: 2, quantity: "0.5", costShare: "10", sortOrder: 10 },
      { variantId: 3, quantity: "1", costShare: "0", sortOrder: 20 },
    ]);
    state.outputs[1].costShare = "95";
    expect(editorProblems(state).outputs).toBe("costShareOver");
    state.outputs[1].costShare = "101";
    expect(editorProblems(state)["outputs[1].costShare"]).toBe("costShare");
  });
});

describe("the order's by-products, split and unbuild", () => {
  it("shows the by-products and splits a confirmed order into parts that sum to it", async () => {
    const order = orderFixture({
      outputs: [
        {
          id: 81,
          variant: ref(2, "SAWDUST"),
          expected: "5",
          produced: "0",
          costShare: "10",
          tracking: "none",
        },
      ],
    });
    const fetchMock = stubManufacturingApi({
      meta: { canManageOrders: true },
      orders: [order],
      write: (method, path) =>
        method === "POST" && path === "/orders/5/split"
          ? jsonResponse(200, {
              order: { ...order, quantity: "4" },
              parts: [{ id: 6, number: "MO-12-2", status: "confirmed", quantity: "6", produced: "0", variantId: 1 }],
              shortLines: [],
            })
          : undefined,
    });
    renderRoute("/manufacturing/orders/5");
    const section = (await screen.findByRole("heading", { name: "By-products" })).closest("section") as HTMLElement;
    expect(within(section).getAllByText("SAWDUST").length).toBeGreaterThan(0);

    await userEvent.click(screen.getByRole("button", { name: "Split" }));
    const dialog = await screen.findByRole("dialog");
    const button = within(dialog).getByRole("button", { name: "Split" });
    await userEvent.type(within(dialog).getByLabelText("MO-12 keeps"), "4");
    await userEvent.type(within(dialog).getByLabelText("Part 2"), "5");
    expect(button).toBeDisabled();
    await userEvent.clear(within(dialog).getByLabelText("Part 2"));
    await userEvent.type(within(dialog).getByLabelText("Part 2"), "6");
    await userEvent.click(button);
    await waitFor(() => expect(sent(fetchMock, "POST", /\/orders\/5\/split$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/orders\/5\/split$/)[0].body).toEqual({ revision: 4, quantities: ["4", "6"] });
  });

  it("unbuilds a lot of a done order, at most what is left of it", async () => {
    const order = orderFixture({ status: "done", produced: "10" });
    const unbuild: ManufacturingUnbuild = {
      id: 1,
      number: "UB-1",
      orderId: 5,
      orderNumber: "MO-12",
      bomId: 7,
      variant: ref(1, "TABLE"),
      quantity: "1",
      lotId: 900,
      lotNumber: "MO-12",
      siteId: 1,
      fromLocationId: 10,
      toLocationId: 10,
      movementId: 3,
      note: "",
      clientRequestId: "x",
      postedByUserId: null,
      postedByDisplay: "Kari",
      postedAt: "2026-11-11T09:00:00Z",
      lines: [{ variant: ref(2, "BOARD"), quantity: "4", lotId: null, lotNumber: null }],
      quarantined: false,
      releasedAt: null,
      releasedByDisplay: null,
    };
    const fetchMock = stubManufacturingApi({
      meta: { canManageOrders: true },
      orders: [order],
      orderLots: {
        5: {
          outputs: [{ lotId: 900, lotNumber: "MO-12", serial: false, quantity: "10", productions: 1, unbuilt: "8" }],
          components: [],
          byProducts: [],
          tracking: "lot",
        },
      },
      write: (method, path) => (method === "POST" && path === "/unbuilds" ? jsonResponse(201, unbuild) : undefined),
    });
    renderRoute("/manufacturing/orders/5");
    await userEvent.click(await screen.findByRole("button", { name: "Unbuild" }));
    const dialog = await screen.findByRole("dialog");
    await userEvent.click(await within(dialog).findByRole("combobox", { name: "Lot" }));
    await userEvent.click(await screen.findByRole("option", { name: "MO-12 · 2 pcs left" }));
    const amount = within(dialog).getByLabelText("Quantity");
    await userEvent.type(amount, "3");
    expect(await within(dialog).findByText("More than is left to take apart.")).toBeInTheDocument();
    await userEvent.clear(amount);
    await userEvent.type(amount, "1");
    await userEvent.click(within(dialog).getByRole("button", { name: "Unbuild" }));
    expect(await within(dialog).findByText("BOARD · 4 pcs")).toBeInTheDocument();
    const [post] = sent(fetchMock, "POST", /\/unbuilds$/);
    expect(post.body).toMatchObject({ orderId: 5, quantity: "1", lotId: 900 });
  });
});

describe("merge and the unbuilds list", () => {
  it("merges the chosen orders with the revisions read again", async () => {
    const fetchMock = stubManufacturingApi({
      meta: { canManageOrders: true },
      orders: [orderFixture(), orderFixture({ id: 6, number: "MO-13", revision: 7 })],
      write: (method, path) =>
        method === "POST" && path === "/orders/merge"
          ? jsonResponse(200, { order: orderFixture(), merged: [], shortLines: [] })
          : undefined,
    });
    renderRoute("/manufacturing/orders");
    await userEvent.click(await screen.findByLabelText("Choose MO-12 to merge"));
    await userEvent.click(screen.getByLabelText("Choose MO-13 to merge"));
    await userEvent.click(screen.getByRole("button", { name: "Merge 2" }));
    await userEvent.click(await screen.findByRole("button", { name: "Merge" }));
    await waitFor(() => expect(sent(fetchMock, "POST", /\/orders\/merge$/)).toHaveLength(1));
    expect(sent(fetchMock, "POST", /\/orders\/merge$/)[0].body).toEqual({
      orders: [
        { id: 5, revision: 4 },
        { id: 6, revision: 7 },
      ],
    });
  });

  it("lists the unbuilds with what each restored", async () => {
    stubManufacturingApi({
      unbuilds: [
        {
          id: 1,
          number: "UB-1",
          orderId: null,
          orderNumber: null,
          bomId: 7,
          variant: ref(1, "TABLE"),
          quantity: "2",
          lotId: null,
          lotNumber: null,
          siteId: 1,
          fromLocationId: 10,
          toLocationId: 10,
          movementId: 3,
          note: "",
          clientRequestId: "x",
          postedByUserId: null,
          postedByDisplay: "Kari",
          postedAt: "2026-11-11T09:00:00Z",
          lines: [{ variant: ref(2, "BOARD"), quantity: "8", lotId: null, lotNumber: null }],
          quarantined: false,
          releasedAt: null,
          releasedByDisplay: null,
        },
      ],
    });
    renderRoute("/manufacturing/unbuilds");
    expect(await screen.findByText("UB-1")).toBeInTheDocument();
    expect(screen.getByText("By the bill of materials")).toBeInTheDocument();
    expect(screen.getByText("BOARD · 8 pcs")).toBeInTheDocument();
  });
});
