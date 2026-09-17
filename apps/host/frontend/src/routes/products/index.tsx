import { createFileRoute } from "@tanstack/react-router";
import { ProductsPage } from "@vantigo/products-ui/pages/products.index";
export const Route = createFileRoute("/products/")({
  validateSearch: (search: Record<string, unknown>) => ({
    page: Math.max(1, Number(search.page) || 1),
    search: typeof search.search === "string" ? search.search : "",
    status: ["Draft", "Active", "Discontinued"].includes(String(search.status))
      ? (String(search.status) as "Draft" | "Active" | "Discontinued")
      : "",
    categoryId:
      Number.isInteger(Number(search.categoryId)) && Number(search.categoryId) > 0 ? Number(search.categoryId) : "",
    // Present only when true: the create form opens on arrival (Spotlight's quick action).
    ...(search.create === true || search.create === "true" ? { create: true as const } : {}),
  }),
  component: ProductsPage,
});
