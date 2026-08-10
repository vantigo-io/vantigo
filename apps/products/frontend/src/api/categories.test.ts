import { describe, expect, it } from "vitest";
import { buildCategoryTree, type CategoryResponse, categoryPath, maxDepth, subtreeProductCounts } from "./categories";

const categories: CategoryResponse[] = [
  { id: 1, name: "Furniture", parentId: null, productCount: 1 },
  { id: 2, name: "Desks", parentId: 1, productCount: 3 },
  { id: 3, name: "Lighting", parentId: 1, productCount: 2 },
  { id: 4, name: "Services", parentId: null, productCount: 0 },
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
      { id: 1, name: "Zebra", parentId: null, productCount: 0 },
      { id: 2, name: "Alpha", parentId: null, productCount: 0 },
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

describe("subtreeProductCounts", () => {
  it("rolls descendant counts up into parents", () => {
    const totals = subtreeProductCounts(categories);

    expect(totals.get(1)).toBe(6); // 1 direct + 3 (Desks) + 2 (Lighting)
    expect(totals.get(2)).toBe(3);
    expect(totals.get(3)).toBe(2);
    expect(totals.get(4)).toBe(0);
  });

  it("handles multiple levels of nesting", () => {
    const totals = subtreeProductCounts([
      { id: 1, name: "Root", parentId: null, productCount: 1 },
      { id: 2, name: "Child", parentId: 1, productCount: 1 },
      { id: 3, name: "Grandchild", parentId: 2, productCount: 1 },
    ]);

    expect(totals.get(1)).toBe(3);
    expect(totals.get(2)).toBe(2);
    expect(totals.get(3)).toBe(1);
  });
});

describe("maxDepth", () => {
  it("counts nesting levels", () => {
    expect(maxDepth(categories)).toBe(2);
    expect(maxDepth([])).toBe(0);
    expect(
      maxDepth([
        { id: 1, name: "Root", parentId: null, productCount: 0 },
        { id: 2, name: "Child", parentId: 1, productCount: 0 },
        { id: 3, name: "Grandchild", parentId: 2, productCount: 0 },
      ]),
    ).toBe(3);
  });
});
