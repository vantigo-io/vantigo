import { queryOptions } from "@tanstack/react-query";
import { request } from "./request";

export { ApiValidationError, NotFoundError } from "./request";

export interface CategoryResponse {
  id: number;
  name: string;
  parentId: number | null;
}

export interface CategoryInput {
  name: string;
  parentId?: number | null;
}

export const categoriesQueryOptions = () =>
  queryOptions({
    queryKey: ["categories"],
    queryFn: ({ signal }) => request<CategoryResponse[]>("/api/v1/categories", { signal }),
  });

export const createCategory = (input: CategoryInput) =>
  request<CategoryResponse>("/api/v1/categories", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

export const updateCategory = (id: number, input: CategoryInput) =>
  request<CategoryResponse>(`/api/v1/categories/${id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });

export const deleteCategory = (id: number) => request<void>(`/api/v1/categories/${id}`, { method: "DELETE" });

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
