import { describe, expect, it } from "vitest";
import { sourceHref, unbuildHref } from "./routes";

describe("sourceHref", () => {
  const production = { module: "manufacturing", kind: "production", id: "9", lineId: "5" };

  it("links a production to its manufacturing order, kept as the source line id", () => {
    expect(sourceHref(production, ["inventory", "manufacturing"])).toBe("/manufacturing/orders/5");
    expect(sourceHref(production, undefined)).toBe("/manufacturing/orders/5");
  });

  it("links an unbuild to its page in Manufacturing (manufacturing phase 2 M123)", () => {
    const unbuild = { module: "manufacturing", kind: "unbuild", id: "4", lineId: "4" };
    expect(sourceHref(unbuild, ["inventory", "manufacturing"])).toBe("/manufacturing/unbuilds/4");
    expect(sourceHref(unbuild, ["inventory"])).toBeUndefined();
    expect(sourceHref({ ...unbuild, id: "x" }, undefined)).toBeUndefined();
  });

  it("links a hold an unbuild put to the unbuild (M123)", () => {
    const hold = { module: "manufacturing", kind: "unbuild", id: "4" };
    expect(unbuildHref(hold, undefined)).toBe("/manufacturing/unbuilds/4");
    expect(unbuildHref(hold, ["inventory"])).toBeUndefined();
    expect(unbuildHref({ ...hold, kind: "deviation" }, undefined)).toBeUndefined();
  });

  it("links nothing when Manufacturing is not mounted, or for another source", () => {
    expect(sourceHref(production, ["inventory"])).toBeUndefined();
    expect(sourceHref({ ...production, lineId: undefined }, undefined)).toBeUndefined();
    expect(sourceHref({ ...production, kind: "order" }, undefined)).toBeUndefined();
    expect(sourceHref({ module: "orders", kind: "delivery", id: "3" }, undefined)).toBeUndefined();
    expect(sourceHref(undefined, undefined)).toBeUndefined();
  });
});
