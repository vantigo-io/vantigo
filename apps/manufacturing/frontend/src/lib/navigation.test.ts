import { describe, expect, it } from "vitest";
import { manufacturingNavItems, visibleManufacturingNavItems } from "./navigation";

const keys = (permissions: string[] | undefined) => visibleManufacturingNavItems(permissions).map((item) => item.key);

describe("the sidebar entries", () => {
  it("are M7's six, the suggestions (phase 2 M111), the unbuilds (M123) and the deviations (M99), in order", () => {
    expect(manufacturingNavItems.map((item) => [item.key, item.to])).toEqual([
      ["overview", "/manufacturing"],
      ["orders", "/manufacturing/orders"],
      ["suggestions", "/manufacturing/suggestions"],
      ["unbuilds", "/manufacturing/unbuilds"],
      ["shopFloor", "/manufacturing/shop-floor"],
      ["schedule", "/manufacturing/schedule"],
      ["deviations", "/manufacturing/deviations"],
      ["boms", "/manufacturing/boms"],
      ["workCenters", "/manufacturing/work-centers"],
    ]);
  });

  it("are all offered by manufacturing:access, and by nothing else", () => {
    expect(keys(["manufacturing:access"])).toEqual([
      "overview",
      "orders",
      "suggestions",
      "unbuilds",
      "shopFloor",
      "schedule",
      "deviations",
      "boms",
      "workCenters",
    ]);
    expect(keys(["*"])).toEqual([
      "overview",
      "orders",
      "suggestions",
      "unbuilds",
      "shopFloor",
      "schedule",
      "deviations",
      "boms",
      "workCenters",
    ]);
    expect(keys(["manufacturing:boms-manage"])).toEqual([]);
    expect(keys(["manufacturing:costs-view"])).toEqual([]);
    expect(keys(undefined)).toEqual([]);
  });
});
