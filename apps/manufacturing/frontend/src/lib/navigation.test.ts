import { describe, expect, it } from "vitest";
import { manufacturingNavItems, visibleManufacturingNavItems } from "./navigation";

const keys = (permissions: string[] | undefined) => visibleManufacturingNavItems(permissions).map((item) => item.key);

describe("the sidebar entries", () => {
  it("are deliveries A's and B's four, in M7's order", () => {
    expect(manufacturingNavItems.map((item) => [item.key, item.to])).toEqual([
      ["overview", "/manufacturing"],
      ["orders", "/manufacturing/orders"],
      ["boms", "/manufacturing/boms"],
      ["workCenters", "/manufacturing/work-centers"],
    ]);
  });

  it("are all offered by manufacturing:access, and by nothing else", () => {
    expect(keys(["manufacturing:access"])).toEqual(["overview", "orders", "boms", "workCenters"]);
    expect(keys(["*"])).toEqual(["overview", "orders", "boms", "workCenters"]);
    expect(keys(["manufacturing:boms-manage"])).toEqual([]);
    expect(keys(["manufacturing:costs-view"])).toEqual([]);
    expect(keys(undefined)).toEqual([]);
  });
});
