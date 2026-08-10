import { describe, expect, it } from "vitest";
import { buildCategoryTree, type CategoryResponse, categoryPath } from "./categories";

const categories: CategoryResponse[] = [
  { id: 1, name: "Furniture", parentId: null },
  { id: 2, name: "Desks", parentId: 1 },
  { id: 3, name: "Lighting", parentId: 1 },
  { id: 4, name: "Services", parentId: null },
];

describe("buildCategoryTree", () => {
  it("flattens the hierarchy depth-first with depths", () => {
    const tree = buildCategoryTree(categories);

    expect(tree.map(({ category, depth }) => [category.name, depth])).toEqual([
      ["Furniture", 0],
      ["Desks", 1],
      ["Lighting", 1],
      ["Services", 0],
    ]);
  });

  it("sorts siblings alphabetically", () => {
    const tree = buildCategoryTree([
      { id: 1, name: "Zebra", parentId: null },
      { id: 2, name: "Alpha", parentId: null },
    ]);

    expect(tree.map(({ category }) => category.name)).toEqual(["Alpha", "Zebra"]);
  });

  it("returns an empty list for no categories", () => {
    expect(buildCategoryTree([])).toEqual([]);
  });
});

describe("categoryPath", () => {
  it("returns the root-to-leaf path", () => {
    expect(categoryPath(categories, 2)).toBe("Furniture / Desks");
  });

  it("returns just the name for a root category", () => {
    expect(categoryPath(categories, 4)).toBe("Services");
  });

  it("returns an empty string for an unknown id", () => {
    expect(categoryPath(categories, 999)).toBe("");
  });
});
