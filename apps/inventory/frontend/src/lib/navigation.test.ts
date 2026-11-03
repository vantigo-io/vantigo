import { describe, expect, it } from "vitest";
import { visibleInventoryNavItems } from "./navigation";

const keys = (permissions: string[] | undefined) => visibleInventoryNavItems(permissions).map((item) => item.key);

describe("the sidebar entries", () => {
  it("offers the overview to either view, and the movements only to both", () => {
    expect(keys(["inventory:locations-view"])).toEqual(["overview"]);
    expect(keys(["inventory:stock-view"])).toEqual(["overview"]);
    expect(keys(["inventory:locations-view", "inventory:stock-view"])).toEqual(["overview", "movements", "receipts"]);
  });

  it("offers everything to a holder of every permission, and nothing to nobody", () => {
    expect(keys(["*"])).toEqual(["overview", "movements", "receipts"]);
    expect(keys(undefined)).toEqual([]);
  });
});
