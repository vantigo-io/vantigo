import { queryOptions } from "@tanstack/react-query";
import { request } from "./request";

export { ApiValidationError, NotFoundError } from "./request";

export interface CategoryResponse {
  id: number;
  name: string;
  parentId: number | null;
  /** Products directly assigned to this category; subtree totals are derived client-side. */
  productCount: number;
}

export interface CategoryInput {
  name: string;
  parentId?: number | null;
}

export const categoriesQueryOptions = () =>
  queryOptions({
    queryKey: ["categories"],
    queryFn: ({ signal }) => request<CategoryResponse[]>("/api/v1/products/categories", { signal }),
  });

export const createCategory = (input: CategoryInput) =>
  request<CategoryResponse>("/api/v1/products/categories", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

export const updateCategory = (id: number, input: CategoryInput) =>
  request<CategoryResponse>(`/api/v1/products/categories/${id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

export const deleteCategory = (id: number) => request<void>(`/api/v1/products/categories/${id}`, { method: "DELETE" });

export interface CategoryTreeItem {
  category: CategoryResponse;
  depth: number;
}

/**
 * Flattens the adjacency list into a depth-first list with depth per row, sorted
 * alphabetically within each level. Used for indented selects and the management list.
 */
export const buildCategoryTree = (categories: CategoryResponse[]): CategoryTreeItem[] => {
  const byParent = new Map<number | null, CategoryResponse[]>();
  for (const category of categories) {
    const key = category.parentId;
    const bucket = byParent.get(key);
    if (bucket) bucket.push(category);
    else byParent.set(key, [category]);
  }
  for (const bucket of byParent.values()) bucket.sort((a, b) => a.name.localeCompare(b.name));

  const result: CategoryTreeItem[] = [];
  const visit = (parentId: number | null, depth: number) => {
    for (const category of byParent.get(parentId) ?? []) {
      result.push({ category, depth });
      visit(category.id, depth + 1);
    }
  };
  visit(null, 0);
  return result;
};

/** Returns the root→leaf path of names for a category, e.g. "Furniture / Desks". */
export const categoryPath = (categories: CategoryResponse[], id: number): string => {
  const byId = new Map(categories.map((category) => [category.id, category]));
  const names: string[] = [];
  let current = byId.get(id);
  while (current) {
    names.unshift(current.name);
    current = current.parentId === null ? undefined : byId.get(current.parentId);
  }
  return names.join(" / ");
};

/**
 * Returns the total number of products per category including all descendants,
 * keyed by category id. Computed bottom-up from the direct counts in the flat list.
 */
export const subtreeProductCounts = (categories: CategoryResponse[]): Map<number, number> => {
  const totals = new Map(categories.map((category) => [category.id, category.productCount]));
  // Deepest-first order guarantees children are folded into parents exactly once.
  for (const { category } of buildCategoryTree(categories).reverse()) {
    if (category.parentId !== null) {
      totals.set(category.parentId, (totals.get(category.parentId) ?? 0) + (totals.get(category.id) ?? 0));
    }
  }
  return totals;
};

/** Returns the deepest nesting level, where a single flat level counts as 1. */
export const maxDepth = (categories: CategoryResponse[]): number =>
  buildCategoryTree(categories).reduce((deepest, { depth }) => Math.max(deepest, depth + 1), 0);
