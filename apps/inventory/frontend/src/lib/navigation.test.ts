import { describe, expect, it } from "vitest";
import { visibleInventoryNavItems } from "./navigation";

const keys = (permissions: string[] | undefined) => visibleInventoryNavItems(permissions).map((item) => item.key);

describe("the sidebar entries", () => {
  it("offers the overview to either view, and the movements only to both", () => {
    expect(keys(["inventory:locations-view"])).toEqual(["overview"]);
    expect(keys(["inventory:stock-view"])).toEqual(["overview"]);
    expect(keys(["inventory:locations-view", "inventory:stock-view"])).toEqual([
      "overview",
      "lots",
      "transfers",
      "movements",
      "receipts",
      "lowStock",
    ]);
  });

  it("offers Counts to counts-manage and Import to stock-adjust, each with both views", () => {
    const views = ["inventory:locations-view", "inventory:stock-view"];
    expect(keys([...views, "inventory:counts-manage"])).toEqual([
      "overview",
      "lots",
      "transfers",
      "movements",
      "receipts",
      "counts",
      "lowStock",
    ]);
    expect(keys([...views, "inventory:stock-adjust"])).toEqual([
      "overview",
      "lots",
      "transfers",
      "movements",
      "receipts",
      "lowStock",
      "import",
    ]);
    expect(keys(["inventory:locations-view", "inventory:counts-manage", "inventory:stock-adjust"])).toEqual([
      "overview",
    ]);
  });

  it("offers everything to a holder of every permission, and nothing to nobody", () => {
    expect(keys(["*"])).toEqual([
      "overview",
      "lots",
      "transfers",
      "movements",
      "receipts",
      "counts",
      "lowStock",
      "import",
    ]);
    expect(keys(undefined)).toEqual([]);
  });
});
