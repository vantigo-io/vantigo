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
  }),
  component: ProductsPage,
});
