import { describe, expect, it } from "vitest";
import { orderFixture } from "../test/server";
import { orderInputOf, orderUpdateOf, orderValuesOf } from "./order-body";

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
