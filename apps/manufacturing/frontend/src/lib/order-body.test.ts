import { describe, expect, it } from "vitest";
import { orderFixture } from "../test/server";
import { orderInputOf, orderUpdateOf, orderValuesOf } from "./order-body";
import { subAssemblyLines } from "./sub-assembly";

const defaults = { bomId: null, siteId: null, outputLocationId: null };

describe("an order's update body", () => {
  it("always sends the requested start, null when it is left empty, so the server takes its default (M35a)", () => {
    const order = orderFixture();
    expect(orderUpdateOf(orderValuesOf(order), order, defaults)).toMatchObject({ requestedStart: null, revision: 4 });
  });

  it("sends the requested start set on a confirmed order", () => {
    const order = orderFixture({ requestedStart: "2026-11-13" });
    const values = orderValuesOf(order);
    expect(values.requestedStart).toBe("2026-11-13");
    expect(orderUpdateOf({ ...values, requestedStart: "2026-11-18" }, order, defaults)).toMatchObject({
      requestedStart: "2026-11-18",
      quantity: "10",
      bomId: 7,
    });
  });

  it("sends a new draft's requested start when one is set, and leaves it out otherwise (M35a)", () => {
    const values = orderValuesOf(undefined);
    expect(orderInputOf(values, defaults)).not.toHaveProperty("requestedStart");
    expect(orderInputOf({ ...values, requestedStart: "2026-11-20" }, defaults)).toMatchObject({
      requestedStart: "2026-11-20",
    });
  });

  it("never sends a planned window, which only planning sets (M34)", () => {
    const order = orderFixture({ plannedStart: "2026-11-12T07:00:00Z", plannedFinish: "2026-11-12T09:00:00Z" });
    const update = orderUpdateOf(orderValuesOf(order), order, defaults);
    const input = orderInputOf(orderValuesOf(undefined), defaults);
    for (const body of [update, input]) {
      expect(body).not.toHaveProperty("plannedStart");
      expect(body).not.toHaveProperty("plannedFinish");
    }
  });
});

describe("a new order's sales order line (M45)", () => {
  it("is sent only when the order is made for one", () => {
    const values = orderValuesOf(undefined);
    expect(orderInputOf(values, defaults)).not.toHaveProperty("salesOrderLineId");
    expect(orderInputOf({ ...values, salesOrderLineId: 501 }, defaults)).toMatchObject({ salesOrderLineId: 501 });
  });
});

describe("the components a sub-assembly order can be made for (M47)", () => {
  it("are the stored lines with a default BoM that miss something", () => {
    const [line] = orderFixture().components;
    const order = orderFixture({
      components: [
        { ...line, id: 1, defaultBomId: 9, missing: "2" },
        { ...line, id: 2, defaultBomId: 9, missing: "0" },
        { ...line, id: 3, defaultBomId: null, missing: "2" },
        { ...line, id: null, defaultBomId: 9, missing: "2" },
      ],
    });
    expect(subAssemblyLines(order).map((one) => one.line.id)).toEqual([1]);
  });

  it("miss only what the open sub-assembly orders of the variant do not make", () => {
    const [line] = orderFixture().components;
    const child = (id: number, status: "confirmed" | "done" | "cancelled", quantity: string, produced: string) => ({
      id,
      number: `MO-${id}`,
      status,
      quantity,
      produced,
      variantId: line.variant.id,
    });
    const order = orderFixture({
      components: [{ ...line, id: 1, defaultBomId: 9, missing: "5" }],
      children: [child(20, "confirmed", "3", "1"), child(21, "done", "4", "4"), child(22, "cancelled", "9", "0")],
    });
    expect(subAssemblyLines(order)).toEqual([{ line: order.components[0], missing: "3" }]);
    const covered = orderFixture({
      components: [{ ...line, id: 1, defaultBomId: 9, missing: "2" }],
      children: [child(20, "confirmed", "2", "0")],
    });
    expect(subAssemblyLines(covered)).toEqual([]);
  });
});
