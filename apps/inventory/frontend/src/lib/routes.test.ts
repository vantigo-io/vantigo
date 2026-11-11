import { describe, expect, it } from "vitest";
import { sourceHref } from "./routes";

describe("sourceHref", () => {
  const production = { module: "manufacturing", kind: "production", id: "9", lineId: "5" };

  it("links a production to its manufacturing order, kept as the source line id", () => {
    expect(sourceHref(production, ["inventory", "manufacturing"])).toBe("/manufacturing/orders/5");
    expect(sourceHref(production, undefined)).toBe("/manufacturing/orders/5");
  });

  it("links nothing when Manufacturing is not mounted, or for another source", () => {
    expect(sourceHref(production, ["inventory"])).toBeUndefined();
    expect(sourceHref({ ...production, lineId: undefined }, undefined)).toBeUndefined();
    expect(sourceHref({ module: "orders", kind: "delivery", id: "3" }, undefined)).toBeUndefined();
    expect(sourceHref(undefined, undefined)).toBeUndefined();
  });
});
